package main

import (
	"fmt"
	"hash/crc64"
	"sync"
	"testing"
)

func TestConfigCheck(t *testing.T) {
	ok := Config{
		SourceBuffer: 3,
		FrameBytes:   1,
		Path:         "/dev/stdin",
		Listen:       "0.0.0.0:80",
	}
	var c Config
	if c, c.SourceBuffer = ok, 0; c.Check() == nil {
		t.Error("SourceBuffer 0 accepted")
	}
	if c, c.SourceBuffer = ok, 2; c.Check() == nil {
		t.Error("SourceBuffer 2 accepted")
	}
	if c, c.FrameBytes = ok, 0; c.Check() == nil {
		t.Error("FrameBytes 0 accepted")
	}
	if c, c.Path = ok, ""; c.Check() == nil {
		t.Error("Path empty accepted")
	}
	if c = ok; c.Check() != nil {
		t.Error("Valid config not accepted")
	}
}

func TestContentEqual(t *testing.T) {
	source := GetSource("/dev/urandom", Config{
		SourceBuffer: 32,
		FrameBytes:   65536,
	})
	nConsumers := 10
	hashes := make([]chan uint64, nConsumers)
	tab := crc64.MakeTable(crc64.ECMA)
	for i := 0; i < nConsumers; i++ {
		hashes[i] = make(chan uint64)
		go func(done chan<- uint64, whoami int) {
			var anySkips bool
			var frame DataFrame
			var nextFrame uint64
			var hash uint64
			for f := 0; f < 130; f++ {
				var skips uint64
				var err error
				if skips, err = source.Next(&nextFrame, frame); err != nil {
					t.Fatalf("source.Next(%d, frame): %s", nextFrame, err)
				}
				if skips > 0 {
					anySkips = true
				}
				hash = crc64.Update(hash, tab, frame)
			}
			if anySkips {
				hash = 0
			}
			done <- hash
		}(hashes[i], i)
	}
	h0 := <-hashes[0]
	for i := 1; i < nConsumers; i++ {
		hi := <-hashes[i]
		if h0 == 0 {
			h0 = hi
		} else if hi != h0 {
			t.Errorf("hash mismatch: h0=%x, h%d=%x", h0, i, hi)
		}
	}
	source.Close()
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
	source := GetSource("/dev/zero", c)
	wg := &sync.WaitGroup{}
	wg.Add(nConsumers)
	for c := 0; c < nConsumers; c++ {
		go func(c int) {
			consume(b, source, c)
			wg.Done()
		}(c)
	}
	wg.Wait()
	source.Close()
}

func consume(b *testing.B, source *Source, label interface{}) {
	var frame DataFrame
	var nextFrame uint64
	for i := uint64(0); i < 10*uint64(b.N); i++ {
		if _, err := source.Next(&nextFrame, frame); err != nil {
			b.Fatalf("source.Next(%d, frame): %s", nextFrame, err)
		}
	}
}

func ExampleStub() {
	fmt.Println("Hello")
	// Output:
	// Hello
}
