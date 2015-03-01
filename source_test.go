package main

import (
	"bytes"
	"fmt"
	"hash/crc64"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestSigpipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("head", "-c64", "/dev/urandom")
	cmd.Stdout = w
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	w.Close()
	go cmd.Wait()
	source := GetSource(fmt.Sprintf("/dev/fd/%d", r.Fd()), &Config{
		SourceBuffer: 5,
		FrameBytes:   16,
		CloseIdle:    false,
		Reopen:       true,
	})
	var frame = make(DataFrame, source.frameBytes)
	var nextFrame uint64
	for f := 0; f < 4; f++ {
		if _, err = source.Next(&nextFrame, &frame); err != nil {
			t.Error(err)
			break
		}
	}
	done := make(chan error, 1)
	go func() {
		_, err = source.Next(&nextFrame, &frame)
		done <- err
	}()
	select {
	case <-time.After(time.Second):
		t.Error("Should have given up within 1s of SIGCHLD")
	case err = <-done:
		if err == nil {
			t.Errorf("Should have error, got frame %v", frame)
		}
	}
	r.Close()
	source.Done()
	CloseAllSources()
}

func TestEmptySource(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	closeNow := make(chan bool)
	go func() {
		<-closeNow
		w.Close()
	}()
	source := GetSource(fmt.Sprintf("/dev/fd/%d", r.Fd()), &Config{
		SourceBuffer: 5,
		FrameBytes:   16,
		CloseIdle:    false,
		Reopen:       false,
	})
	var frame = make(DataFrame, source.frameBytes)
	var nextFrame uint64
	done := make(chan error, 1)
	go func() {
		_, err := source.Next(&nextFrame, &frame)
		done <- err
	}()
	time.Sleep(time.Millisecond)
	select {
	case err := <-done:
		t.Errorf("Should still be waiting for input, got error %v", err)
	default:
	}
	closeNow <- true
	select {
	case <-time.After(100 * time.Millisecond):
		t.Error("Should have given up within 100ms")
	case err := <-done:
		if err == nil {
			t.Errorf("Should have error, got frame %v", frame)
		}
	}
	source.Done()
	r.Close()
	CloseAllSources()
}

func failUnless(t *testing.T, ms time.Duration, ok <-chan bool) {
	select {
	case <-time.After(ms * time.Millisecond):
		t.Fatalf("Timed out in %d ms", ms)
	case <-ok:
	}
}

func DataFaker(t *testing.T) (string, chan<- interface{}, *[]byte) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var sentData []byte
	wantMore := make(chan interface{})
	go func() {
		for cmd := range wantMore {
			var moreData []byte
			var n int
			if n, ok := cmd.(int); ok {
				moreData = make([]byte, n)
			} else {
				moreData = cmd.([]byte)
			}
			for i := 0; i < n; i++ {
				moreData[i] = byte(rand.Int() & 0xff)
			}
			sentData = append(sentData, moreData...)
			if did, err := w.Write(moreData); did < len(moreData) || err != nil {
				t.Error("Short write: %d < %d, %s", did, len(moreData), err)
			}
			w.Sync()
		}
		r.Close()
		w.Close()
	}()
	return fmt.Sprintf("/dev/fd/%d", r.Fd()), wantMore, &sentData
}

func TestSourceFilter(t *testing.T) {
	type expect struct {
		frame     []byte
		frameSize int
		err       error
	}
	fMock := make(chan *expect, 100)
	var pending *expect
	Filters["MOCK"] = func(frame []byte, context *interface{}) (frameSize int, err error) {
		if pending == nil {
			pending = <-fMock
		}
		if len(frame) < len(pending.frame) {
			return len(pending.frame), ShortFrame
		}
		if 0 != bytes.Compare(pending.frame, frame[0:len(pending.frame)]) {
			t.Fatalf("Expected filter(%v), got %v", pending.frame, frame)
		}
		defer func() { pending = nil }()
		return pending.frameSize, pending.err
	}
	defer func() { delete(Filters, "MOCK") }()
	fakeFile, sendFake, _ := DataFaker(t)
	source := GetSource(fakeFile, &Config{
		SourceBuffer: 5,
		FrameBytes:   4,
		CloseIdle:    true,
		Reopen:       false,
		FrameFilter:  "MOCK",
	})
	var nextFrame uint64
	var frame = make(DataFrame, 4)
	expectNext := func(expect []byte, expectErr error) {
		_, err := source.Next(&nextFrame, &frame)
		if err != expectErr {
			t.Errorf("Frame %d expected err %s got %s", nextFrame, expectErr, err)
		} else if err == nil && 0 != bytes.Compare(expect, frame) {
			t.Errorf("Frame %d expected bytes %v got %v", nextFrame, expect, frame)
		}
	}
	// Our mock filter will pass any frame without a 00.
	sendFake <- []byte{11, 22, 33, 44, 11, 22, 33, 00, 11, 00, 33, 44, 11, 22, 33, 44}
	fMock <- &expect{[]byte{11, 22, 33, 44}, 4, nil}
	expectNext([]byte{11, 22, 33, 44}, nil)
	fMock <- &expect{[]byte{11, 22, 33, 00}, 3, nil}
	expectNext([]byte{11, 22, 33}, nil)
	fMock <- &expect{[]byte{00, 11, 00, 33}, 0, InvalidFrame}
	fMock <- &expect{[]byte{11, 00, 33, 44}, 1, nil}
	expectNext([]byte{11}, nil)
	fMock <- &expect{[]byte{00, 33, 44, 11}, 0, InvalidFrame}
	fMock <- &expect{[]byte{33, 44, 11, 22}, 4, nil}
	expectNext([]byte{33, 44, 11, 22}, nil)
	source.Done()
	// TODO: source should give filter a chance to accept the data at EOF, even though it doesn't fill max frame size.
	// fMock <- &expect{[]byte{33,44}, 1, nil}
	// fMock <- &expect{[]byte{44}, 1, nil}
	// expectNext([]byte{33}, nil)
	// expectNext([]byte{44}, nil)
	expectNext([]byte{}, io.EOF)
}

func TestSentEqualsReceived(t *testing.T) {
	fakeData, wantMore, sentData := DataFaker(t)
	source := GetSource(fakeData, &Config{
		SourceBuffer: 5,
		FrameBytes:   16,
		CloseIdle:    true,
		Reopen:       false,
	})
	var rcvdData = []byte{}
	var frame = make(DataFrame, source.frameBytes)
	var nextFrame uint64
	ok := make(chan bool)
	wantMore <- 19 // Send one full frame (to make sure we start receiving at frame 0) and a bit extra
	for f := 100; f > 0; f-- {
		go func() {
			if _, err := source.Next(&nextFrame, &frame); err != nil {
				t.Fatal(err)
			}
			if len(frame) != 16 {
				t.Errorf("Wrong size frame, want 16 got %d", len(frame))
			}
			ok <- true
		}()
		failUnless(t, 100, ok)
		rcvdData = append(rcvdData, frame...)
		if f == 4 {
			wantMore <- 16 * 3 // Last three frames (we sent one frame before the loop)
		} else if f%4 == 0 {
			wantMore <- 16 * 4 // Next four frames
		}
	}
	close(wantMore)
	go func() {
		if _, err := source.Next(&nextFrame, &frame); err != io.EOF {
			t.Error("Should have got EOF")
		}
		ok <- true
	}()
	failUnless(t, 100, ok)
	wantData := (*sentData)[0:1600]
	if 0 != bytes.Compare(wantData, rcvdData) {
		t.Errorf("want %d != rcvd %d", len(wantData), len(rcvdData))
	}
	source.Done()
	CloseAllSources()
}

func TestHeader(t *testing.T) {
	headerSize := uint64(64)
	nClients := 5
	sources := make(chan *Source, nClients)
	for i := 0; i < nClients; i++ {
		sources <- GetSource("/dev/urandom", &Config{
			SourceBuffer: 5,
			FrameBytes:   65536,
			HeaderBytes:  headerSize,
			CloseIdle:    true,
		})
	}
	empty := make([]byte, headerSize)
	var h0 []byte
	for i := 0; i < nClients; i++ {
		h := make([]byte, headerSize)
		source := <-sources
		err := source.GetHeader(h)
		if err != nil {
			t.Error(err)
		} else if h0 == nil {
			h0 = h
		} else if bytes.Compare(h, h0) != 0 {
			t.Errorf("Header mismatch: %v != %v", h0, h)
		} else if bytes.Compare(h, empty) == 0 {
			t.Error("Header appears uninitialized")
		} else if uint64(len(h)) != headerSize {
			t.Errorf("Header size mismatch: %d != %d", len(h), headerSize)
		}
		var frame = make(DataFrame, source.frameBytes)
		var nextFrame uint64
		for f := 0; f < 6; f++ {
			var err error
			if _, err = source.Next(&nextFrame, &frame); err != nil {
				if err != io.EOF {
					t.Error(err)
				}
				break
			}
		}
		source.Done()
	}
	CloseAllSources()
}

func TestContentEqual(t *testing.T) {
	source := GetSource("/dev/urandom", &Config{
		SourceBuffer: 32,
		FrameBytes:   65536,
		CloseIdle:    true,
	})
	nConsumers := 10
	done := make(chan uint64, nConsumers)
	tab := crc64.MakeTable(crc64.ECMA)
	for i := 0; i < nConsumers; i++ {
		go func() {
			var hash uint64
			defer func() { done <- hash }()
			var frame = make(DataFrame, source.frameBytes)
			var nextFrame uint64
			for f := 0; f < 130; f++ {
				var err error
				if _, err = source.Next(&nextFrame, &frame); err != nil && err != io.EOF {
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
	source.Done()
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
	var frame = make(DataFrame, source.frameBytes)
	var nextFrame uint64
	for i := uint64(0); i < 10*uint64(b.N); i++ {
		if _, err := source.Next(&nextFrame, &frame); err != nil {
			if err != io.EOF {
				b.Fatalf("source.Next(%d, frame): %s", nextFrame, err)
			}
			break
		}
	}
}
