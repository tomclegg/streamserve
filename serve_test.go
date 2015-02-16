package main

import (
	"regexp"
	"testing"
)

var validAddr = regexp.MustCompile(`^(\[[0-9a-f:\.]+\]|[0-9a-f\.]+):([0-9]+)$`)

func TestServerListeningAddr(t *testing.T) {
	listening := make(chan string)
	go func() {
		err := RunNewServer(Config{Addr: ":0"}, listening)
		t.Fatal(err)
	}()
	addr, ok := <-listening
	if !ok {
		t.Fatal("No address on listening channel")
	}
	t.Logf("Listening: %s", addr)
	if !validAddr.MatchString(addr) {
		t.Errorf("Invalid address from listening channel: %s", addr)
	}
}

func TestServerGetZero(t *testing.T) {
	listening := make(chan string)
	go RunNewServer(Config{Addr: ":0", Path: "/dev/zero"}, listening)
	addr, _ := <-listening
	t.Logf("TODO: connect to %s, read zeroes", addr)
}
