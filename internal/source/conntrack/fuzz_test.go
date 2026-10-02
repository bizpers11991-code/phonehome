package conntrack

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func seedFiles(f *testing.F) [][]byte {
	names, err := filepath.Glob(filepath.Join("testdata", "*"))
	if err != nil {
		f.Fatal(err)
	}
	var out [][]byte
	for _, name := range names {
		b, err := os.ReadFile(name)
		if err != nil {
			f.Fatal(err)
		}
		out = append(out, b)
	}
	return out
}

func FuzzParseEntry(f *testing.F) {
	for _, b := range seedFiles(f) {
		for line := range bytes.Lines(b) {
			f.Add(string(line))
		}
	}
	f.Fuzz(func(t *testing.T, line string) {
		e, ok := parseEntry(line)
		if !ok {
			return
		}
		if (e.orig.proto != "tcp" && e.orig.proto != "udp") || !e.orig.src.IsValid() || !e.orig.dst.IsValid() ||
			e.orig.src.Is4In6() || e.orig.dst.Is4In6() {
			t.Fatalf("parseEntry(%q) = %+v", line, e)
		}
	})
}

// FuzzFetchFlows polls an arbitrary snapshot twice: the first poll reports
// each outbound connection once, the second, identical one reports nothing.
func FuzzFetchFlows(f *testing.F) {
	for _, b := range seedFiles(f) {
		f.Add(b, uint8(3))
	}
	f.Fuzz(func(t *testing.T, snap []byte, limit uint8) {
		tab := New("nf_conntrack", WithClock(fixedClock()), WithReader(func(context.Context) (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(snap)), nil
		}))
		ctx := context.Background()
		var n int
		for range 1 << 16 {
			fl, _, err := tab.FetchFlows(ctx, "", int(limit%5))
			if err != nil {
				return // e.g. a line over the scanner's limit
			}
			if fl == nil {
				break
			}
			if limit%5 > 0 && len(fl) > int(limit%5) {
				t.Fatalf("got %d flows, limit %d", len(fl), limit%5)
			}
			for _, x := range fl {
				if !tab.outbound(tuple{x.Proto, x.ClientIP, x.RemoteIP, 0, x.RemotePort}) {
					t.Fatalf("not outbound: %+v", x)
				}
			}
			n += len(fl)
		}
		if n > bytes.Count(snap, []byte("\n"))+1 {
			t.Fatalf("%d flows from %d lines", n, bytes.Count(snap, []byte("\n"))+1)
		}
		if fl, _, err := tab.FetchFlows(ctx, "", 0); err != nil || fl != nil {
			t.Fatalf("second identical poll returned %v, %v", fl, err)
		}
	})
}
