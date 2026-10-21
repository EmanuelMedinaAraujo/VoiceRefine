package main

import "math"

// The tray icon is a microphone on a disc whose colour says what VoiceRefine does.
var stateColors = map[string][3]byte{
	"setup":      {0x9c, 0xa3, 0xaf}, // grey: still downloading or loading
	Idle:         {0x25, 0x63, 0xeb}, // blue: ready
	Recording:    {0xdc, 0x26, 0x26}, // red: listening
	Transcribing: {0xd9, 0x77, 0x06}, // amber: writing the text
}

// iconPixels draws the icon at size×size as premultiplied BGRA rows, top row first.
func iconPixels(size int, rgb [3]byte) []byte {
	px := make([]byte, size*size*4)
	s := float64(size) / 32 // drawn on a 32-unit grid
	for y := range size {
		for x := range size {
			// The centre of the pixel on the 32-unit grid.
			gx, gy := (float64(x)+0.5)/s, (float64(y)+0.5)/s
			disc := coverage(math.Hypot(gx-16, gy-16)-15.5, s)
			mic := max(
				coverage(capsule(gx, gy, 16, 8.5, 16, 15.5, 4.2), s),
				coverage(arc(gx, gy), s),
				coverage(box(gx, gy, 14.9, 21.5, 17.1, 25.5), s),
				coverage(box(gx, gy, 11.5, 24.2, 20.5, 26.2), s),
			)
			// White microphone over the coloured disc.
			a := disc
			r := (float64(rgb[0])*(1-mic) + 255*mic) * a
			g := (float64(rgb[1])*(1-mic) + 255*mic) * a
			b := (float64(rgb[2])*(1-mic) + 255*mic) * a
			i := (y*size + x) * 4
			px[i], px[i+1], px[i+2], px[i+3] = byte(b), byte(g), byte(r), byte(a*255)
		}
	}
	return px
}

// coverage turns a signed distance on the 32-unit grid into how much of a pixel a shape covers.
func coverage(dist, scale float64) float64 {
	return min(1, max(0, 0.5-dist*scale))
}

// capsule is the distance to a rounded segment from (x1,y1) to (x2,y2) with radius r.
func capsule(x, y, x1, y1, x2, y2, r float64) float64 {
	dx, dy := x2-x1, y2-y1
	t := min(1, max(0, ((x-x1)*dx+(y-y1)*dy)/(dx*dx+dy*dy)))
	return math.Hypot(x-(x1+t*dx), y-(y1+t*dy)) - r
}

// arc is the microphone's holder: the lower half of a ring around the capsule.
func arc(x, y float64) float64 {
	ring := math.Abs(math.Hypot(x-16, y-15)-7) - 1.1
	if y < 15 {
		return max(ring, 15-y)
	}
	return ring
}

func box(x, y, x1, y1, x2, y2 float64) float64 {
	return max(x1-x, x-x2, y1-y, y-y2)
}
