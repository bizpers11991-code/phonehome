package receipt

import (
	"bytes"
	"image"
	"image/png"
	"math"

	"golang.org/x/image/vector"
)

// PNG renders the receipt at exactly Doc.Width pixels wide on a transparent
// background. Shapes and glyphs are rasterised from their vector outlines
// with exact-coverage anti-aliasing, so no supersampling is needed; for
// high-DPI sharing, lay the receipt out with a larger Options.Width instead.
func (d Doc) PNG() ([]byte, error) {
	sc := d.scene()
	img := image.NewRGBA(image.Rect(0, 0, int(sc.w), int(sc.h)))
	for _, l := range sc.layers {
		paths := l.outlines()
		r := bounds(paths).Intersect(img.Bounds())
		if r.Empty() {
			continue
		}
		z := vector.NewRasterizer(r.Dx(), r.Dy())
		ox, oy := float32(r.Min.X), float32(r.Min.Y)
		for _, p := range paths {
			for _, o := range p.ops {
				a, b, c := o.pts[0], o.pts[1], o.pts[2]
				switch o.kind {
				case opMove:
					z.MoveTo(float32(a.X)-ox, float32(a.Y)-oy)
				case opLine:
					z.LineTo(float32(a.X)-ox, float32(a.Y)-oy)
				case opQuad:
					z.QuadTo(float32(a.X)-ox, float32(a.Y)-oy, float32(b.X)-ox, float32(b.Y)-oy)
				case opCube:
					z.CubeTo(float32(a.X)-ox, float32(a.Y)-oy, float32(b.X)-ox, float32(b.Y)-oy, float32(c.X)-ox, float32(c.Y)-oy)
				case opClose:
					z.ClosePath()
				}
			}
		}
		z.Draw(img, r, image.NewUniform(l.fill), image.Point{})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// outlines flattens a layer's glyph runs into page-space paths.
func (l layer) outlines() []*path {
	paths := append([]*path(nil), l.paths...)
	for _, run := range l.runs {
		for i, r := range run.runes {
			if g := run.face.glyph(r); g != nil {
				paths = append(paths, g.transformed(affine{1, 0, 0, 1, float64(i) * advance, 0}.then(run.m)))
			}
		}
	}
	return paths
}

// bounds is the pixel rectangle covering every point of paths. Bézier
// control points bound their curves, so this is conservative.
func bounds(paths []*path) image.Rectangle {
	x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, p := range paths {
		for i := range p.ops {
			for _, q := range p.ops[i].points() {
				x0, y0 = min(x0, q.X), min(y0, q.Y)
				x1, y1 = max(x1, q.X), max(y1, q.Y)
			}
		}
	}
	if x0 > x1 {
		return image.Rectangle{}
	}
	return image.Rect(int(math.Floor(x0)), int(math.Floor(y0)), int(math.Ceil(x1)), int(math.Ceil(y1)))
}
