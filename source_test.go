package main

import (
	"bytes"
	"fmt"
	"hash/crc64"
	"io"
	"io/ioutil"
	"math/rand"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestSigpipe(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(runtime.NumCPU()))
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
	defer r.Close()
	go cmd.Wait()
	sm := NewSourceMap()
	defer sm.Close()
	rdr := sm.NewReader(fmt.Sprintf("/dev/fd/%d", r.Fd()), &Config{
		SourceBuffer: 5,
		FrameBytes:   16,
		CloseIdle:    false,
		Reopen:       true,
	})
	defer rdr.Close()
	var frame = make(DataFrame, 16)
	for f := 0; f < 4; f++ {
		if _, err = rdr.Read(frame); err != nil {
			t.Error(err)
			break
		}
	}
	done := make(chan bool, 1)
	var got int
	go func() {
		got, err = rdr.Read(frame)
		done <- true
	}()
	select {
	case <-time.After(time.Second):
		t.Error("Should have given up within 1s of SIGCHLD")
	case <-done:
		if err == nil {
			t.Errorf("Should have error, got frame %v", frame[:got])
		}
	}
}

func TestEmptySource(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(runtime.NumCPU()))
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	closeNow := make(chan bool)
	go func() {
		<-closeNow
		w.Close()
	}()
	sm := NewSourceMap()
	defer sm.Close()
	rdr := sm.NewReader(fmt.Sprintf("/dev/fd/%d", r.Fd()), &Config{
		SourceBuffer: 5,
		FrameBytes:   16,
		CloseIdle:    false,
		Reopen:       false,
	})
	var frame = make(DataFrame, 16)
	done := make(chan error, 1)
	go func() {
		_, err := rdr.Read(frame)
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
	rdr.Close()
	r.Close()
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
				t.Errorf("Short write: %d < %d, %s", did, len(moreData), err)
			}
			w.Sync()
		}
		r.Close()
		w.Close()
	}()
	return fmt.Sprintf("/dev/fd/%d", r.Fd()), wantMore, &sentData
}

func TestSourceFilterContext(t *testing.T) {
	context := 1000
	Filters["MOCK"] = func(frame []byte, contextIn interface{}) (int, interface{}, error) {
		switch ctx := contextIn.(type) {
		case int:
			if context != ctx {
				t.Error("expected", context, "got", ctx)
			}
		case nil:
			context = 1000
		}
		context++
		return len(frame), context, nil
	}
	defer func() { delete(Filters, "MOCK") }()
	fakeFile, sendFake, _ := DataFaker(t)
	sm := NewSourceMap()
	defer sm.Close()
	rdr := sm.NewReader(fakeFile, &Config{
		SourceBuffer: 5,
		FrameBytes:   4,
		CloseIdle:    true,
		Reopen:       false,
		FrameFilter:  "MOCK",
	})
	go func() {
		sendFake <- make([]byte, 160)
		close(sendFake)
	}()
	ioutil.ReadAll(rdr)
	if context != 1040 {
		t.Error("context was", context)
	}
}

func TestSourceFilter(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(runtime.NumCPU()))
	type expect struct {
		frame     []byte
		frameSize int
		err       error
	}
	fMock := make(chan *expect, 100)
	var pending *expect
	Filters["MOCK"] = func(frame []byte, _ interface{}) (frameSize int, _ interface{}, err error) {
		if pending == nil {
			pending = <-fMock
		}
		if len(frame) < len(pending.frame) {
			return len(pending.frame), nil, ShortFrame
		}
		if 0 != bytes.Compare(pending.frame, frame[0:len(pending.frame)]) {
			t.Fatalf("Expected filter(%v), got %v", pending.frame, frame)
		}
		defer func() { pending = nil }()
		return pending.frameSize, nil, pending.err
	}
	defer func() { delete(Filters, "MOCK") }()
	fakeFile, sendFake, _ := DataFaker(t)
	sm := NewSourceMap()
	rdr := sm.NewReader(fakeFile, &Config{
		SourceBuffer: 5,
		FrameBytes:   4,
		CloseIdle:    true,
		Reopen:       false,
		FrameFilter:  "MOCK",
	})
	var frame = make(DataFrame, 4)
	expectNext := func(expect []byte, expectErr error) {
		n, err := rdr.Read(frame)
		if err != expectErr {
			t.Errorf("Frame %d expected err %s got %s", rdr.nextFrame, expectErr, err)
		} else if err == nil && 0 != bytes.Compare(expect, frame[:n]) {
			t.Errorf("Frame %d expected bytes %v got %v", rdr.nextFrame, expect, frame)
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
	sm.Close()
	// TODO: source should give filter a chance to accept the data at EOF, even though it doesn't fill max frame size.
	// close(sendFake)
	// fMock <- &expect{[]byte{33,44}, 1, nil}
	// fMock <- &expect{[]byte{44}, 1, nil}
	// expectNext([]byte{33}, nil)
	// expectNext([]byte{44}, nil)
	expectNext([]byte{}, io.EOF)
}

func TestSentEqualsReceived(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(runtime.NumCPU()))
	fakeData, wantMore, sentData := DataFaker(t)
	sm := NewSourceMap()
	defer sm.Close()
	rdr := sm.NewReader(fakeData, &Config{
		SourceBuffer: 5,
		FrameBytes:   16,
		CloseIdle:    true,
		Reopen:       false,
	})
	defer rdr.Close()
	var rcvdData = []byte{}
	var frame = make(DataFrame, 16)
	ok := make(chan bool)
	wantMore <- 19 // Send one full frame (to make sure we start receiving at frame 0) and a bit extra
	for f := 100; f > 0; f-- {
		go func() {
			if _, err := rdr.Read(frame); err != nil {
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
		if _, err := rdr.Read(frame); err != io.EOF {
			t.Error("Should have got EOF")
		}
		ok <- true
	}()
	failUnless(t, 100, ok)
	wantData := (*sentData)[0:1600]
	if 0 != bytes.Compare(wantData, rcvdData) {
		t.Errorf("want %d != rcvd %d", len(wantData), len(rcvdData))
	}
}

func TestBandwidthVariableFrameSize(t *testing.T) {
	frameSizes := []int{47, 128, 1024, 2048, 8192} // arbitrary, with some small and some big
	var fsIndex int
	Filters["MOCK"] = func(frame []byte, _ interface{}) (want int, _ interface{}, err error) {
		want = frameSizes[fsIndex]
		if len(frame) < want {
			err = ShortFrame
		} else {
			fsIndex = (fsIndex + 1) % len(frameSizes)
		}
		return
	}
	defer func() { delete(Filters, "MOCK") }()
	doBandwidthTests(t, &Config{
		SourceBuffer:    16,
		FrameBytes:      8192,
		CloseIdle:       true,
		Reopen:          false,
		FrameFilter:     "MOCK",
	})
}

func TestBandwidth(t *testing.T) {
	doBandwidthTests(t, &Config{
		SourceBuffer:    16,
		FrameBytes:      8192,
		CloseIdle:       true,
		Reopen:          false,
	})
}

func doBandwidthTests(t *testing.T, config *Config) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(runtime.NumCPU()))
	for _, bw := range []uint64{100000, 1000000} {
		config.SourceBandwidth = bw
		sm := NewSourceMap()
		rdr := sm.NewReader("/dev/urandom", config)
		var frame = make(DataFrame, 16384)
		var got, gotFrames uint64
		done := time.After(time.Second)
	reading:
		for {
			select {
			case <-done:
				break reading
			default:
				var n int
				var err error
				if n, err = rdr.Read(frame); err != nil {
					if err != io.EOF {
						t.Error(err)
					}
					break reading
				}
				gotFrames++
				got += uint64(n)
			}
		}
		bpf := got / gotFrames
		t.Log("Requested", bw, "B/s, got", got, "B in 1s (avg", bpf, "bpf)")
		if got > bw * 12 / 10 || got < bw * 8 / 10 {
			t.Error("Bandwidth out of acceptable range")
		}
		sm.Close()
	}
}

func TestHeader(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(runtime.NumCPU()))
	headerSize := uint64(64)
	nClients := 5
	sm := NewSourceMap()
	defer sm.Close()
	rdrs := make(chan *SourceReader, nClients)
	for i := 0; i < nClients; i++ {
		rdrs <- sm.NewReader("/dev/urandom", &Config{
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
		rdr := <-rdrs
		defer rdr.Close()
		n, err := rdr.Read(h)
		if err != nil {
			t.Error(err)
		} else if h0 == nil {
			h0 = h
		} else if bytes.Compare(h[:n], h0) != 0 {
			t.Errorf("Header mismatch: %v != %v", h0, h)
		} else if bytes.Compare(h[:n], empty) == 0 {
			t.Error("Header appears uninitialized")
		} else if uint64(n) != headerSize {
			t.Errorf("Header size mismatch: %d != %d", n, headerSize)
		}
		var frame = make(DataFrame, 65536)
		for f := 0; f < 6; f++ {
			var err error
			if _, err = rdr.Read(frame); err != nil {
				if err != io.EOF {
					t.Error(err)
				}
				break
			}
		}
	}
}

// TestContentEqual can be extremely slow (dozens of seconds) if
// GOMAXPROCS==1.
func TestContentEqual(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(runtime.NumCPU()))
	nConsumers := 10
	done := make(chan uint64, nConsumers)
	tab := crc64.MakeTable(crc64.ECMA)
	sm := NewSourceMap()
	defer sm.Close()
	for i := 0; i < nConsumers; i++ {
		go func() {
			rdr := sm.NewReader("/dev/urandom", &Config{
				SourceBuffer: 32,
				FrameBytes:   65536,
				CloseIdle:    true,
				Reopen:       false,
			})
			defer rdr.Close()
			var hash uint64
			defer func() { done <- hash }()
			var frame = make(DataFrame, 65536)
			for f := 0; f < 130; f++ {
				var err error
				if _, err = rdr.Read(frame); err != nil && err != io.EOF {
					t.Fatalf("rdr.Read(frame) at frame %d: %s", rdr.nextFrame, err)
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

func benchSource(b *testing.B, nConsumers int, conf Config) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(runtime.NumCPU()))
	sm := NewSourceMap()
	defer sm.Close()
	wg := &sync.WaitGroup{}
	wg.Add(nConsumers)
	for c := 0; c < nConsumers; c++ {
		go func(c int) {
			defer wg.Done()
			rdr := sm.NewReader("/dev/zero", &conf)
			defer rdr.Close()
			consume(b, rdr, c)
		}(c)
	}
	wg.Wait()
}

func consume(b *testing.B, rdr *SourceReader, label interface{}) {
	var frame = make(DataFrame, rdr.source.frameBytes)
	for i := uint64(0); i < 10*uint64(b.N); i++ {
		if _, err := rdr.Read(frame); err != nil {
			if err != io.EOF {
				b.Fatal("rdr.Read(): ", err)
			}
			break
		}
	}
}
