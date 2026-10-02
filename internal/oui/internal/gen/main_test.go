package main

import (
	"os"
	"testing"
)

func TestBrandFor(t *testing.T) {
	tests := map[string]string{
		"Samsung Electronics Co.,Ltd":          "Samsung",
		"SAMSUNG ELECTRO-MECHANICS(THAILAND)":  "Samsung",
		"Hon Hai Precision Ind. Co.,Ltd.":      "Foxconn",
		"Sony Interactive Entertainment Inc.":  "PlayStation",
		"Sony Corporation":                     "Sony",
		"Apple, Inc.":                          "Apple",
		"Nest Labs Inc.":                       "Google Nest",
		"Google, Inc.":                         "Google",
		"Dell Inc.":                            "Dell",
		"Italdata Ingegneria dell'Idea S.p.A.": "", // "dell" must be a whole name
		"Hewlett Packard Enterprise":           "", // exact match only
		"Toshiba Global Commerce Solutions":    "",
		"Jiangsu Ruichi Broadcom Communications Technoligy Co., Ltd.": "",
		"Cisco Systems, Inc":          "",
		"IEEE Registration Authority": "",
		"":                            "",
	}
	for org, want := range tests {
		if got := brandFor(org); got != want {
			t.Errorf("brandFor(%q) = %q, want %q", org, got, want)
		}
	}
}

func TestParse(t *testing.T) {
	f, err := os.Open("testdata/oui.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got := map[string]string{}
	err = parse(f, 6, func(prefix, org string) {
		if name := brandFor(org); name != "" {
			got[prefix] = name
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"b827eb": "Raspberry Pi",
		"f0272d": "Amazon",
		"acbc32": "Apple",
		"24a160": "Espressif",
		"ec1bbd": "Silicon Labs",
		"dc0b34": "LG",
		"10f005": "Intel",
		"000d4b": "Roku",
	}
	if len(got) != len(want) {
		t.Errorf("got %d entries %v, want %d", len(got), got, len(want))
	}
	for p, name := range want {
		if got[p] != name {
			t.Errorf("prefix %s = %q, want %q", p, got[p], name)
		}
	}
}

func TestParseRejectsErrorPage(t *testing.T) {
	f, err := os.Open("testdata/rejected.html")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := parse(f, 6, func(string, string) {}); err == nil {
		t.Error("parse accepted an HTML error page")
	}
}
