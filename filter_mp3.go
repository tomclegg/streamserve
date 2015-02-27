package main

import "log"

func init() {
	Filters["mp3"] = Mp3Filter
}

func Mp3Filter(frame []byte, context *interface{}) (frameSize int, err error) {
	if len(frame) < 4 {
		err = ShortFrame
		return
	}
	if frame[0] != '\377' || (frame[1]&'\340') != '\340' {
		err = InvalidFrame
		return
	}
	version := int(frame[1]>>3) & 3
	layer := int(frame[1]>>1) & 3
	var bitrate int
	{
		rate := int(frame[2]>>4) & 15
		bitrates := bitrateTable[version][layer]
		if len(bitrates) <= rate || bitrates[rate] <= 0 {
			err = InvalidFrame
			return
		}
		bitrate = bitrates[rate] * 1000
	}
	var samplerate int
	{
		rate := int(frame[2]>>2) & 3
		samplerates := samplerateTable[version]
		if len(samplerates) <= rate {
			err = InvalidFrame
			return
		}
		samplerate = samplerates[rate]
	}
	padding := int(frame[2]>>1) & 1
	switch {
	case layer == layerI:
		frameSize = (12*bitrate/samplerate + padding) * 4
	case layer == layerIII && version != version1:
		// MPEG-2 layer III and MPEG-2.5 layer III frames are
		// half the size of other layer II and III frames.
		frameSize = 72*bitrate/samplerate + padding
	default:
		frameSize = 144*bitrate/samplerate + padding
	}
	if frameSize > len(frame) {
		err = ShortFrame
	} else if Debugging {
		log.Printf("frameSize %d len %d MPEG-%s layer %s bitrate %d samplerate %d padding %d", frameSize, len(frame), versionName[version], layerName[layer], bitrate, samplerate, padding)
	}
	return
}

const (
	layerI     = 3
	layerII    = 2
	layerIII   = 1
	version1   = 3
	version2   = 2
	version2_5 = 0
)

var layerName = []string{"", "III", "II", "I"}
var versionName = []string{"2.5", "", "2.0", "1.0"}

var invalid = []int{}
var v1l1_bitrate = []int{-1, 32, 64, 96, 128, 160, 192, 224, 256, 288, 320, 352, 384, 416, 448, -1}
var v1l2_bitrate = []int{-1, 32, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384, -1}
var v1l3_bitrate = []int{-1, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, -1}
var v2l1_bitrate = []int{-1, 32, 48, 56, 64, 80, 96, 112, 128, 144, 160, 176, 192, 224, 256, -1}
var v2l2_bitrate = []int{-1, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, -1}
var bitrateTable = [][][]int{
	[][]int{invalid, v2l2_bitrate, v2l2_bitrate, v2l1_bitrate}, // version2_5
	[][]int{invalid, invalid, invalid, invalid},
	[][]int{invalid, v2l2_bitrate, v2l2_bitrate, v2l1_bitrate}, // version2
	[][]int{invalid, v1l3_bitrate, v1l2_bitrate, v1l1_bitrate}, // version1
}
var samplerateTable = [][]int{
	[]int{11025, 12000, 8000}, // version2_5
	invalid,
	[]int{22050, 24000, 16000}, // version2
	[]int{44100, 48000, 32000}, // version1
}
