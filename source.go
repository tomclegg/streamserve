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
	sinkCount       int
	frames          []DataFrame
	frameLocks      []sync.RWMutex
	frameBytes      uint64
	gone            bool
	header          []byte
	HeaderBytes     uint64
	input           io.ReadCloser
	nextFrame       uint64 // How many frames have ever been here
	path            string
	closeIdle       bool
	reopen          bool
	bandwidth       uint64
	sync.RWMutex    // Must be held while changing nextFrame or gone
	*sync.Cond      // Control access to frames other than nextFrame
	statBytesIn     uint64
	statBytesOut    uint64
	statLast        time.Time
	statLogInterval time.Duration
}

var sourceMap = make(map[string]*Source)
var sourceMapMutex = sync.RWMutex{}

func NewSource(path string, c *Config) (s *Source) {
	s = &Source{}
	s.Cond = sync.NewCond(s.RLocker())
	s.frameLocks = make([]sync.RWMutex, c.SourceBuffer)
	s.frames = make([]DataFrame, c.SourceBuffer)
	for i := range s.frames {
		s.frames[i] = make(DataFrame, c.FrameBytes)
	}
	s.bandwidth = c.SourceBandwidth
	s.closeIdle = c.CloseIdle
	s.frameBytes = c.FrameBytes
	s.HeaderBytes = c.HeaderBytes
	s.path = path
	s.reopen = c.Reopen
	s.statLogInterval = c.StatLogInterval
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
	for framePos := uint64(0); framePos < s.frameBytes; {
		var got int
		if got, err = s.input.Read(s.frames[bufPos][framePos:]); got <= 0 {
			return
		}
		framePos += uint64(got)
	}
	return
}

// Read data from the given source into the buffer. If the source
// reaches EOF and cannot be reopened, remove the source from
// sourceMap and return.
func (s *Source) run() {
	var err error
	s.statLast = time.Now()
	defer s.LogStats(true)
	defer s.Close()
	if err := s.openInput(); err != nil {
		return
	}
	var ticker *time.Ticker
	if s.bandwidth > 0 {
		ticker = time.NewTicker(time.Duration(uint64(1000000000) * s.frameBytes / s.bandwidth))
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
		s.nextFrame += 1
		s.Cond.Broadcast()
		s.statBytesIn += s.frameBytes
		s.LogStats(false)
		if ticker != nil {
			<-ticker.C
		}
	}
	if s.input != nil {
		s.input.Close()
	}
}

// If !really, only if statLogInterval says so.
func (s *Source) LogStats(really bool) {
	if really || (s.statLogInterval > 0 && time.Since(s.statLast) >= s.statLogInterval) {
		s.statLast.Add(s.statLogInterval)
		log.Printf("source %s stats: %d in %d out", s.path, s.statBytesIn, s.statBytesOut)
	}
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
// data source is exhausted, return with err != nil (with frame
// untouched and other return values undefined).
func (s *Source) Next(nextFrame *uint64, frame DataFrame) (nSkipped uint64, err error) {
	defer func() {
		if err == nil {
			atomic.AddUint64(&s.statBytesOut, s.frameBytes)
			*nextFrame += 1
		}
	}()
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
			err = io.EOF
			return
		}
	}
	bufPos := *nextFrame % uint64(cap(s.frames))
	s.frameLocks[bufPos].RLock()
	defer s.frameLocks[bufPos].RUnlock()
	copy(frame, s.frames[bufPos])
	if cap(frame) < len(s.frames[bufPos]) {
		err = errors.New("Caller's frame buffer is too small.")
	}
	return
}

func (s *Source) Done() {
	s.sinkCount -= 1
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
	sourceMapMutex.Lock()
	if s.sinkCount == 0 {
		delete(sourceMap, s.path)
		didClose = true
	}
	sourceMapMutex.Unlock()
	if didClose {
		s.disconnectAll()
	}
}

func CloseAllSources() {
	for _, src := range sourceMap {
		src.Close()
	}
}

// Return a Source for the given path (URI) and config (argv). At any
// given time, there is at most one Source for a given path.
//
// The caller must ensure Done() is eventually called, exactly once,
// on the returned *Source.
func GetSource(path string, c *Config) (src *Source) {
	sourceMapMutex.Lock()
	defer sourceMapMutex.Unlock()
	var ok bool
	if src, ok = sourceMap[path]; !ok {
		src = NewSource(path, c)
		sourceMap[path] = src
		go src.run()
	}
	src.sinkCount += 1
	return src
}
