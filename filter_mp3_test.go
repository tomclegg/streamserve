package main

import "testing"

var v1bits = 3 << 3
var v2bits = 2 << 3
var v25bits = 0
var lIbits = 3 << 1
var lIIbits = 2 << 1
var lIIIbits = 1 << 1
var brShift = uint(4)
var srShift = uint(2)
var padShift = uint(1)

func TestMp3FilterVersion1LayerI(t *testing.T) {
	shouldFilter(t, (12*128000/48000+1)*4,
		0377, byte(0340|v1bits|lIbits), byte((4<<brShift)|(1<<srShift)|(1<<padShift)))
}

func TestMp3FilterVersion2LayerIII(t *testing.T) {
	shouldFilter(t, 72*160000/24000+1,
		0377, byte(0340|v2bits|lIIIbits), byte((14<<brShift)|(1<<srShift)|(1<<padShift)))
}

func TestMp3FilterVersion25LayerII(t *testing.T) {
	shouldFilter(t, 144*144000/11025,
		0377, byte(0340|v25bits|lIIbits), byte((13<<brShift)|(0<<srShift)|(0<<padShift)))
}

func TestMp3FilterLame128k44100(t *testing.T) {
	// Data from `</dev/zero lame -r -b 128 - -`
	data := make([]byte, 418)
	copy(data, []byte{
		0xff, 0xfb, 0x92, 0x64, 0x40, 0x8f, 0xf0, 0,
		0, 0x69, 0, 0, 0, 0x08, 0, 0,
		0x0d, 0x20, 0, 0, 0x01, 0, 0, 1,
		0xa4, 0, 0, 0, 0x20, 0, 0, 0x34,
		0x80, 0, 0, 4,
	})
	for i := 36; i < len(data); i++ {
		data[i] = 0x55
	}
	shouldFilter(t, 418, data...)
}

func TestMp3FilterLame40k16000(t *testing.T) {
	// Data from `</dev/zero lame -r -b 40 - -`
	data := make([]byte, 180)
	copy(data, []byte{
		0xff, 0xf3, 0x58, 0x64, 0x60, 0, 0, 1,
		0xa4, 0, 0, 0, 0, 0, 0, 3,
		0x48, 0, 0, 0, 0,
	})
	for i := 21; i < len(data); i++ {
		data[i] = 0x55
	}
	shouldFilter(t, 180, data...)
}

func shouldFilter(t *testing.T, fSize int, header ...byte) {
	okFrame := append(header, make([]byte, fSize-len(header))...)
	if fs, _, err := Mp3Filter(okFrame, nil); err != nil || fs != fSize {
		t.Errorf("Good frame (%d, %v) returned %d, %s", fSize, header, fs, err)
	}
	if fs, _, err := Mp3Filter(append(okFrame, byte(0)), nil); err != nil || fs != fSize {
		t.Errorf("Good frame (%d+1, %v) returned %d, %s", fSize, header, fs, err)
	}
	if fs, _, err := Mp3Filter(okFrame[:fSize-1], nil); err != ErrShortFrame {
		t.Errorf("Short frame (%d, %v) returned %d, %s", fSize-1, header, fs, err)
	}
}
