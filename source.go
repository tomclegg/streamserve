package main

import (
	"errors"
	"io"
	"log"
	"os"
	"runtime"
	"sync"
	"time"
)

type Source struct {
	sinkCount       int
	frames          []DataFrame
	frameBytes      uint64
	gone            bool
	input           io.ReadCloser
	nextFrame       uint64 // How many frames have ever been here
	path            string
	closeIdle       bool
	sync.RWMutex    // Must be held while changing nextFrame or gone
	*sync.Cond      // Control access to frames other than nextFrame
	statBytesIn     uint64
	statBytesOut    uint64
	statLast        time.Time
	statLogInterval time.Duration
	statLock        sync.Mutex
}

var sourceMap = make(map[string]*Source)
var sourceMapMutex = sync.RWMutex{}

func NewSource(path string, c Config) (s *Source) {
	s = &Source{}
	s.Cond = sync.NewCond(s.RLocker())
	s.frames = make([]DataFrame, c.SourceBuffer)
	for i := range s.frames {
		s.frames[i] = make(DataFrame, c.FrameBytes)
	}
	s.frameBytes = c.FrameBytes
	s.path = path
	s.statLogInterval = c.StatLogInterval
	s.closeIdle = c.CloseIdle
	return
}

// Read data from the given source into the buffer. If the source
// reaches EOF and cannot be reopened, remove the source from
// sourceMap and return.
func (s *Source) run() {
	var err error
	s.statLast = time.Now()
	if s.input, err = os.Open(s.path); err != nil {
		log.Printf("Source %s error: %s", s.path, err)
		return
	}
	defer s.input.Close()
	defer s.LogStats(true)
	for !s.gone {
		bufPos := s.nextFrame % uint64(cap(s.frames))
		for framePos := uint64(0); framePos < s.frameBytes; {
			var got int
			if got, err = s.input.Read(s.frames[bufPos][framePos:]); err != nil {
				log.Printf("Source %s error: %s", s.path, err)
				// TODO: Reopen
				s.Close()
				return
			}
			framePos += uint64(got)
		}
		s.Lock()
		s.nextFrame += 1
		s.Unlock()
		s.statLock.Lock()
		s.statBytesIn += s.frameBytes
		s.statLock.Unlock()
		s.Cond.Broadcast()
		runtime.Gosched()
		s.LogStats(false)
	}
}

// If !really, only if statLogInterval says so.
func (s *Source) LogStats(really bool) {
	s.statLock.Lock()
	defer s.statLock.Unlock()
	if really || (s.statLogInterval > 0 && time.Since(s.statLast) >= s.statLogInterval) {
		log.Printf("Stats: %d in %d out", s.statBytesIn, s.statBytesOut)
		s.statLast = time.Now()
	}
}

// Copy the next data frame into the given buffer and update the
// nextFrame pointer.
//
// Return the number of frames skipped due to buffer underrun. If the
// data source is exhausted, return with err != nil (with frame
// untouched and other return values undefined).
func (s *Source) Next(nextFrame *uint64, frame DataFrame) (nSkipped uint64, err error) {
	s.Cond.L.Lock()
	defer func() {
		s.Cond.L.Unlock()
		if err == nil {
			s.statLock.Lock()
			s.statBytesOut += s.frameBytes
			s.statLock.Unlock()
			*nextFrame += 1
			runtime.Gosched()
		}
	}()
	for *nextFrame >= s.nextFrame && !s.gone {
		// If we don't Unlock and GoSched here, performance goes awful.
		s.Cond.L.Unlock()
		runtime.Gosched()
		s.Cond.L.Lock()
		// Theoretically, this should be enough:
		s.Cond.Wait()
	}
	if *nextFrame >= s.nextFrame {
		err = io.EOF
		return
	}
	lag := s.nextFrame - *nextFrame
	if lag >= uint64(cap(s.frames)-1) {
		*nextFrame = s.nextFrame - 1
		nSkipped = lag
	}
	bufPos := *nextFrame % uint64(cap(s.frames))
	if cap(frame) < len(s.frames[bufPos]) {
		err = errors.New("Caller's frame buffer is too small.")
		return
	}
	copy(frame, s.frames[bufPos])
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
func GetSource(path string, c Config) (src *Source) {
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
