package main

import "testing"

func TestRawFilter(t *testing.T) {
	buf := []byte{0, 1, 2, 3, 4, 5, 6, 7}
	if fs, _, err := RawFilter(buf, nil); !(fs == 8 && err == nil) {
		t.Error("fs =", fs, "err =", err)
	}
	if fs, _, err := RawFilter(buf[4:], nil); !(fs == 4 && err == nil) {
		t.Error("fs =", fs, "err =", err)
	}
	if fs, _, err := RawFilter(buf[:4], nil); !(fs == 8 && err == ShortFrame) {
		t.Error("fs =", fs, "err =", err)
	}
}
