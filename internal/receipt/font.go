package receipt

import (
	"fmt"
	"sync"

	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// Glyph outlines are kept in "em1000" units: 1000 units per em, origin on the
// baseline, Y pointing down. Both renderers scale them by size/1000.
const em = 1000

const (
	// Go Mono metrics, in font units of a 2048-unit em.
	unitsPerEm = 2048
	advanceFU  = 1229 // every Go Mono glyph has this advance
	capFU      = 1480
)

// advance is the horizontal advance of one cell, in em1000 units.
const advance = em * advanceFU / float64(unitsPerEm)

type face struct {
	id     string // "r" or "b"; prefixes glyph ids in SVG
	font   *sfnt.Font
	mu     sync.Mutex
	buf    sfnt.Buffer
	glyphs map[rune]*path
}

var faces = sync.OnceValue(func() [2]*face {
	load := func(id string, ttf []byte) *face {
		f, err := sfnt.Parse(ttf)
		if err != nil {
			panic(fmt.Sprintf("receipt: embedded font %s: %v", id, err)) // embedded data; tested
		}
		return &face{id: id, font: f, glyphs: map[rune]*path{}}
	}
	return [2]*face{load("r", gomono.TTF), load("b", gomonobold.TTF)}
})

func faceFor(bold bool) *face {
	if bold {
		return faces()[1]
	}
	return faces()[0]
}

// has reports whether Go Mono can draw r.
func has(r rune) bool {
	f := faceFor(false)
	f.mu.Lock()
	defer f.mu.Unlock()
	i, err := f.font.GlyphIndex(&f.buf, r)
	return err == nil && i != 0
}

// glyph returns the outline of r in em1000 units; nil for blank glyphs.
func (f *face) glyph(r rune) *path {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.glyphs[r]; ok {
		return p
	}
	var p *path
	if i, err := f.font.GlyphIndex(&f.buf, r); err == nil && i != 0 {
		// Loading at ppem = unitsPerEm yields font units in 26.6 fixed point.
		segs, err := f.font.LoadGlyph(&f.buf, i, fixed.I(unitsPerEm), nil)
		if err == nil && len(segs) > 0 {
			p = &path{}
			pt := func(a fixed.Point26_6) pt {
				const k = em / float64(unitsPerEm) / 64
				return pt{float64(a.X) * k, float64(a.Y) * k}
			}
			for _, s := range segs {
				switch s.Op {
				case sfnt.SegmentOpMoveTo:
					if len(p.ops) > 0 {
						p.close()
					}
					p.moveTo(pt(s.Args[0]))
				case sfnt.SegmentOpLineTo:
					p.lineTo(pt(s.Args[0]))
				case sfnt.SegmentOpQuadTo:
					p.quadTo(pt(s.Args[0]), pt(s.Args[1]))
				case sfnt.SegmentOpCubeTo:
					p.cubeTo(pt(s.Args[0]), pt(s.Args[1]), pt(s.Args[2]))
				}
			}
			p.close()
		}
	}
	f.glyphs[r] = p
	return p
}
