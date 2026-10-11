package storage

import (
	"encoding/binary"
	"image"
)

// DecodeConfig may return VP8X canvas size before inspecting a VP8/VP8L frame.
// Inspect the bounded RIFF payload without decoding pixels, and require the
// actual frame and canvas to agree before the decoder can allocate either.
func validAvatarWebPFrames(data []byte, config image.Config) bool {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" || uint64(binary.LittleEndian.Uint32(data[4:8]))+8 != uint64(len(data)) {
		return false
	}
	canvasWidth, canvasHeight := 0, 0
	var canvasFlags byte
	seenFrame, seenAlpha := false, false
	for pos := 12; pos < len(data); {
		if len(data)-pos < 8 {
			return false
		}
		size := uint64(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		end := uint64(pos) + 8 + size
		next := end + (size & 1)
		if next > uint64(len(data)) {
			return false
		}
		payload := data[pos+8 : int(end)]
		switch string(data[pos : pos+4]) {
		case "VP8X":
			if pos != 12 || len(payload) != 10 {
				return false
			}
			canvasFlags = payload[0]
			// The pinned decoder supports still images, not ANIM/ANMF animation frames.
			if canvasFlags&2 != 0 {
				return false
			}
			canvasWidth = 1 + int(payload[4]) + int(payload[5])<<8 + int(payload[6])<<16
			canvasHeight = 1 + int(payload[7]) + int(payload[8])<<8 + int(payload[9])<<16
			if canvasWidth > AvatarMaxDimension || canvasHeight > AvatarMaxDimension {
				return false
			}
		case "ALPH":
			if canvasWidth == 0 || canvasFlags&16 == 0 || seenFrame || seenAlpha || len(payload) < 1 {
				return false
			}
			seenAlpha = true // alpha allocation uses the already bounded VP8X canvas
		case "VP8 ", "VP8L":
			if seenFrame {
				return false
			}
			width, height := 0, 0
			if string(data[pos:pos+4]) == "VP8L" {
				if len(payload) < 5 || payload[0] != 0x2f || seenAlpha {
					return false
				}
				header := binary.LittleEndian.Uint32(payload[1:5])
				if header>>29 != 0 {
					return false
				} // VP8L version must be zero
				width = int(header&0x3fff) + 1
				height = int((header>>14)&0x3fff) + 1
			} else {
				// WebP VP8 is a keyframe: frame tag, fixed start code, then 14-bit sizes.
				if len(payload) < 10 || payload[0]&1 != 0 || payload[3] != 0x9d || payload[4] != 0x01 || payload[5] != 0x2a {
					return false
				}
				width = int(binary.LittleEndian.Uint16(payload[6:8]) & 0x3fff)
				height = int(binary.LittleEndian.Uint16(payload[8:10]) & 0x3fff)
			}
			if width <= 0 || height <= 0 || width > AvatarMaxDimension || height > AvatarMaxDimension {
				return false
			}
			if width != config.Width || height != config.Height {
				return false
			}
			if canvasWidth != 0 && (width != canvasWidth || height != canvasHeight) {
				return false
			}
			seenFrame = true
		case "ANIM", "ANMF":
			return false
		}
		pos = int(next)
	}
	return seenFrame
}
