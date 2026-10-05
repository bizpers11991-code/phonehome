package source

import (
	"bufio"
	"io"
	"strings"
	"testing"
)

func TestDomain(t *testing.T) {
	for in, want := range map[string]string{
		"Example.ORG.":                        "example.org",
		"xn--bcher-kva.example":               "xn--bcher-kva.example",
		"bücher.example":                      "bücher.example",
		"_dns.resolver.arpa":                  "_dns.resolver.arpa",
		"\x1b]0;owned\x07\x1b[2Jevil.example": "",
		"tab\tname.example":                   "",
		"space name.example":                  "",
		"del\x7f.example":                     "",
		"c1\u0085.example":                    "",
		"bad\xffutf8.example":                 "",
		strings.Repeat("a", 253):              strings.Repeat("a", 253),
		strings.Repeat("a", 254):              "",
		"":                                    "",
	} {
		if got := Domain(in); got != want {
			t.Errorf("Domain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReadLine(t *testing.T) {
	long := strings.Repeat("x", MaxLine)
	in := "short\n" + long + "\n" + strings.Repeat("y", MaxLine-1) + "\nlast"
	br := bufio.NewReaderSize(strings.NewReader(in), 16)
	type res struct {
		line string
		null bool
		n    int
		err  error
	}
	var got []res
	for {
		line, n, err := ReadLine(br)
		got = append(got, res{string(line), line == nil, n, err})
		if err != nil {
			break
		}
	}
	want := []res{
		{"short\n", false, 6, nil},
		{"", true, MaxLine + 1, nil}, // too long, skipped but counted
		{strings.Repeat("y", MaxLine-1) + "\n", false, MaxLine, nil},
		{"last", false, 4, io.EOF},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: got %q (nil %t) n=%d err=%v, want n=%d nil %t err=%v",
				i, trim(got[i].line), got[i].null, got[i].n, got[i].err, want[i].n, want[i].null, want[i].err)
		}
	}
}

func trim(s string) string {
	if len(s) > 20 {
		return s[:20] + "…"
	}
	return s
}
