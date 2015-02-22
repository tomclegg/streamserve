package main

import (
	"io"
	"log"
	"time"
)

type Sink struct {
	io.Writer
	label  string
	source *Source
}

func (sink *Sink) Run() (err error) {
	defer sink.source.Done()
	var startTime = time.Now()
	var statBytes uint64
	var statFrames uint64
	var statSkips uint64
	log.Printf("client %s start", sink.label)
	defer func() {
		log.Printf("client %s end with %s elapsed %d bytes %d frames %d skipframes", sink.label, time.Since(startTime).String(), statBytes, statFrames, statSkips)
	}()
	var frame DataFrame = make(DataFrame, sink.source.frameBytes)
	var nextFrame uint64
	for {
		var nSkip uint64
		if nSkip, err = sink.source.Next(&nextFrame, frame); err != nil {
			return
		}
		if nSkip > 0 {
			log.Printf("client %s skip %d frames", sink.label, nSkip)
			statSkips += nSkip
		}
		if _, err = sink.Write(frame); err != nil {
			return
		}
		statBytes += uint64(len(frame))
		statFrames += 1
	}
}

// Return a new sink for the given source.
func NewSink(label string, dst io.Writer, source *Source) *Sink {
	return &Sink{label: label, source: source, Writer: dst}
}
