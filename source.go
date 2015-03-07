package main

import (
	"bytes"
	"errors"
	"io"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type Source struct {
	sinkCount        int
	todo             DataFrame
	frames           []DataFrame
	frameLocks       []sync.RWMutex
	frameBytes       uint64
	gone             bool
	header           []byte
	HeaderBytes      uint64
	input            io.ReadCloser
	nextFrame        uint64 // How many frames have ever been here
	path             string
	closeIdle        bool
	reopen           bool
	bandwidth        uint64
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
	s.sourceMap = sourceMap
	s.Cond = sync.NewCond(s.RLocker())
	s.frameLocks = make([]sync.RWMutex, c.SourceBuffer)
	s.frames = make([]DataFrame, c.SourceBuffer)
	for i := range s.frames {
		s.frames[i] = make(DataFrame, c.FrameBytes)
	}
	s.todo = make(DataFrame, 0, c.FrameBytes)
	s.bandwidth = c.SourceBandwidth
	s.closeIdle = c.CloseIdle
	s.frameBytes = c.FrameBytes
	s.HeaderBytes = c.HeaderBytes
	s.path = path
	s.reopen = c.Reopen
	s.statLogInterval = c.StatLogInterval
	s.filter = Filters[c.FrameFilter]
	return
}

func (s *Source) openInput() (err error) {
	// Notify anyone waiting for the header to arrive
	defer s.Cond.Broadcast()
	if s.input, err = os.Open(s.path); err != nil {
		log.Printf("source %s open: %s", s.path, err)
		return
	}
	header := make([]byte, s.HeaderBytes)
	for pos := uint64(0); pos < s.HeaderBytes; {
		var got int
		if got, err = s.input.Read(header[pos:]); got == 0 {
			log.Printf("source %s read-header: %s", s.path, err)
			s.input.Close()
			return
		}
		pos += uint64(got)
	}
	if len(s.header) > 0 && bytes.Compare(header, s.header) != 0 {
		log.Printf("header mismatch: old %v, new %v", s.header, header)
		s.input.Close()
		return
	}
	s.Cond.L.Lock()
	s.header = header
	s.Cond.L.Unlock()
	s.statBytesIn += s.HeaderBytes
	return
}

func (s *Source) readNextFrame() (err error) {
	bufPos := s.nextFrame % uint64(cap(s.frames))
	s.frameLocks[bufPos].Lock()
	defer s.frameLocks[bufPos].Unlock()
	s.frames[bufPos] = s.frames[bufPos][:cap(s.frames[bufPos])]
	for frameEnd := 0; frameEnd < int(s.frameBytes); {
		if s.gone {
			return io.EOF
		} else if len(s.todo) > 0 {
			copy(s.frames[bufPos][frameEnd:], s.todo)
			frameEnd += len(s.todo)
			s.todo = s.todo[:0]
		} else {
			got, err := s.input.Read(s.frames[bufPos][frameEnd:])
			if got > 0 {
				frameEnd += got
				s.statBytesIn += uint64(got)
			} else if err != nil {
				return err
			} else {
				// A Reader can return 0 bytes with
				// err==nil. Wait a bit, to avoid
				// spinning too hard.
				time.Sleep(10 * time.Millisecond)
				continue
			}
		}
		frameStart := 0
		for err != ShortFrame && frameStart < frameEnd {
			var okFrameSize int
			okFrameSize, err = s.filter(s.frames[bufPos][frameStart:frameEnd], &s.filterContext)
			switch err {
			case nil:
				s.todo = s.todo[:frameEnd-okFrameSize-frameStart]
				copy(s.todo, s.frames[bufPos][frameStart+okFrameSize:frameEnd])
				if frameStart > 0 {
					copy(s.frames[bufPos], s.frames[bufPos][frameStart:frameStart+okFrameSize])
				}
				s.frames[bufPos] = s.frames[bufPos][:okFrameSize]
				return nil
			case InvalidFrame:
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
	var statTicker <-chan time.Time
	if s.statLogInterval > 0 {
		statTicker = (time.NewTicker(s.statLogInterval)).C
	} else {
		statTicker = make(chan time.Time)
	}
	for !s.gone {
		if err = s.readNextFrame(); err != nil {
			log.Printf("source %s read: %s", s.path, err)
			s.input.Close()
			s.input = nil
			if !s.reopen {
				break
			} else if err = s.openInput(); err != nil {
				continue
			} else {
				break
			}
		}
		s.nextFrame++
		s.Cond.Broadcast()
		if ticker != nil {
			<-ticker.C
		}
		select {
		case <-statTicker:
			s.LogStats()
		default:
		}
	}
	if s.input != nil {
		s.input.Close()
	}
}

// LogStats logs data source and client statistics (bytes in, skipped, out).
func (s *Source) LogStats() {
	log.Printf("source %s stats: %d in (%d invalid) %d out", s.path, s.statBytesIn, s.statBytesInvalid, s.statBytesOut)
}

func (s *Source) GetHeader(buf []byte) (err error) {
	s.Cond.L.Lock()
	defer s.Cond.L.Unlock()
	for uint64(len(s.header)) < s.HeaderBytes && !s.gone {
		s.Cond.Wait()
	}
	if uint64(len(s.header)) < s.HeaderBytes {
		err = io.EOF
		return
	}
	if cap(buf) < len(s.header) {
		err = errors.New("Caller's header buffer is too small.")
		return
	}
	atomic.AddUint64(&s.statBytesOut, s.HeaderBytes)
	copy(buf, s.header)
	return
}

// Copy the next data frame into the given buffer and update the
// nextFrame pointer.
//
// Return the number of frames skipped due to buffer underrun. If the
// data source is exhausted, return with err == io.EOF (with frame
// untouched and other return values undefined).
func (s *Source) Next(nextFrame *uint64, frame *DataFrame) (nSkipped uint64, err error) {
	// We avoid doing more locking than absolutely necessary here,
	// which causes some edge cases: it's possible for s.nextFrame
	// to advance and even lap *nextFrame while we're deciding
	// which frame to grab. However, since s.nextFrame never moves
	// backward, the two worst cases are: [1] we grab a frame
	// after computing nSkipped and then being lapped again, which
	// means nSkipped underestimates the distance between the
	// requested and actual frame (but this will be made up next
	// time around), and [2] we grab a frame after s.nextFrame has
	// advanced _to_ that frame, which means readNextFrame() will
	// block while we copy the frame (but that's no worse than
	// blocking _every_ readNextFrame, which would happen if we
	// held a lock on s.nextFrame here).
	if s.nextFrame > uint64(0) && *nextFrame == uint64(0) {
		// New clients start out reading fresh frames.
		*nextFrame = s.nextFrame - uint64(1)
	} else if s.nextFrame >= *nextFrame+uint64(cap(s.frames)) {
		// s.nextFrame has lapped *nextFrame. Catch up.
		delta := s.nextFrame - *nextFrame - uint64(1)
		nSkipped += delta
		*nextFrame += delta
	} else if *nextFrame >= s.nextFrame {
		// Client has caught up to source. Includes "both are at zero" case.
		s.Cond.L.Lock()
		for *nextFrame >= s.nextFrame && !s.gone {
			s.Cond.Wait()
		}
		s.Cond.L.Unlock()
		if *nextFrame >= s.nextFrame {
			// source is gone _and_ there are no more full frames in the buffer.
			err = io.EOF
			return
		}
	}
	bufPos := *nextFrame % uint64(cap(s.frames))
	s.frameLocks[bufPos].RLock()
	defer s.frameLocks[bufPos].RUnlock()
	if cap(*frame) < len(s.frames[bufPos]) {
		err = errors.New("Caller's frame buffer is too small.")
	}
	*frame = (*frame)[:len(s.frames[bufPos])]
	copy(*frame, s.frames[bufPos])
	atomic.AddUint64(&s.statBytesOut, uint64(len(s.frames[bufPos])))
	*nextFrame++
	return
}

func (s *Source) Done() {
	s.sinkCount--
	if s.closeIdle {
		s.CloseIfIdle()
	}
}

// Make sure everyone waiting in Next() gives up. Prevents deadlock.
func (s *Source) disconnectAll() {
	s.gone = true
	s.Broadcast()
}

func (s *Source) Close() {
	s.closeIdle = true
	s.disconnectAll()
}

func (s *Source) CloseIfIdle() {
	didClose := false
	s.sourceMap.mutex.Lock()
	if s.sinkCount == 0 {
		delete(s.sourceMap.sources, s.path)
		didClose = true
	}
	s.sourceMap.mutex.Unlock()
	if didClose {
		s.disconnectAll()
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

func (sm *SourceMap) Close() {
	for _, src := range sm.sources {
		src.Close()
	}
}

// GetSource returns a Source for the given path (URI) and config
// (argv). At any given time, there is at most one Source for a given
// path.
//
// The caller must ensure Done() is eventually called, exactly once,
// on the returned *Source.
func (sm *SourceMap) GetSource(path string, c *Config) (src *Source) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	var ok bool
	if src, ok = sm.sources[path]; !ok {
		src = NewSource(path, c, sm)
		sm.sources[path] = src
		go src.run()
	}
	src.sinkCount++
	return src
}
