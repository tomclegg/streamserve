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
	frames          []DataFrame
	frameBytes      uint64
	gone            bool
	input           io.ReadCloser
	nextFrame       uint64 // How many frames have ever been here
	path            string
	sync.RWMutex    // Must be held while changing nextFrame or gone
	*sync.Cond      // Control access to frames other than nextFrame
	statBytesIn     uint64
	statBytesOut    uint64
	statLogInterval time.Duration
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
	s.statLogInterval = time.Duration(c.StatLogInterval * 1000000000)
	return
}

// Read data from the given source into the buffer. If the source
// reaches EOF and cannot be reopened, remove the source from
// sourceMap and return.
func (s *Source) run() {
	var err error
	lastLog := time.Now()
	defer func() {
		if err != nil {
			log.Printf("Source %s error: %s", s.path, err)
		}
		s.Close()
	}()
	if s.input, err = os.Open(s.path); err != nil {
		return
	}
	defer s.input.Close()
	for {
		bufPos := s.nextFrame % uint64(cap(s.frames))
		for framePos := uint64(0); framePos < s.frameBytes; {
			var got int
			if got, err = s.input.Read(s.frames[bufPos][framePos:]); err != nil {
				return
			}
			framePos += uint64(got)
		}
		s.Lock()
		s.statBytesIn += s.frameBytes
		s.nextFrame += 1
		if s.statLogInterval > 0 && time.Since(lastLog) >= s.statLogInterval {
			log.Printf("Stats: %d in %d out", s.statBytesIn, s.statBytesOut)
			lastLog = time.Now()
		}
		s.Unlock()
		s.Cond.Broadcast()
		runtime.Gosched()
	}
	log.Printf("Input channel closed for %s", s.path)
	// TODO: Reopen
}

// Copy the next data frame into the given buffer and update the
// nextFrame pointer.
//
// Return the number of frames skipped due to buffer underrun. If the
// data source is exhausted, return with err != nil (with frame
// untouched and other return values undefined).
func (s *Source) Next(nextFrame *uint64, frame DataFrame) (nSkipped uint64, err error) {
	s.Cond.L.Lock()
	for *nextFrame >= s.nextFrame && !s.gone {
		// If we don't Unlock and GoSched here, performance goes awful.
		s.Cond.L.Unlock()
		runtime.Gosched()
		s.Cond.L.Lock()
		// Theoretically, this should be enough:
		s.Cond.Wait()
	}
	if s.gone {
		err = errors.New("Read past end of stream")
		s.Cond.L.Unlock()
		return
	}
	lag := s.nextFrame - *nextFrame
	if lag >= uint64(cap(s.frames)-1) {
		*nextFrame = s.nextFrame - 1
		nSkipped = lag
	}
	copy(frame[:], s.frames[*nextFrame%uint64(cap(s.frames))][:])
	s.Cond.L.Unlock()
	s.Lock()
	s.statBytesOut += s.frameBytes
	s.Unlock()
	*nextFrame += 1
	runtime.Gosched()
	return
}

// Remove the source from sourceMap.
func (s *Source) Close() {
	sourceMapMutex.Lock()
	delete(sourceMap, s.path)
	sourceMapMutex.Unlock()
	s.RWMutex.Lock()
	s.gone = true
	s.RWMutex.Unlock()
	s.Broadcast()
}

// Return a Source for the given path (URI) and config (argv). At any
// given time, there is at most one Source for a given path.
func GetSource(path string, c Config) *Source {
	sourceMapMutex.RLock()
	if src, ok := sourceMap[path]; ok {
		sourceMapMutex.RUnlock()
		return src
	}
	sourceMapMutex.RUnlock()
	sourceMapMutex.Lock()
	defer sourceMapMutex.Unlock()
	if src, ok := sourceMap[path]; ok {
		return src
	}
	src := NewSource(path, c)
	sourceMap[path] = src
	go src.run()
	return src
}
