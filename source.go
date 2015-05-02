package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

type Source struct {
	label            string
	sinkCount        uint64
	todo             []byte
	frames           [][]byte
	frameLocks       []sync.RWMutex
	frameBytes       uint64
	gone             bool
	header           []byte
	HeaderBytes      uint64
	input            io.ReadCloser
	nextFrame        uint64 // How many frames have ever been here
	execArgs         []string
	cmd              *exec.Cmd
	path             string
	closeIdle        bool
	reopen           bool
	bandwidth        uint64
	clientMaxBytes   uint64
	filter           FilterFunc
	filterContext    interface{}
	sync.RWMutex     // Must be held while changing nextFrame or gone
	*sync.Cond       // Control access to frames other than nextFrame
	statBytesInvalid uint64
	statBytesIn      uint64
	statBytesOut     uint64
	statLast         time.Time
	statLogInterval  time.Duration
	sourceMap        *SourceMap
}

func NewSource(path string, c *Config, sourceMap *SourceMap) (s *Source) {
	s = &Source{}
	s.path = path
	s.sourceMap = sourceMap
	s.Cond = sync.NewCond(s.RLocker())
	s.frameLocks = make([]sync.RWMutex, c.SourceBuffer)
	s.frames = make([][]byte, c.SourceBuffer)
	for i := range s.frames {
		s.frames[i] = make([]byte, c.FrameBytes)
	}
	s.todo = make([]byte, 0, c.FrameBytes)
	s.bandwidth = c.SourceBandwidth
	s.clientMaxBytes = c.ClientMaxBytes
	s.closeIdle = c.CloseIdle
	s.frameBytes = c.FrameBytes
	s.HeaderBytes = c.HeaderBytes
	s.reopen = c.Reopen
	s.statLogInterval = c.StatLogInterval
	s.filter = Filters[c.FrameFilter]
	if c.ExecFlag {
		s.label = fmt.Sprintf("%v", c.Args)
		s.execArgs = c.Args
	} else {
		s.label = path
	}
	return
}

func (s *Source) openInputFile() (err error) {
	s.input, err = os.Open(s.path)
	return
}

func (s *Source) openInputCmd() (err error) {
	s.cmd = exec.Command(s.execArgs[0], s.execArgs[1:]...)
	s.input, err = s.cmd.StdoutPipe()
	if err != nil {
		s.cmd = nil
		return
	}
	err = s.cmd.Start()
	if err != nil {
		s.cmd = nil
		return
	}
	return
}

func (s *Source) openInput() (err error) {
	// Notify anyone waiting for the header to arrive
	defer s.Cond.Broadcast()
	if len(s.execArgs) > 0 {
		err = s.openInputCmd()
	} else {
		err = s.openInputFile()
	}
	if err != nil {
		log.Printf("source %s open: %s", s.label, err)
		return
	}
	log.Println("source", s.label, "opened")
	header := make([]byte, s.HeaderBytes)
	for pos := uint64(0); pos < s.HeaderBytes; {
		var got int
		if got, err = s.input.Read(header[pos:]); got == 0 {
			log.Printf("source %s read-header: %s", s.label, err)
			s.closeInput()
			return
		}
		pos += uint64(got)
	}
	if len(s.header) > 0 && bytes.Compare(header, s.header) != 0 {
		log.Printf("header mismatch: old %v, new %v", s.header, header)
		s.closeInput()
		return
	}
	s.Cond.L.Lock()
	s.header = header
	s.Cond.L.Unlock()
	s.statBytesIn += s.HeaderBytes
	return
}

func (s *Source) closeInput() {
	if s.input != nil {
		s.input.Close()
		s.input = nil
	}
	if s.cmd != nil {
		if s.cmd.Process != nil {
			s.cmd.Process.Kill()
		}
		s.cmd.Wait()
		s.cmd = nil
	}
}

func (s *Source) readNextFrame() (okFrameSize int, err error) {
	bufPos := s.nextFrame % uint64(cap(s.frames))
	s.frameLocks[bufPos].Lock()
	defer s.frameLocks[bufPos].Unlock()
	s.frames[bufPos] = s.frames[bufPos][:cap(s.frames[bufPos])]
	for frameEnd := 0; frameEnd < int(s.frameBytes); {
		if s.gone {
			return 0, io.EOF
		} else if len(s.todo) > 0 {
			copy(s.frames[bufPos][frameEnd:], s.todo)
			frameEnd += len(s.todo)
			s.todo = s.todo[:0]
		} else {
			got, err := s.input.Read(s.frames[bufPos][frameEnd:])
			if s.gone {
				return 0, io.EOF
			} else if got > 0 {
				frameEnd += got
				s.statBytesIn += uint64(got)
			} else if err != nil {
				return 0, err
			} else {
				// A Reader can return 0 bytes with
				// err==nil. Wait a bit, to avoid
				// spinning too hard.
				time.Sleep(10 * time.Millisecond)
				continue
			}
		}
		frameStart := 0
		for err != ErrShortFrame && frameStart < frameEnd {
			okFrameSize, s.filterContext, err = s.filter(s.frames[bufPos][frameStart:frameEnd], s.filterContext)
			switch err {
			case nil:
				s.todo = s.todo[:frameEnd-okFrameSize-frameStart]
				copy(s.todo, s.frames[bufPos][frameStart+okFrameSize:frameEnd])
				if frameStart > 0 {
					copy(s.frames[bufPos], s.frames[bufPos][frameStart:frameStart+okFrameSize])
				}
				s.frames[bufPos] = s.frames[bufPos][:okFrameSize]
				return
			case ErrInvalidFrame:
				// Try filter again on next byte
				frameStart++
				s.statBytesInvalid++
				err = nil
			default:
			}
		}
		// Shuffle the remaining bytes over and get more data
		copy(s.frames[bufPos], s.frames[bufPos][frameStart:frameEnd])
		frameEnd -= frameStart
		frameStart = 0
		err = nil
		okFrameSize = 0
	}
	return
}

// run() reads data from the input pipe into the buffer until the
// source reaches EOF and cannot be reopened. It then removes the
// source from sourceMap and returns.
func (s *Source) run() {
	var err error
	s.statLast = time.Now()
	defer s.LogStats()
	defer s.Close()
	if err := s.openInput(); err != nil {
		return
	}
	var ticker *time.Ticker
	if s.bandwidth > 0 {
		ticker = time.NewTicker(time.Duration(uint64(time.Second) * s.frameBytes / s.bandwidth))
	}
	var toThrottle int // #bytes read from source but not yet throttled by ticker
	var statTicker <-chan time.Time
	if s.statLogInterval > 0 {
		statTicker = (time.NewTicker(s.statLogInterval)).C
	} else {
		statTicker = make(chan time.Time)
	}
	for !s.gone {
		var frameSize int
		if frameSize, err = s.readNextFrame(); err != nil {
			log.Printf("source %s read: %s", s.label, err)
			s.closeInput()
			if s.gone || !s.reopen {
				// Shouldn't reopen
				break
			} else if err = s.openInput(); err != nil {
				// Failed reopen
				break
			} else {
				// Successful reopen
				continue
			}
		}
		s.nextFrame++
		s.Cond.Broadcast()
		if ticker != nil {
			toThrottle += frameSize
			for toThrottle >= int(s.frameBytes) {
				<-ticker.C
				toThrottle -= int(s.frameBytes)
			}
		}
		select {
		case <-statTicker:
			s.LogStats()
		default:
		}
	}
	s.closeInput()
}

// LogStats logs data source and client statistics (bytes in, skipped, out).
func (s *Source) LogStats() {
	log.Printf("source %s: %d activeclients, %d inbytes, %d invalidbytes, %d outbytes", s.label, s.sinkCount, s.statBytesIn, s.statBytesInvalid, s.statBytesOut)
}

func (s *Source) GetHeader(buf []byte) (int, error) {
	if s.HeaderBytes == 0 {
		return 0, nil
	}
	s.Cond.L.Lock()
	defer s.Cond.L.Unlock()
	for uint64(len(s.header)) < s.HeaderBytes && !s.gone {
		s.Cond.Wait()
	}
	if uint64(len(s.header)) < s.HeaderBytes {
		return 0, io.EOF
	}
	if len(buf) < len(s.header) {
		return 0, ErrBufferTooSmall
	}
	atomic.AddUint64(&s.statBytesOut, s.HeaderBytes)
	copy(buf, s.header)
	return int(s.HeaderBytes), nil
}

// NewReader returns a SourceReader that reads frames from this source.
func (s *Source) NewReader() *SourceReader {
	atomic.AddUint64(&s.sinkCount, 1)
	s.LogStats()
	return &SourceReader{source: s}
}

// Done is called by each SourceReader when it stops reading, so the
// Source can know whether it is idle.
func (s *Source) Done() {
	atomic.AddUint64(&s.sinkCount, ^uint64(0))
	s.LogStats()
	if s.closeIdle {
		s.closeIfIdle()
	}
}

// Make sure everyone waiting in Next() gives up. Prevents deadlock.
func (s *Source) disconnectAll() {
	if !s.gone {
		log.Println("source", s.label, "closing")
	}
	s.gone = true
	s.Broadcast()
}

// Close disconnects all clients and closes the source.
func (s *Source) Close() {
	s.closeIdle = true
	s.disconnectAll()
	s.closeInput()
}

func (s *Source) closeIfIdle() {
	didClose := false
	s.sourceMap.mutex.Lock()
	if s.sinkCount == 0 {
		delete(s.sourceMap.sources, s.path)
		didClose = true
	}
	s.sourceMap.mutex.Unlock()
	if didClose {
		s.Close()
	}
}

type SourceMap struct {
	sources map[string]*Source
	mutex   sync.RWMutex
}

func NewSourceMap() (sm *SourceMap) {
	sm = &SourceMap{sources: make(map[string]*Source)}
	return
}

// Count returns the number of open sources.
func (sm *SourceMap) Count() int {
	return len(sm.sources)
}

// Close closes all sources, disconnecting all of their clients.
func (sm *SourceMap) Close() {
	for _, src := range sm.sources {
		src.Close()
	}
}

// NewReader returns a SourceReader for the given path (URI) and
// config (argv). At any given time, there is at most one Source for a
// given path.
func (sm *SourceMap) NewReader(path string, c *Config) *SourceReader {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	var src *Source
	var ok bool
	if src, ok = sm.sources[path]; !ok {
		src = NewSource(path, c, sm)
		sm.sources[path] = src
		go src.run()
	}
	return src.NewReader()
}
