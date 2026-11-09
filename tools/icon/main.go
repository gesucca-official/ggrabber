// Genera il logo di ggrabber: un grafo git che confluisce in una freccia verso un cassetto (backup).
//
//	go run ./tools/icon            -> assets/icon.png (1024x1024)
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"strings"

	xdraw "golang.org/x/image/draw"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"

	"golang.org/x/image/vector"
)

const S = 1024

func main() {
	out := flag.String("o", "assets/icon.png", "file di output")
	flag.Parse()

	img := image.NewRGBA(image.Rect(0, 0, S, S))
	bg := gradient(color.RGBA{99, 91, 255, 255}, color.RGBA{36, 28, 110, 255})
	white := image.NewUniform(color.RGBA{255, 255, 255, 255})

	fill(img, bg, func(z *vector.Rasterizer) { roundRect(z, 0, 0, S, S, 230) })

	// cassetto
	fill(img, white, func(z *vector.Rasterizer) {
		roundRect(z, 200, 600, 280, 840, 40)
		roundRect(z, 744, 600, 824, 840, 40)
		roundRect(z, 200, 760, 824, 840, 40)
	})
	// rami del grafo e freccia (forme separate: sovrapposte nello stesso tracciato si annullerebbero)
	fill(img, white, func(z *vector.Rasterizer) { line(z, 512, 170, 512, 590, 64) })
	fill(img, white, func(z *vector.Rasterizer) { curve(z, 300, 300, 512, 500, 64) })
	fill(img, white, func(z *vector.Rasterizer) { poly(z, [][2]float32{{372, 540}, {652, 540}, {512, 710}}) })
	// commit: anelli bianchi con il fondo che "buca" il centro
	for _, n := range [][2]float32{{512, 170}, {300, 290}} {
		fill(img, white, func(z *vector.Rasterizer) { circle(z, n[0], n[1], 82) })
		fill(img, bg, func(z *vector.Rasterizer) { circle(z, n[0], n[1], 42) })
	}

	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		log.Fatal(err)
	}
	var full bytes.Buffer
	png.Encode(&full, img)
	if err := os.WriteFile(*out, full.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}

	// .ico multi-risoluzione (le voci PNG sono supportate da Windows Vista in poi)
	sizes := []int{16, 24, 32, 48, 64, 128, 256}
	var entries [][]byte
	for _, s := range sizes {
		small := image.NewRGBA(image.Rect(0, 0, s, s))
		xdraw.CatmullRom.Scale(small, small.Bounds(), img, img.Bounds(), xdraw.Src, nil)
		var b bytes.Buffer
		png.Encode(&b, small)
		entries = append(entries, b.Bytes())
	}
	var ico bytes.Buffer
	binary.Write(&ico, binary.LittleEndian, [3]uint16{0, 1, uint16(len(sizes))})
	offset := 6 + 16*len(sizes)
	for i, s := range sizes {
		dim := uint8(s)
		if s == 256 {
			dim = 0
		}
		binary.Write(&ico, binary.LittleEndian, struct {
			W, H, Colors, Reserved uint8
			Planes, BPP            uint16
			Size, Offset           uint32
		}{dim, dim, 0, 0, 1, 32, uint32(len(entries[i])), uint32(offset)})
		offset += len(entries[i])
	}
	for _, e := range entries {
		ico.Write(e)
	}
	icoPath := strings.TrimSuffix(*out, filepath.Ext(*out)) + ".ico"
	if err := os.WriteFile(icoPath, ico.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
}

func gradient(top, bottom color.RGBA) *image.RGBA {
	g := image.NewRGBA(image.Rect(0, 0, S, S))
	for y := 0; y < S; y++ {
		t := float64(y) / (S - 1)
		c := color.RGBA{
			uint8(float64(top.R) + t*(float64(bottom.R)-float64(top.R))),
			uint8(float64(top.G) + t*(float64(bottom.G)-float64(top.G))),
			uint8(float64(top.B) + t*(float64(bottom.B)-float64(top.B))),
			255,
		}
		draw.Draw(g, image.Rect(0, y, S, y+1), image.NewUniform(c), image.Point{}, draw.Src)
	}
	return g
}

func fill(dst *image.RGBA, src image.Image, shape func(*vector.Rasterizer)) {
	z := vector.NewRasterizer(S, S)
	shape(z)
	z.Draw(dst, dst.Bounds(), src, image.Point{})
}

const k = 0.5523 // approssimazione del cerchio con curve di Bézier

func circle(z *vector.Rasterizer, cx, cy, r float32) {
	z.MoveTo(cx+r, cy)
	z.CubeTo(cx+r, cy+k*r, cx+k*r, cy+r, cx, cy+r)
	z.CubeTo(cx-k*r, cy+r, cx-r, cy+k*r, cx-r, cy)
	z.CubeTo(cx-r, cy-k*r, cx-k*r, cy-r, cx, cy-r)
	z.CubeTo(cx+k*r, cy-r, cx+r, cy-k*r, cx+r, cy)
	z.ClosePath()
}

func roundRect(z *vector.Rasterizer, x0, y0, x1, y1, r float32) {
	z.MoveTo(x0+r, y0)
	z.LineTo(x1-r, y0)
	z.CubeTo(x1-r+k*r, y0, x1, y0+r-k*r, x1, y0+r)
	z.LineTo(x1, y1-r)
	z.CubeTo(x1, y1-r+k*r, x1-r+k*r, y1, x1-r, y1)
	z.LineTo(x0+r, y1)
	z.CubeTo(x0+r-k*r, y1, x0, y1-r+k*r, x0, y1-r)
	z.LineTo(x0, y0+r)
	z.CubeTo(x0, y0+r-k*r, x0+r-k*r, y0, x0+r, y0)
	z.ClosePath()
}

func line(z *vector.Rasterizer, x0, y0, x1, y1, w float32) {
	dx, dy := x1-x0, y1-y0
	l := float32(math.Hypot(float64(dx), float64(dy)))
	nx, ny := -dy/l*w/2, dx/l*w/2
	poly(z, [][2]float32{{x0 + nx, y0 + ny}, {x1 + nx, y1 + ny}, {x1 - nx, y1 - ny}, {x0 - nx, y0 - ny}})
}

// curve: ramo che scende verticale da (x0,y0) e confluisce in (x1,y1), spesso w.
// La Bézier viene campionata e "ingrossata" lungo le normali in un unico poligono.
func curve(z *vector.Rasterizer, x0, y0, x1, y1, w float32) {
	p := [4][2]float64{{float64(x0), float64(y0)}, {float64(x0), float64(y1)}, {float64(x1), float64(y0)}, {float64(x1), float64(y1)}}
	// la curva parte verticale e arriva verticale
	p[1] = [2]float64{p[0][0], (p[0][1] + p[3][1]) / 2}
	p[2] = [2]float64{p[3][0], (p[0][1] + p[3][1]) / 2}
	const n = 64
	var left, right [][2]float32
	for i := 0; i <= n; i++ {
		t := float64(i) / n
		u := 1 - t
		x := u*u*u*p[0][0] + 3*u*u*t*p[1][0] + 3*u*t*t*p[2][0] + t*t*t*p[3][0]
		y := u*u*u*p[0][1] + 3*u*u*t*p[1][1] + 3*u*t*t*p[2][1] + t*t*t*p[3][1]
		dx := 3*u*u*(p[1][0]-p[0][0]) + 6*u*t*(p[2][0]-p[1][0]) + 3*t*t*(p[3][0]-p[2][0])
		dy := 3*u*u*(p[1][1]-p[0][1]) + 6*u*t*(p[2][1]-p[1][1]) + 3*t*t*(p[3][1]-p[2][1])
		l := math.Hypot(dx, dy)
		nx, ny := -dy/l*float64(w)/2, dx/l*float64(w)/2
		left = append(left, [2]float32{float32(x + nx), float32(y + ny)})
		right = append(right, [2]float32{float32(x - nx), float32(y - ny)})
	}
	for i := len(right) - 1; i >= 0; i-- {
		left = append(left, right[i])
	}
	poly(z, left)
}

func poly(z *vector.Rasterizer, pts [][2]float32) {
	z.MoveTo(pts[0][0], pts[0][1])
	for _, p := range pts[1:] {
		z.LineTo(p[0], p[1])
	}
	z.ClosePath()
}
