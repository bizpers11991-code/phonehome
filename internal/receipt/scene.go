package receipt

import (
	"image/color"
	"math"
)

// The palette: warm thermal paper, near-black ink and one faded red accent
// reserved for the grade stamp, warnings and the demo watermark.
var (
	paperColor  = color.NRGBA{0xFB, 0xF8, 0xF1, 0xFF}
	inkColor    = color.NRGBA{0x1F, 0x1D, 0x1A, 0xFF}
	accentColor = color.NRGBA{0xC0, 0x39, 0x2B, 0xFF}
	shadowColor = color.NRGBA{0x2B, 0x22, 0x18, 0x12}
)

// glyphRun is a row of glyphs on the grid; m maps em1000 glyph space of the
// first cell to the page, and each later cell is offset by one advance.
type glyphRun struct {
	face  *face
	m     affine
	runes []rune
}

// layer is a set of shapes filled with one colour and composited as a group,
// so overlapping shapes within a layer never double up their opacity.
type layer struct {
	fill  color.NRGBA
	paths []*path // page coordinates
	runs  []glyphRun
}

type scene struct {
	w, h   float64
	layers []layer
}

// geom holds the layout metrics, all derived from the pixel width.
type geom struct {
	s      float64 // scale relative to DefaultWidth
	margin float64 // transparent room around the paper for its shadow
	tear   float64 // depth of the torn edge
	x0     float64 // left edge of the text grid
	cw     float64 // cell width
	fs     float64 // font size (px per em)
	lh     float64 // line height
}

func newGeom(width int) geom {
	w := float64(width)
	g := geom{s: w / DefaultWidth, cw: w * 0.02}
	g.margin = 18 * g.s
	g.tear = 7 * g.s
	g.x0 = (w - Cols*g.cw) / 2
	g.fs = g.cw * em / advance
	g.lh = 1.42 * g.fs
	return g
}

func (g geom) height(l Line) float64 {
	switch l.Kind {
	case Header:
		return 2.2 * g.lh
	case Rule, DoubleRule:
		return 0.9 * g.lh
	case Spacer:
		return 0.5 * g.lh
	case Stamp:
		return 5.6 * g.lh
	case Barcode:
		return 1.9 * g.lh
	}
	return g.lh
}

// capHeight is the height of a capital letter at font size fs.
func capHeight(fs float64) float64 { return fs * capFU / unitsPerEm }

// rng is a tiny deterministic generator (SplitMix64) for decorative noise.
type rng uint64

func (r *rng) next() uint64 {
	*r += 0x9E3779B97F4A7C15
	z := uint64(*r)
	z = (z ^ z>>30) * 0xBF58476D1CE4E5B9
	z = (z ^ z>>27) * 0x94D049BB133111EB
	return z ^ z>>31
}

// float returns a value in [lo, hi).
func (r *rng) float(lo, hi float64) float64 {
	return lo + (hi-lo)*float64(r.next()>>11)/(1<<53)
}

// scene lays the document out as vector layers.
func (d Doc) scene() scene {
	g := newGeom(d.Width)
	w := float64(d.Width)
	top := g.margin + g.tear + 26*g.s
	body := 0.0
	for _, l := range d.Lines {
		body += g.height(l)
	}
	h := math.Ceil(top + body + 24*g.s + g.tear + g.margin*1.6)
	sc := scene{w: w, h: h}
	noise := rng(d.seed)

	paper := tornPaper(g.margin, g.margin, w-2*g.margin, h-g.margin*2.6, g.tear, &noise, g.s)
	for i := 1; i <= 6; i++ {
		f := float64(i)
		sc.layers = append(sc.layers, layer{fill: shadowColor, paths: []*path{
			paper.transformed(affine{1, 0, 0, 1, 0, f * 1.3 * g.s}),
		}})
	}
	sc.layers = append(sc.layers, layer{fill: paperColor, paths: []*path{paper}})

	ink := layer{fill: inkColor}
	accent := layer{fill: accentColor}
	var stamp []layer
	y := top
	for _, l := range d.Lines {
		lh := g.height(l)
		dst := &ink
		if l.Accent {
			dst = &accent
		}
		switch l.Kind {
		case Text:
			dst.runs = append(dst.runs, g.run(l.Text, l.Bold, y+lh/2+capHeight(g.fs)/2))
		case Header:
			n := float64(width(l.Text))
			x := g.x0 + (Cols-2*n)/2*g.cw
			dst.runs = append(dst.runs, glyphRun{
				face:  faceFor(l.Bold),
				m:     scaleAt(2*g.fs/em, x, y+lh/2+capHeight(g.fs)),
				runes: []rune(l.Text),
			})
		case Rule:
			p := &path{}
			for c := 0; c < Cols; c++ {
				p.rect(g.x0+(float64(c)+0.2)*g.cw, y+lh/2-0.8*g.s, 0.6*g.cw, 1.6*g.s)
			}
			dst.paths = append(dst.paths, p)
		case DoubleRule:
			p := &path{}
			p.rect(g.x0, y+lh/2-3*g.s, Cols*g.cw, 1.5*g.s)
			p.rect(g.x0, y+lh/2+1.5*g.s, Cols*g.cw, 1.5*g.s)
			dst.paths = append(dst.paths, p)
		case Stamp:
			for i, a := range l.Aside {
				ink.runs = append(ink.runs, g.run(a, i == 0, y+(float64(i)+0.9)*g.lh+capHeight(g.fs)/2))
			}
			stamp = g.stamp(l.Text, g.x0+34*g.cw, y+lh/2, &noise)
		case Barcode:
			ink.paths = append(ink.paths, g.barcode(y, lh, &noise))
		}
		y += lh
	}
	sc.layers = append(sc.layers, ink, accent)
	sc.layers = append(sc.layers, stamp...)
	if d.Demo {
		sc.layers = append(sc.layers, g.watermark(w, h))
	}
	return sc
}

// run places a line of text on the grid with the given baseline.
func (g geom) run(s string, bold bool, baseline float64) glyphRun {
	return glyphRun{
		face:  faceFor(bold),
		m:     scaleAt(g.fs/em, g.x0, baseline),
		runes: []rune(s),
	}
}

// tornPaper outlines the receipt with zig-zag tears along top and bottom.
func tornPaper(x, y, w, h, depth float64, r *rng, s float64) *path {
	p := &path{}
	teeth := int(w / (11 * s))
	step := w / float64(teeth)
	p.moveTo(pt{x, y + depth})
	for i := 0; i < teeth; i++ {
		xi := x + float64(i)*step
		p.lineTo(pt{xi + step/2 + r.float(-1, 1)*s, y + r.float(0, 0.35)*depth})
		p.lineTo(pt{xi + step, y + depth*r.float(0.75, 1)})
	}
	p.lineTo(pt{x + w, y + h - depth})
	for i := teeth - 1; i >= 0; i-- {
		xi := x + float64(i)*step
		p.lineTo(pt{xi + step/2 + r.float(-1, 1)*s, y + h - r.float(0, 0.35)*depth})
		p.lineTo(pt{xi, y + h - depth*r.float(0.75, 1)})
	}
	p.close()
	return p
}

// stamp draws a rubber-stamped grade: a double-ringed rounded box rotated a
// few degrees, in translucent accent ink, worn by flecks of bare paper.
func (g geom) stamp(grade string, cx, cy float64, r *rng) []layer {
	w, h := 12.5*g.cw, 4.5*g.lh
	x, y := cx-w/2, cy-h/2
	rot := rotateAbout(-8, cx, cy)

	ring := &path{}
	t := 4 * g.s
	ring.roundRect(x, y, w, h, 12*g.s, false)
	ring.roundRect(x+t, y+t, w-2*t, h-2*t, 12*g.s-t, true)
	in, t2 := 7*g.s, 1.5*g.s
	ring.roundRect(x+in, y+in, w-2*in, h-2*in, 8*g.s, false)
	ring.roundRect(x+in+t2, y+in+t2, w-2*(in+t2), h-2*(in+t2), 8*g.s-t2, true)

	ink := layer{fill: accentColor, paths: []*path{ring.transformed(rot)}}
	ink.fill.A = 0xD8
	label := "GRADE"
	lfs := 0.95 * g.fs
	ink.runs = append(ink.runs, glyphRun{
		face:  faceFor(true),
		m:     scaleAt(lfs/em, cx-float64(len(label))*advance*lfs/em/2, y+0.29*h).then(rot),
		runes: []rune(label),
	})
	gfs := 0.47 * h / (capFU / float64(unitsPerEm))
	ink.runs = append(ink.runs, glyphRun{
		face:  faceFor(true),
		m:     scaleAt(gfs/em, cx-advance*gfs/em/2, y+0.86*h).then(rot),
		runes: []rune(grade),
	})

	// Worn ink: specks and short scuffs of bare paper over the stamp.
	wear := layer{fill: paperColor}
	wear.fill.A = 0xE0
	p := &path{}
	for i := 0; i < 260; i++ {
		px, py := x+r.float(0, w), y+r.float(0, h)
		rx := r.float(0.4, 1.7) * g.s
		ry := rx * r.float(0.5, 1)
		if i%19 == 0 {
			rx *= 5 // the odd scuff where the stamp missed the paper
		}
		p.ellipse(px, py, rx, ry)
	}
	wear.paths = append(wear.paths, p.transformed(rot))
	return []layer{ink, wear}
}

// barcode draws a decorative barcode. It encodes nothing: bar widths come
// from the receipt's content hash so each receipt looks unique.
func (g geom) barcode(y, lh float64, r *rng) *path {
	full := 30 * g.cw
	unit := full / 110
	x, end := g.x0+(Cols*g.cw-full)/2, g.x0+(Cols*g.cw+full)/2
	p := &path{}
	for x < end {
		bw := unit * float64(1+r.next()%3)
		if x+bw > end {
			break
		}
		p.rect(x, y+0.15*lh, bw, 0.8*lh)
		x += bw + unit*float64(1+r.next()%3)
	}
	return p
}

// watermark repeats a big diagonal DEMO DATA down the paper.
func (g geom) watermark(w, h float64) layer {
	const text = "DEMO DATA"
	l := layer{fill: accentColor}
	l.fill.A = 0x30
	fs := 0.86 * (w - 2*g.margin) / (float64(len(text)) * advance / em)
	tw := float64(len(text)) * advance * fs / em
	n := max(1, int(math.Round(h/(1.15*w))))
	for k := 0; k < n; k++ {
		cy := (float64(k) + 0.5) * h / float64(n)
		m := scaleAt(fs/em, w/2-tw/2, cy+capHeight(fs)/2).then(rotateAbout(-30, w/2, cy))
		l.runs = append(l.runs, glyphRun{face: faceFor(true), m: m, runes: []rune(text)})
	}
	return l
}
