package main

import (
	"hash/crc64"
	"io"
	"runtime"
	"sync"
	"testing"
)

func TestContentEqual(t *testing.T) {
	source := GetSource("/dev/urandom", &Config{
		SourceBuffer: 32,
		FrameBytes:   65536,
	})
	nConsumers := 10
	done := make(chan uint64)
	tab := crc64.MakeTable(crc64.ECMA)
	for i := 0; i < nConsumers; i++ {
		go func() {
			var hash uint64
			defer func() { done <- hash }()
			var frame = make(DataFrame, source.frameBytes)
			var nextFrame uint64
			for f := 0; f < 130; f++ {
				var err error
				if _, err = source.Next(&nextFrame, frame); err != nil && err != io.EOF {
					t.Fatalf("source.Next(%d, frame): %s", nextFrame, err)
				}
				hash = crc64.Update(hash, tab, frame)
			}
		}()
	}
	h0 := <-done
	for i := 1; i < nConsumers; i++ {
		hi := <-done
		if h0 == 0 {
			h0 = hi
		} else if hi != h0 {
			t.Errorf("hash mismatch: h0=%x, h%d=%x", h0, i, hi)
		}
	}
	CloseAllSources()
}

// 32s of CD audio, 1s per frame, 1000 consumers
func BenchmarkSource1KConsumers(b *testing.B) {
	benchSource(b, 1000, Config{
		SourceBuffer: 32,
		FrameBytes:   176800,
	})
}

// 320s of CD audio, 1s per frame, 10000 consumers
func BenchmarkSource10KConsumers(b *testing.B) {
	benchSource(b, 10000, Config{
		SourceBuffer: 320,
		FrameBytes:   176800,
	})
}

// 32s of 128kbps audio, 1s per frame, 10000 consumers
func BenchmarkSource128kbps10KConsumers(b *testing.B) {
	benchSource(b, 10000, Config{
		SourceBuffer: 32,
		FrameBytes:   128 * 1000 / 8,
	})
}

func BenchmarkSourceTinyBuffer(b *testing.B) {
	benchSource(b, 1, Config{
		SourceBuffer: 3,
		FrameBytes:   1,
	})
}

func BenchmarkSourceMediumBuffer(b *testing.B) {
	benchSource(b, 1, Config{
		SourceBuffer: 64,
		FrameBytes:   64,
	})
}

func BenchmarkSourceBigFrame(b *testing.B) {
	benchSource(b, 1, Config{
		SourceBuffer: 64,
		FrameBytes:   1048576,
	})
}

func BenchmarkSourceBigBuffer(b *testing.B) {
	benchSource(b, 1, Config{
		SourceBuffer: 1048576,
		FrameBytes:   64,
	})
}

func benchSource(b *testing.B, nConsumers int, c Config) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(runtime.NumCPU()))
	source := GetSource("/dev/zero", &c)
	wg := &sync.WaitGroup{}
	wg.Add(nConsumers)
	for c := 0; c < nConsumers; c++ {
		go func(c int) {
			defer wg.Done()
			consume(b, source, c)
		}(c)
	}
	wg.Wait()
	CloseAllSources()
}

func consume(b *testing.B, source *Source, label interface{}) {
	var frame DataFrame = make(DataFrame, source.frameBytes)
	var nextFrame uint64
	for i := uint64(0); i < 10*uint64(b.N); i++ {
		if _, err := source.Next(&nextFrame, frame); err != nil && err != io.EOF {
			b.Fatalf("source.Next(%d, frame): %s", nextFrame, err)
		}
	}
}
