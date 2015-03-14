package main

import "testing"

func TestRawFilter(t *testing.T) {
	buf := []byte{0,1,2,3,4,5,6,7}
	var ctx interface{}
	if fs, err := RawFilter(buf, &ctx); !(fs == 8 && err == nil) {
		t.Error("fs =", fs, "err =", err)
	}
	if fs, err := RawFilter(buf[4:], &ctx); !(fs == 4 && err == nil) {
		t.Error("fs =", fs, "err =", err)
	}
	if fs, err := RawFilter(buf[:4], &ctx); !(fs == 8 && err == ShortFrame) {
		t.Error("fs =", fs, "err =", err)
	}
}
