package receipt

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"image/color"
	"math"
	"strconv"
	"strings"
)

// SVG renders the receipt as a self-contained SVG. Text is drawn from Go
// Mono glyph outlines (each glyph defined once and reused), so the grid
// looks the same in every browser whether or not the font is installed.
// The plain text of the receipt is kept in <desc> for screen readers.
func (d Doc) SVG() []byte {
	sc := d.scene()
	var b bytes.Buffer
	w, h := num(sc.w), num(sc.h)
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="%s" height="%s" viewBox="0 0 %s %s" role="img" aria-labelledby="t d">`+"\n", w, h, w, h)
	b.WriteString(`<title id="t">`)
	esc(&b, d.Title)
	b.WriteString("</title>\n<desc id=\"d\">")
	esc(&b, d.plain())
	b.WriteString("</desc>\n<defs>\n")
	defined := map[string]bool{}
	for _, l := range sc.layers {
		for _, run := range l.runs {
			for _, r := range run.runes {
				id := glyphID(run.face, r)
				g := run.face.glyph(r)
				if g == nil || defined[id] {
					continue
				}
				defined[id] = true
				fmt.Fprintf(&b, `<path id="%s" d="`, id)
				pathData(&b, g)
				b.WriteString("\"/>\n")
			}
		}
	}
	b.WriteString("</defs>\n")
	for _, l := range sc.layers {
		fmt.Fprintf(&b, `<g fill="%s"`, hex(l.fill))
		if l.fill.A < 0xFF {
			fmt.Fprintf(&b, ` opacity="%s"`, strconv.FormatFloat(float64(l.fill.A)/0xFF, 'f', 3, 64))
		}
		b.WriteString(">\n")
		for _, p := range l.paths {
			b.WriteString(`<path d="`)
			pathData(&b, p)
			b.WriteString("\"/>\n")
		}
		for _, run := range l.runs {
			m := run.m
			fmt.Fprintf(&b, `<g transform="matrix(%s %s %s %s %s %s)">`, num4(m.a), num4(m.b), num4(m.c), num4(m.d), num(m.e), num(m.f))
			for i, r := range run.runes {
				if run.face.glyph(r) == nil {
					continue
				}
				fmt.Fprintf(&b, `<use xlink:href="#%s" x="%s"/>`, glyphID(run.face, r), num(float64(i)*advance))
			}
			b.WriteString("</g>\n")
		}
		b.WriteString("</g>\n")
	}
	b.WriteString("</svg>\n")
	return b.Bytes()
}

// plain is the receipt as plain text, one grid row per line.
func (d Doc) plain() string {
	var lines []string
	for _, l := range d.Lines {
		switch l.Kind {
		case Text:
			lines = append(lines, strings.TrimRight(l.Text, " "))
		case Header:
			lines = append(lines, center(l.Text, Cols))
		case Rule:
			lines = append(lines, strings.Repeat("-", Cols))
		case DoubleRule:
			lines = append(lines, strings.Repeat("=", Cols))
		case Stamp:
			lines = append(lines, append(l.Aside, "GRADE: "+l.Text)...)
		}
	}
	return "\n" + strings.Join(lines, "\n") + "\n"
}

func glyphID(f *face, r rune) string { return f.id + strconv.FormatInt(int64(r), 16) }

func esc(b *bytes.Buffer, s string) { _ = xml.EscapeText(b, []byte(s)) }

func hex(c color.NRGBA) string { return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B) }

// num formats a coordinate with at most two decimals.
func num(f float64) string {
	return strconv.FormatFloat(math.Round(f*100)/100, 'f', -1, 64)
}

// num4 formats a matrix coefficient with at most five decimals.
func num4(f float64) string {
	return strconv.FormatFloat(math.Round(f*1e5)/1e5, 'f', -1, 64)
}

func pathData(b *bytes.Buffer, p *path) {
	for i := range p.ops {
		o := &p.ops[i]
		b.WriteByte(byte(o.kind))
		for j, q := range o.points() {
			if j > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(num(q.X))
			b.WriteByte(' ')
			b.WriteString(num(q.Y))
		}
	}
}
