package receipt

import (
	"bytes"
	"encoding/xml"
	"errors"
	"flag"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// go test ./internal/receipt -update rewrites the sample images in testdata/samples;
// -svgdir also writes the SVGs somewhere for side-by-side review.
var (
	update = flag.Bool("update", false, "rewrite testdata/samples/receipt-*.png")
	svgDir = flag.String("svgdir", "", "also write sample SVGs to this directory")
)

func samples() map[string]Doc {
	o := Options{Demo: true, Now: printed}
	return map[string]Doc{
		"device-demo":  Device(samsungTV(), o),
		"device-a":     Device(hueBridge(), o),
		"device-long":  Device(longName(), o),
		"home-demo":    Home(home(), o),
		"device-since": Device(fixedTV(), o),
		"home-since":   Home(fixedHome(), o),
	}
}

func TestSamples(t *testing.T) {
	for name, d := range samples() {
		t.Run(name, func(t *testing.T) {
			p, err := d.PNG()
			if err != nil {
				t.Fatal(err)
			}
			img, err := png.Decode(bytes.NewReader(p))
			if err != nil {
				t.Fatal(err)
			}
			if got := img.Bounds().Dx(); got != DefaultWidth {
				t.Errorf("PNG width = %d, want %d", got, DefaultWidth)
			}
			s := d.SVG()
			wellFormed(t, s)
			if len(s) > 300<<10 {
				t.Errorf("SVG is %d KB, want < 300 KB", len(s)>>10)
			}
			if *update {
				if err := os.WriteFile(filepath.Join("testdata", "samples", "receipt-"+name+".png"), p, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if *svgDir != "" {
				if err := os.WriteFile(filepath.Join(*svgDir, "receipt-"+name+".svg"), s, 0o644); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func wellFormed(t *testing.T, s []byte) {
	t.Helper()
	dec := xml.NewDecoder(bytes.NewReader(s))
	for {
		_, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatalf("SVG is not well-formed XML: %v", err)
		}
	}
}

func TestDeterministic(t *testing.T) {
	o := Options{Now: printed}
	a, b := Device(samsungTV(), o), Device(samsungTV(), o)
	if !bytes.Equal(a.SVG(), b.SVG()) {
		t.Error("SVG differs between identical inputs")
	}
	pa, _ := a.PNG()
	pb, _ := b.PNG()
	if !bytes.Equal(pa, pb) {
		t.Error("PNG differs between identical inputs")
	}
	h1, h2 := Home(home(), o), Home(home(), o)
	if !bytes.Equal(h1.SVG(), h2.SVG()) {
		t.Error("home SVG differs between identical inputs")
	}
}

func TestWidthScales(t *testing.T) {
	d := Device(hueBridge(), Options{Now: printed, Width: 1152})
	p, err := d.PNG()
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(p))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 1152 {
		t.Errorf("width = %d, want 1152", img.Bounds().Dx())
	}
}
