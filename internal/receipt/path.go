package receipt

import "math"

type pt struct{ X, Y float64 }

type opKind byte

const (
	opMove  opKind = 'M'
	opLine  opKind = 'L'
	opQuad  opKind = 'Q'
	opCube  opKind = 'C'
	opClose opKind = 'Z'
)

type op struct {
	kind opKind
	pts  [3]pt
}

// points returns the points the op uses.
func (o *op) points() []pt {
	switch o.kind {
	case opMove, opLine:
		return o.pts[:1]
	case opQuad:
		return o.pts[:2]
	case opCube:
		return o.pts[:3]
	}
	return nil
}

// path is a vector outline shared by the SVG and PNG renderers.
type path struct{ ops []op }

func (p *path) moveTo(a pt)       { p.ops = append(p.ops, op{kind: opMove, pts: [3]pt{a}}) }
func (p *path) lineTo(a pt)       { p.ops = append(p.ops, op{kind: opLine, pts: [3]pt{a}}) }
func (p *path) quadTo(a, b pt)    { p.ops = append(p.ops, op{kind: opQuad, pts: [3]pt{a, b}}) }
func (p *path) cubeTo(a, b, c pt) { p.ops = append(p.ops, op{kind: opCube, pts: [3]pt{a, b, c}}) }
func (p *path) close()            { p.ops = append(p.ops, op{kind: opClose}) }
func (p *path) rect(x, y, w, h float64) {
	p.moveTo(pt{x, y})
	p.lineTo(pt{x + w, y})
	p.lineTo(pt{x + w, y + h})
	p.lineTo(pt{x, y + h})
	p.close()
}

// roundRect adds a rounded rectangle; reverse flips the winding so it can
// punch a hole in an enclosing shape.
func (p *path) roundRect(x, y, w, h, r float64, reverse bool) {
	const k = 0.5523 // cubic approximation of a quarter circle
	c := r * k
	if !reverse {
		p.moveTo(pt{x + r, y})
		p.lineTo(pt{x + w - r, y})
		p.cubeTo(pt{x + w - r + c, y}, pt{x + w, y + r - c}, pt{x + w, y + r})
		p.lineTo(pt{x + w, y + h - r})
		p.cubeTo(pt{x + w, y + h - r + c}, pt{x + w - r + c, y + h}, pt{x + w - r, y + h})
		p.lineTo(pt{x + r, y + h})
		p.cubeTo(pt{x + r - c, y + h}, pt{x, y + h - r + c}, pt{x, y + h - r})
		p.lineTo(pt{x, y + r})
		p.cubeTo(pt{x, y + r - c}, pt{x + r - c, y}, pt{x + r, y})
	} else {
		p.moveTo(pt{x + r, y})
		p.cubeTo(pt{x + r - c, y}, pt{x, y + r - c}, pt{x, y + r})
		p.lineTo(pt{x, y + h - r})
		p.cubeTo(pt{x, y + h - r + c}, pt{x + r - c, y + h}, pt{x + r, y + h})
		p.lineTo(pt{x + w - r, y + h})
		p.cubeTo(pt{x + w - r + c, y + h}, pt{x + w, y + h - r + c}, pt{x + w, y + h - r})
		p.lineTo(pt{x + w, y + r})
		p.cubeTo(pt{x + w, y + r - c}, pt{x + w - r + c, y}, pt{x + w - r, y})
	}
	p.close()
}

// ellipse adds an axis-aligned ellipse centred on (cx, cy).
func (p *path) ellipse(cx, cy, rx, ry float64) {
	const k = 0.5523
	p.moveTo(pt{cx + rx, cy})
	p.cubeTo(pt{cx + rx, cy + ry*k}, pt{cx + rx*k, cy + ry}, pt{cx, cy + ry})
	p.cubeTo(pt{cx - rx*k, cy + ry}, pt{cx - rx, cy + ry*k}, pt{cx - rx, cy})
	p.cubeTo(pt{cx - rx, cy - ry*k}, pt{cx - rx*k, cy - ry}, pt{cx, cy - ry})
	p.cubeTo(pt{cx + rx*k, cy - ry}, pt{cx + rx, cy - ry*k}, pt{cx + rx, cy})
	p.close()
}

// affine is the matrix [a c e; b d f], mapping (x, y) to
// (a*x + c*y + e, b*x + d*y + f), like SVG's matrix(a b c d e f).
type affine struct{ a, b, c, d, e, f float64 }

func scaleAt(s, x, y float64) affine { return affine{s, 0, 0, s, x, y} }

// rotateAbout rotates by deg degrees (clockwise on screen) around (x, y).
func rotateAbout(deg, x, y float64) affine {
	sin, cos := math.Sincos(deg * math.Pi / 180)
	return affine{cos, sin, -sin, cos, x - cos*x + sin*y, y - sin*x - cos*y}
}

// then returns the transform that applies m first and n second.
func (m affine) then(n affine) affine {
	return affine{
		a: n.a*m.a + n.c*m.b,
		b: n.b*m.a + n.d*m.b,
		c: n.a*m.c + n.c*m.d,
		d: n.b*m.c + n.d*m.d,
		e: n.a*m.e + n.c*m.f + n.e,
		f: n.b*m.e + n.d*m.f + n.f,
	}
}

func (m affine) apply(p pt) pt {
	return pt{m.a*p.X + m.c*p.Y + m.e, m.b*p.X + m.d*p.Y + m.f}
}

// transformed returns a copy of p with every point mapped through m.
func (p *path) transformed(m affine) *path {
	q := &path{ops: make([]op, len(p.ops))}
	for i, o := range p.ops {
		q.ops[i].kind = o.kind
		for j := range o.pts {
			q.ops[i].pts[j] = m.apply(o.pts[j])
		}
	}
	return q
}
