package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"regexp"
	"sync"
	"testing"
	"time"
)

var validAddr = regexp.MustCompile(`^(\[[0-9a-f:\.]+\]|[0-9a-f\.]+):([0-9]+)$`)

func TestServerListeningAddr(t *testing.T) {
	listening := make(chan string)
	ctrl := make(chan string)
	go func() {
		err := RunNewServer(&Config{Addr: ":0", FrameBytes: 1, Path: "/dev/zero", SourceBuffer: 4}, listening, ctrl)
		if err != nil {
			t.Fatal(err)
		}
	}()
	addr, ok := <-listening
	if !ok {
		t.Fatal("No address on listening channel")
	}
	t.Logf("Listening: %s", addr)
	if !validAddr.MatchString(addr) {
		t.Errorf("Invalid address from listening channel: %s", addr)
	}
	ctrl <- "shutdown"
	CloseAllSources()
	// Wait for server to stop
	<-listening
}

func TestServerStopsIfCantReopen(t *testing.T) {
	listening := make(chan string, 1)
	done := make(chan bool)
	go func() {
		RunNewServer(&Config{
			Addr:           ":0",
			CloseIdle:      true,
			FrameBytes:     16,
			ClientMaxBytes: 16,
			Path:           "/dev/urandom",
			Reopen:         false,
			SourceBuffer:   4,
		}, listening, nil)
		done <- true
	}()
	resp, err := http.Get(fmt.Sprintf("http://%s/", <-listening))
	body, _ := ioutil.ReadAll(resp.Body)
	t.Logf("GET: resp %v, err %v", body, err)
	select {
	case <-time.After(time.Second):
		t.Error("Server should have shut down within 1s of client disconnect")
	case addr := <-listening:
		if addr != "" {
			t.Errorf("listening channel expect '', got %v", addr)
		}
	}
}

func BenchmarkServer128ClientsGetZero(b *testing.B) {
	devZeroToClients(*b, 128, b.N)
}

func devZeroToClients(b testing.B, nClients int, nBytesPerClient int) {
	listening := make(chan string)
	ctrl := make(chan string)
	go func() {
		err := RunNewServer(&Config{Addr: ":0", FrameBytes: 2 << 11, Path: "/dev/zero", SourceBuffer: 2 << 11, StatLogInterval: time.Duration(1000000000)}, listening, ctrl)
		if err != nil {
			b.Fatal(err)
		}
	}()
	addr, _ := <-listening
	clientwg := sync.WaitGroup{}
	clientwg.Add(nClients)
	for i := 0; i < nClients; i++ {
		go func(i int) {
			defer clientwg.Done()
			resp, err := http.Get(fmt.Sprintf("http://%s/", addr))
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
	ctrl <- "shutdown"
	CloseAllSources()
	// Wait for server to stop
	<-listening
}
