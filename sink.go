package main

import (
	"io"
	"log"
	"time"
)

type Sink struct {
	io.WriteCloser
	label  string
	source *Source
}

func (sink *Sink) run() (err error) {
	var startTime = time.Now()
	var statBytes uint64
	var statFrames uint64
	var statSkips uint64
	defer func() {
		log.Printf("Sink %s ends: %s elapsed %d bytes %d frames %d skipframes", sink.label, time.Since(startTime).String(), statBytes, statFrames, statSkips)
		if err := sink.Close(); err != nil {
			log.Printf("Sink %s error: close: %s", sink.label, err)
		}

	}()
	var frame DataFrame
	var nextFrame uint64
	for {
		var nSkip uint64
		if nSkip, err = sink.source.Next(&nextFrame, frame); err != nil {
			log.Printf("Sink %s error: source: %s", sink.label, err)
			return
		}
		if nSkip > 0 {
			log.Printf("Sink %s skipped %d frames", sink.label, nSkip)
			statSkips += nSkip
		}
		if _, err = sink.Write(frame); err != nil {
			log.Printf("Sink %s error: write: %s", sink.label, err)
			return
		}
		statBytes += uint64(len(frame))
		statFrames += 1
	}
}

// Return a new sink for the given source.
func NewSink(label string, dst io.WriteCloser, path string, c Config) *Sink {
	sink := Sink{label: label, source: GetSource(path, c), WriteCloser: dst}
	go sink.run()
	return &sink
}
