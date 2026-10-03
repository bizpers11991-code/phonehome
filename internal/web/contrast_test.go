package web

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// palette reads the --name: #rrggbb tokens of app.css's light :root block
// and of its dark-mode override.
func palette(t *testing.T) (light, dark map[string]string) {
	t.Helper()
	b, err := staticFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	i := strings.Index(css, "@media (prefers-color-scheme: dark)")
	if i < 0 {
		t.Fatal("no dark-mode block in app.css")
	}
	tok := regexp.MustCompile(`--([a-z0-9-]+):\s*(#[0-9a-fA-F]{6})\s*;`)
	read := func(s string) map[string]string {
		m := map[string]string{}
		for _, x := range tok.FindAllStringSubmatch(s, -1) {
			if _, ok := m[x[1]]; !ok {
				m[x[1]] = strings.ToLower(x[2])
			}
		}
		return m
	}
	light = read(css[:i])
	dark = read(css[i:])
	for k, v := range light { // tokens the dark block does not override
		if _, ok := dark[k]; !ok {
			dark[k] = v
		}
	}
	return light, dark
}

type rgb [3]float64

func parseHex(t *testing.T, s string) rgb {
	t.Helper()
	var c rgb
	for i := range 3 {
		n, err := strconv.ParseUint(s[1+2*i:3+2*i], 16, 8)
		if err != nil {
			t.Fatalf("bad colour %q", s)
		}
		c[i] = float64(n)
	}
	return c
}

// mix is CSS color-mix(in srgb, a p, b).
func mix(a, b rgb, p float64) rgb {
	var c rgb
	for i := range 3 {
		c[i] = a[i]*p + b[i]*(1-p)
	}
	return c
}

// contrast is the WCAG 2 contrast ratio.
func contrast(a, b rgb) float64 {
	lum := func(c rgb) float64 {
		var l [3]float64
		for i, v := range c {
			v /= 255
			if v <= 0.04045 {
				l[i] = v / 12.92
			} else {
				l[i] = math.Pow((v+0.055)/1.055, 2.4)
			}
		}
		return 0.2126*l[0] + 0.7152*l[1] + 0.0722*l[2]
	}
	x, y := lum(a), lum(b)
	return (math.Max(x, y) + 0.05) / (math.Min(x, y) + 0.05)
}

// TestContrast checks the dashboard's text and focus colours against WCAG
// 2.1 AA in light and dark mode: 4.5:1 for text, 3:1 for large text (the
// grade badges) and for the focus ring.
func TestContrast(t *testing.T) {
	light, dark := palette(t)
	for mode, p := range map[string]map[string]string{"light": light, "dark": dark} {
		c := func(name string) rgb {
			v, ok := p[name]
			if !ok {
				t.Fatalf("%s: no --%s", mode, name)
			}
			return parseHex(t, v)
		}
		gradeText := parseHex(t, "#ffffff") // .grade and .mini-grade text
		if mode == "dark" {
			gradeText = parseHex(t, "#111111")
		}
		check := func(what string, fg, bg rgb, min float64) {
			if r := contrast(fg, bg); r < min {
				t.Errorf("%s: %s contrast %.2f:1, want at least %.1f:1", mode, what, r, min)
			}
		}
		for _, bg := range []string{"bg", "surface", "sunken"} {
			for _, fg := range []string{"ink", "ink-2", "ink-3"} {
				check(fmt.Sprintf("--%s on --%s", fg, bg), c(fg), c(bg), 4.5)
			}
			check("focus ring on --"+bg, c("focus"), c(bg), 3)
		}
		for _, bg := range []string{"bg", "surface"} {
			check("--better on --"+bg, c("better"), c(bg), 4.5)
			check("--worse on --"+bg, c("worse"), c(bg), 4.5)
		}
		check("--warn-ink on --warn-bg", c("warn-ink"), c("warn-bg"), 4.5)
		check("primary button text", c("bg"), c("ink"), 4.5)
		for _, cat := range []string{"acr", "ads", "tracking", "telemetry", "essential", "content", "unknown"} {
			// .pill and .chip: --ink on the category colour mixed into the surface.
			check("--ink on the "+cat+" pill", c("ink"), mix(c("c-"+cat), c("surface"), 0.13), 4.5)
		}
		for _, g := range []string{"a", "b", "c", "d", "f"} {
			check("text on the mini grade "+g, gradeText, c("g-"+g), 4.5)
		}
	}
}
