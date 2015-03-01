package main

import (
	"io"
	"log"
	"time"
)

type Sink struct {
	io.Writer
	label    string
	maxBytes uint64
	source   *Source
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
	if sink.source.HeaderBytes > uint64(0) {
		header := make([]byte, sink.source.HeaderBytes)
		if err = sink.source.GetHeader(header); err != nil {
			if err != io.EOF {
				log.Printf("client %s header error %s", sink.label, err)
			}
			return
		}
		sink.Write(header)
		statBytes += uint64(len(header))
	}
	var frame = make(DataFrame, sink.source.frameBytes)
	var nextFrame uint64
	for {
		var nSkip uint64
		if nSkip, err = sink.source.Next(&nextFrame, &frame); err != nil {
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
		statFrames++
		if sink.maxBytes > 0 && statBytes >= sink.maxBytes {
			return
		}
	}
}

// Return a new sink for the given source.
func NewSink(label string, dst io.Writer, source *Source, c *Config) *Sink {
	return &Sink{
		label:    label,
		maxBytes: c.ClientMaxBytes,
		source:   source,
		Writer:   dst,
	}
}
