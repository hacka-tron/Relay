// Command mkicon draws the app icon (Bolt's face on a rounded square) and
// writes it as a 1024x1024 PNG. The output is committed as build/icon.png;
// rerun this only to change the icon:
//
//	go run ./scripts/mkicon build/icon.png
package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

const size = 1024

var (
	bg     = color.RGBA{0xEA, 0xF2, 0xFB, 0xFF}
	amber  = color.RGBA{0xF5, 0x9E, 0x0B, 0xFF}
	cream  = color.RGBA{0xFF, 0xF4, 0xDD, 0xFF}
	ink    = color.RGBA{0x17, 0x26, 0x3A, 0xFF}
	nothin = color.RGBA{}
)

// shape reports whether point (x, y) in a 200x200 design space is inside,
// and its colour. Shapes later in the list paint over earlier ones.
type shape func(x, y float64) bool

func roundRect(x0, y0, x1, y1, r float64) shape {
	return func(x, y float64) bool {
		cx := math.Max(x0+r, math.Min(x, x1-r))
		cy := math.Max(y0+r, math.Min(y, y1-r))
		return x >= x0 && x <= x1 && y >= y0 && y <= y1 && math.Hypot(x-cx, y-cy) <= r
	}
}

func circle(cx, cy, r float64) shape {
	return func(x, y float64) bool { return math.Hypot(x-cx, y-cy) <= r }
}

// smile is a thick arc: the lower part of a ring.
func smile(cx, cy, r, w float64) shape {
	return func(x, y float64) bool {
		d := math.Hypot(x-cx, y-cy)
		return y > cy+r*0.35 && d >= r-w/2 && d <= r+w/2
	}
}

func main() {
	layers := []struct {
		s shape
		c color.RGBA
	}{
		{roundRect(0, 0, 200, 200, 44), bg},
		{roundRect(97, 30, 103, 60, 3), ink},     // antenna
		{circle(100, 28, 10), amber},             // antenna tip
		{roundRect(30, 52, 170, 172, 44), amber}, // head
		{roundRect(46, 76, 154, 146, 26), cream}, // face
		{circle(78, 106, 9), ink},
		{circle(122, 106, 9), ink},
		{smile(100, 108, 22, 6), ink},
	}
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	const ss = 4 // supersampling per axis, for smooth edges
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/ss) * 200 / size
					y := (float64(py) + (float64(sy)+0.5)/ss) * 200 / size
					c := nothin
					for _, l := range layers {
						if l.s(x, y) {
							c = l.c
						}
					}
					r, g, b, a = r+float64(c.R), g+float64(c.G), b+float64(c.B), a+float64(c.A)
				}
			}
			n := float64(ss * ss)
			img.Set(px, py, color.RGBA{uint8(r / n), uint8(g / n), uint8(b / n), uint8(a / n)})
		}
	}
	f, err := os.Create(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}
