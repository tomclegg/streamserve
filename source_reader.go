package main

import (
	"errors"
	"io"
	"sync/atomic"
)

// SourceReader reads data from a Source. Every Read() call either
// reads exactly one complete frame, or returns an error.
type SourceReader struct {
	source     *Source
	didHeader  bool
	nextFrame  uint64
	FramesRead uint64
	// A frame is "skipped" if an earlier frame and a later frame
	// have been returned by a Read() call, but the frame itself
	// was never returned by Read().
	FramesSkipped uint64
	BytesRead     uint64
}

// ErrBufferTooSmall is returned if Read is called with a buffer
// smaller than the source's next frame.
var ErrBufferTooSmall = errors.New("caller's buffer is too small")

func (sr *SourceReader) Read(buf []byte) (int, error) {
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
	s := sr.source
	if !sr.didHeader {
		sr.didHeader = true
		if sr.source.HeaderBytes > 0 {
			return sr.source.GetHeader(buf)
		}
	}
	if s.clientMaxBytes > 0 && sr.BytesRead >= s.clientMaxBytes {
		return 0, io.EOF
	}
	if s.nextFrame > uint64(0) && sr.nextFrame == uint64(0) {
		// New clients start out reading fresh frames.
		sr.nextFrame = s.nextFrame - uint64(1)
	} else if s.nextFrame >= sr.nextFrame+uint64(cap(s.frames)) {
		// s.nextFrame has lapped sr.nextFrame. Catch up.
		delta := s.nextFrame - sr.nextFrame - uint64(1)
		sr.FramesSkipped += delta
		sr.nextFrame += delta
	} else if sr.nextFrame >= s.nextFrame {
		// Client has caught up to source. Includes "both are at zero" case.
		s.Cond.L.Lock()
		for sr.nextFrame >= s.nextFrame && !s.gone {
			s.Cond.Wait()
		}
		s.Cond.L.Unlock()
		if sr.nextFrame >= s.nextFrame {
			// source is gone _and_ there are no more full frames in the buffer.
			return 0, io.EOF
		}
	}
	bufPos := sr.nextFrame % uint64(cap(s.frames))
	s.frameLocks[bufPos].RLock()
	defer s.frameLocks[bufPos].RUnlock()
	frameSize := len(s.frames[bufPos])
	if len(buf) < frameSize {
		return 0, ErrBufferTooSmall
	}
	copy(buf, s.frames[bufPos])
	atomic.AddUint64(&s.statBytesOut, uint64(frameSize))
	sr.nextFrame++
	sr.FramesRead++
	sr.BytesRead += uint64(frameSize)
	return len(s.frames[bufPos]), nil
}

// Close disconnects the reader from the source. Unclosed
// SourceReaders can cause Sources to stay open needlessly.
func (sr *SourceReader) Close() {
	sr.source.Done()
}
