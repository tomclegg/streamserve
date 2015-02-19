package main

import (
	"testing"
)

func TestConfigCheck(t *testing.T) {
	ok := Config{
		SourceBuffer: 3,
		FrameBytes:   1,
		Path:         "/dev/stdin",
		Addr:         ":http",
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
