package main

import (
	"errors"
)

type FilterFunc func(buf []byte, contextIn interface{}) (frameSize int, nextContext interface{}, err error)

var InvalidFrame = errors.New("Not a valid frame")
var ShortFrame = errors.New("Short frame")

var Filters = map[string]FilterFunc{
	"": RawFilter,
}

// RawFilter passes a frame IFF it fills the frame buffer capacity.
func RawFilter(frame []byte, _ interface{}) (frameSize int, _ interface{}, err error) {
	frameSize = cap(frame)
	if len(frame) < frameSize {
		err = ShortFrame
	}
	return
}
