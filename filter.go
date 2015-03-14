package main

import (
	"errors"
)

type FilterFunc func(buf []byte, context *interface{}) (frameSize int, err error)

var InvalidFrame = errors.New("Not a valid frame")
var ShortFrame = errors.New("Short frame")

var Filters = map[string]FilterFunc{
	"": RawFilter,
}

// RawFilter passes a frame IFF it fills the frame buffer capacity.
func RawFilter(frame []byte, context *interface{}) (frameSize int, err error) {
	frameSize = cap(frame)
	if len(frame) < frameSize {
		err = ShortFrame
	}
	return
}
