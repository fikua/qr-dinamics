package qrcodes

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"

	"rsc.io/qr"
)

// quietZone is the white margin, in modules, the QR spec requires around the code.
const quietZone = 4

// minPNGSize is the minimum side of a downloaded PNG, in pixels (US-03).
const minPNGSize = 1000

// errorLevel is the error-correction level: Q (25 %) is above the M the PRD
// asks for and leaves room for dirt and wear on printed stickers.
const errorLevel = qr.Q

// RenderPNG encodes text as a QR PNG of at least 1000 px with its quiet zone.
func RenderPNG(text string) ([]byte, error) {
	code, err := qr.Encode(text, errorLevel)
	if err != nil {
		return nil, fmt.Errorf("encode qr: %w", err)
	}
	modules := code.Size + 2*quietZone
	scale := (minPNGSize + modules - 1) / modules
	side := modules * scale

	img := image.NewGray(image.Rect(0, 0, side, side))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	for y := range code.Size {
		for x := range code.Size {
			if !code.Black(x, y) {
				continue
			}
			x0, y0 := (x+quietZone)*scale, (y+quietZone)*scale
			for py := y0; py < y0+scale; py++ {
				for px := x0; px < x0+scale; px++ {
					img.SetGray(px, py, color.Gray{Y: 0})
				}
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encode png: %w", err)
	}
	return buf.Bytes(), nil
}

// RenderSVG encodes text as a scalable QR SVG with its quiet zone.
func RenderSVG(text string) ([]byte, error) {
	code, err := qr.Encode(text, errorLevel)
	if err != nil {
		return nil, fmt.Errorf("encode qr: %w", err)
	}
	modules := code.Size + 2*quietZone
	var buf bytes.Buffer
	fmt.Fprintf(&buf, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`, modules, modules)
	fmt.Fprintf(&buf, `<rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, modules, modules)
	for y := range code.Size {
		for x := 0; x < code.Size; {
			if !code.Black(x, y) {
				x++
				continue
			}
			run := 1
			for x+run < code.Size && code.Black(x+run, y) {
				run++
			}
			fmt.Fprintf(&buf, "M%d %dh%dv1h-%dz", x+quietZone, y+quietZone, run, run)
			x += run
		}
	}
	buf.WriteString(`"/></svg>`)
	return buf.Bytes(), nil
}

// imageSize returns the side of a PNG, for tests.
func imageSize(b []byte) (int, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	return cfg.Width, nil
}
