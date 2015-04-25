package main

import (
	"errors"
)

// FilterFunc indicates whether the given buf starts with a valid
// frame. If so, it returns the size of the valid frame and err==nil.
//
// If not, it returns a non-nil error.
//
// The nextContext returned by a FilterFunc is passed to the same
// FilterFunc as contextIn next time the FilterFunc is called to
// filter a subsequent segment of the same data stream.
type FilterFunc func(buf []byte, contextIn interface{}) (frameSize int, nextContext interface{}, err error)

// ErrInvalidFrame indicates it is impossible for the supplied buf to
// be a prefix of any valid frame.
var ErrInvalidFrame = errors.New("Not a valid frame")

// ErrShortFrame indicates it is possible that the supplied buf is a
// prefix of a valid frame, but more data must be supplied in order to
// identify such a frame.
//
// It does not guarantee that the next valid frame will be longer
// than the supplied buf, or begin with buf.
var ErrShortFrame = errors.New("Short frame")

// Filters is a map of named FilterFuncs which can be selected by
// callers.
var Filters = map[string]FilterFunc{
	"": RawFilter,
}

// RawFilter passes a frame IFF it fills the frame buffer capacity.
func RawFilter(frame []byte, _ interface{}) (frameSize int, _ interface{}, err error) {
	frameSize = cap(frame)
	if len(frame) < frameSize {
		err = ErrShortFrame
	}
	return
}
