package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"net/http"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var validAddr = regexp.MustCompile(`^(\[[0-9a-f:\.]+\]|[0-9a-f\.]+):([0-9]+)$`)

func TestServerListeningAddr(t *testing.T) {
	srv := &Server{}
	err := srv.Run(&Config{Addr: ":0", FrameBytes: 1, Path: "/dev/zero", SourceBuffer: 4})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Listening: %s", srv.Addr)
	if !validAddr.MatchString(srv.Addr) {
		t.Errorf("Invalid address from listening channel: %s", srv.Addr)
	}
	srv.Close()
}

func TestServerStopsIfCantReopen(t *testing.T) {
	srv := &Server{}
	srv.Run(&Config{
		Addr:           ":0",
		CloseIdle:      true,
		FrameBytes:     16,
		ClientMaxBytes: 16,
		Path:           "/dev/urandom",
		Reopen:         false,
		SourceBuffer:   4,
	})
	resp, err := http.Get(fmt.Sprintf("http://%s/", srv.Addr))
	body, _ := ioutil.ReadAll(resp.Body)
	t.Logf("GET: resp %v, err %v", body, err)
	done := make(chan bool)
	go func() {
		srv.Wait()
		done <- true
	}()
	select {
	case <-time.After(time.Second):
		t.Error("Server should have shut down within 1s of client disconnect")
	case <-done:
	}
}

func TestBigFrame(t *testing.T) {
	wantlen := 1<<22	// >4x io.Copy's buffer
	srv := &Server{}
	srv.Run(&Config{
		Addr:           ":0",
		CloseIdle:      true,
		FrameBytes:     uint64(wantlen>>2),
		ClientMaxBytes: uint64(wantlen),
		Path:           "/dev/urandom",
		Reopen:         false,
		SourceBuffer:   5,
	})
	defer srv.Close()
	resp, err := http.Get(fmt.Sprintf("http://%s/", srv.Addr))
	if err != nil {
		t.Fatal("Got err ", err)
	}
	if body, err := ioutil.ReadAll(resp.Body); len(body) != wantlen || err != nil {
		t.Error("Got len(body)", len(body), "err", err, "-- wanted", wantlen)
	}
}

func TestClientRateSpread(t *testing.T) {
	nClients := 500
	bytesRcvd := uint64(0)
	stopAll := false
	srv := &Server{}
	srv.Run(&Config{
		Addr:            ":0",
		CloseIdle:       true,
		FrameBytes:      1 << 10,
		Path:            "/dev/urandom",
		Reopen:          false,
		SourceBandwidth: 1 << 26, // 64 MiB/s
		SourceBuffer:    1 << 8,
	})
	allConnected := make(chan bool)
	nClientsConnected := int64(0)
	clientwg := sync.WaitGroup{}
	clientwg.Add(nClients)
	for i := 0; i < nClients; i++ {
		go func() {
			defer clientwg.Done()
			bandwidth := (rand.Int() & 0x3fffff) + (1 << 22) // 4..8 MiB/s
			resp, err := http.Get(fmt.Sprintf("http://%s/", srv.Addr))
			if int64(nClients) == atomic.AddInt64(&nClientsConnected, 1) {
				allConnected <- true
			}
			if err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 1<<8)
			tick := time.NewTicker(time.Duration(time.Second * time.Duration(len(buf)) / time.Duration(bandwidth))).C
			for !stopAll {
				<-tick
				n, err := io.ReadFull(resp.Body, buf)
				atomic.AddUint64(&bytesRcvd, uint64(n))
				if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
					t.Fatal(err)
				}
			}
		}()
	}
	<-allConnected
	<-time.After(3 * time.Second)
	stopAll = true
	srv.Close()
	clientwg.Wait()
	t.Logf("Received %d bytes", bytesRcvd)
}

func BenchmarkServer128ClientsGetZero(b *testing.B) {
	devZeroToClients(b, 128, b.N)
}

func devZeroToClients(b *testing.B, nClients int, nBytesPerClient int) {
	srv := &Server{}
	err := srv.Run(&Config{Addr: ":0", FrameBytes: 2 << 11, Path: "/dev/zero", SourceBuffer: 2 << 11, StatLogInterval: time.Duration(time.Second)})
	if err != nil {
		b.Fatal(err)
	}
	clientwg := sync.WaitGroup{}
	clientwg.Add(nClients)
	for i := 0; i < nClients; i++ {
		go func(i int) {
			defer clientwg.Done()
			resp, err := http.Get(fmt.Sprintf("http://%s/", srv.Addr))
			if err != nil {
				b.Errorf("Client %d: %s", i, err)
				return
			}
			defer resp.Body.Close()
			got := 0
			buf := make([]byte, 1<<20)
			for err == nil && got < nBytesPerClient {
				var n int
				n, err = resp.Body.Read(buf)
				if n > 0 {
					got += n
				}
			}
		}(i)
	}
	clientwg.Wait()
	srv.Close()
	// Wait for server to stop
}
