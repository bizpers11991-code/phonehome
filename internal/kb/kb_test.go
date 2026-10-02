package kb

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/bizpers11991-code/phonehome/internal/model"
	kbdata "github.com/bizpers11991-code/phonehome/kb"
)

const companiesYAML = `
companies:
  - id: acme
    name: Acme Corp
    country: US
    url: https://acme.example
  - id: adco
    name: AdCo
    country: DE
`

const domainsYAML = `
company: acme
rules:
  - pattern: example.com
    category: content
    purpose: Serves the Acme app.
    confidence: medium
    evidence: [https://acme.example/docs]
  - pattern: ads.example.com
    company: adco
    category: ads
    purpose: Serves and measures adverts.
    confidence: high
    kind_hint: tv
    evidence: [https://paper.example/ads]
  - pattern: =time.example.com
    category: essential
    purpose: Keeps the device clock in sync.
    confidence: high
    evidence: [https://acme.example/ntp]
  - pattern: acr.tv.example.com
    category: acr
    purpose: Identifies what is on screen.
    confidence: low
    kind_hint: tv
    evidence: [https://forum.example/thread]
`

func fixture() fstest.MapFS {
	return fstest.MapFS{
		"companies/main.yaml": {Data: []byte(companiesYAML)},
		"companies/README.md": {Data: []byte("not yaml: [")},
		"domains/acme.yml":    {Data: []byte(domainsYAML)},
		"fixes/acme-tv-acr.yaml": {Data: []byte(`
id: acme-tv-acr
title: Turn off Acme content recognition
vendors: [acme]
kinds: [tv]
steps: [Settings › Privacy › Off]
evidence: [https://acme.example/privacy]
`)},
		"fixes/any-adco.yaml": {Data: []byte(`
id: any-adco
title: Opt out of AdCo
vendors: [AdCo]
steps: [Visit the opt-out page]
evidence: [https://adco.example/optout]
`)},
		"fixes/all-cameras.yaml": {Data: []byte(`
id: all-cameras
title: Put cameras on their own network
kinds: [camera]
steps: [Create a guest network]
evidence: [https://guide.example/vlan]
`)},
	}
}

func mustLoad(t testing.TB, fsys fstest.MapFS) *KB {
	t.Helper()
	k, err := Load(fsys)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return k
}

func TestClassify(t *testing.T) {
	k := mustLoad(t, fixture())
	tests := []struct {
		in       string
		domain   string
		rule     string
		category model.Category
		company  string
	}{
		{"example.com", "example.com", "example.com", model.CatContent, "acme"},
		{"api.example.com", "api.example.com", "example.com", model.CatContent, "acme"},
		{"a.b.c.example.com", "a.b.c.example.com", "example.com", model.CatContent, "acme"},
		{"badexample.com", "badexample.com", "", model.CatUnknown, ""},
		{"example.com.evil.net", "example.com.evil.net", "", model.CatUnknown, ""},
		{"ads.example.com", "ads.example.com", "ads.example.com", model.CatAds, "adco"},
		{"x.ads.example.com", "x.ads.example.com", "ads.example.com", model.CatAds, "adco"},
		{"badads.example.com", "badads.example.com", "example.com", model.CatContent, "acme"},
		{"time.example.com", "time.example.com", "=time.example.com", model.CatEssential, "acme"},
		{"eu.time.example.com", "eu.time.example.com", "example.com", model.CatContent, "acme"},
		{"x.acr.tv.example.com", "x.acr.tv.example.com", "acr.tv.example.com", model.CatACR, "acme"},
		{"tv.example.com", "tv.example.com", "example.com", model.CatContent, "acme"},
		{"ADS.Example.COM", "ads.example.com", "ads.example.com", model.CatAds, "adco"},
		{"ads.example.com.", "ads.example.com", "ads.example.com", model.CatAds, "adco"},
		{"  Time.Example.com. \n", "time.example.com", "=time.example.com", model.CatEssential, "acme"},
		{"com", "com", "", model.CatUnknown, ""},
		{"", "", "", model.CatUnknown, ""},
		{".", "", "", model.CatUnknown, ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			c := k.Classify(tt.in)
			if c.Domain != tt.domain || c.Rule != tt.rule || c.Category != tt.category {
				t.Errorf("Classify(%q) = {Domain:%q Rule:%q Category:%q}, want {%q %q %q}",
					tt.in, c.Domain, c.Rule, c.Category, tt.domain, tt.rule, tt.category)
			}
			var got string
			if c.Company != nil {
				got = c.Company.ID
			}
			if got != tt.company {
				t.Errorf("Classify(%q).Company = %q, want %q", tt.in, got, tt.company)
			}
		})
	}
}

func TestClassifyFields(t *testing.T) {
	k := mustLoad(t, fixture())
	c := k.Classify("x.ads.example.com")
	want := model.Classification{
		Domain:     "x.ads.example.com",
		Rule:       "ads.example.com",
		Category:   model.CatAds,
		Company:    k.Company("adco"),
		Purpose:    "Serves and measures adverts.",
		Confidence: "high",
		Evidence:   []string{"https://paper.example/ads"},
		KindHint:   model.KindTV,
	}
	if c.Domain != want.Domain || c.Rule != want.Rule || c.Category != want.Category ||
		c.Company != want.Company || c.Purpose != want.Purpose || c.Confidence != want.Confidence ||
		!slices.Equal(c.Evidence, want.Evidence) || c.KindHint != want.KindHint {
		t.Errorf("Classify = %+v\nwant       %+v", c, want)
	}
	if c.Company == nil || c.Company.Name != "AdCo" || c.Company.Country != "DE" {
		t.Errorf("Company = %+v, want AdCo/DE", c.Company)
	}

	u := k.Classify("unknown.test")
	if u.Category != model.CatUnknown || u.Company != nil || u.Rule != "" || u.Confidence != "" {
		t.Errorf("unknown domain = %+v", u)
	}
}

func TestEmptyKB(t *testing.T) {
	k := mustLoad(t, fstest.MapFS{})
	if c := k.Classify("example.com"); c.Category != model.CatUnknown || c.Domain != "example.com" {
		t.Errorf("Classify on empty KB = %+v", c)
	}
	if got := k.FixesFor(model.Device{Vendor: "acme"}, nil); len(got) != 0 {
		t.Errorf("FixesFor on empty KB = %v", got)
	}
	if s := k.Stats(); s.Rules != 0 || s.Companies != 0 || s.Fixes != 0 {
		t.Errorf("Stats on empty KB = %+v", s)
	}
}

func TestCompany(t *testing.T) {
	k := mustLoad(t, fixture())
	if c := k.Company("acme"); c == nil || c.Name != "Acme Corp" || c.URL != "https://acme.example" {
		t.Errorf("Company(acme) = %+v", c)
	}
	if c := k.Company("nope"); c != nil {
		t.Errorf("Company(nope) = %+v, want nil", c)
	}
}

func TestFixes(t *testing.T) {
	k := mustLoad(t, fixture())
	if got, want := fixIDs(k.Fixes()), []string{"acme-tv-acr", "all-cameras", "any-adco"}; !slices.Equal(got, want) {
		t.Errorf("Fixes() = %v, want %v", got, want)
	}
}

func TestFixesFor(t *testing.T) {
	k := mustLoad(t, fixture())
	tests := []struct {
		name      string
		dev       model.Device
		companies []string
		want      []string
	}{
		{"vendor and kind", model.Device{Vendor: "ACME Electronics", Kind: model.KindTV}, nil, []string{"acme-tv-acr"}},
		{"vendor, wrong kind", model.Device{Vendor: "Acme", Kind: model.KindSpeaker}, nil, nil},
		{"hostname", model.Device{Hostname: "living-room-acme-tv", Kind: model.KindTV}, nil, []string{"acme-tv-acr"}},
		{"label", model.Device{Label: "My Acme", Kind: model.KindTV}, nil, []string{"acme-tv-acr"}},
		{"company seen", model.Device{Kind: model.KindTV}, []string{"acme"}, []string{"acme-tv-acr"}},
		{"vendor list is case-insensitive", model.Device{Kind: model.KindPlug}, []string{"adco"}, []string{"any-adco"}},
		{"kind only", model.Device{Kind: model.KindCamera}, nil, []string{"all-cameras"}},
		{"several, sorted", model.Device{Vendor: "acme", Kind: model.KindTV}, []string{"adco"}, []string{"acme-tv-acr", "any-adco"}},
		// A Samsung TV that fetches Google ads must not be told to fix Google TV.
		{"minor company ignored", model.Device{Vendor: "acme", Kind: model.KindTV}, []string{"acme", "adco"}, []string{"acme-tv-acr"}},
		{"nothing known", model.Device{}, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fixIDs(k.FixesFor(tt.dev, tt.companies)); !slices.Equal(got, tt.want) {
				t.Errorf("FixesFor = %v, want %v", got, tt.want)
			}
		})
	}
}

func fixIDs(fs []model.Fix) []string {
	var ids []string
	for _, f := range fs {
		ids = append(ids, f.ID)
	}
	return ids
}

func TestStats(t *testing.T) {
	s := mustLoad(t, fixture()).Stats()
	if s.Rules != 4 || s.Companies != 2 || s.Fixes != 3 {
		t.Errorf("Stats = %+v, want 4 rules, 2 companies, 3 fixes", s)
	}
	want := map[model.Category]int{model.CatContent: 1, model.CatAds: 1, model.CatEssential: 1, model.CatACR: 1}
	for c, n := range want {
		if s.ByCategory[c] != n {
			t.Errorf("ByCategory[%s] = %d, want %d", c, s.ByCategory[c], n)
		}
	}
	s.ByCategory[model.CatAds] = 99 // must not leak into the KB
	if mustLoad(t, fixture()).Stats().ByCategory[model.CatAds] != 1 {
		t.Error("Stats().ByCategory aliases internal state")
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  []string // substrings of the error
	}{
		{
			"bad yaml",
			map[string]string{"domains/x.yaml": "rules: [\n"},
			[]string{"domains/x.yaml: invalid YAML"},
		},
		{
			"unknown field",
			map[string]string{"domains/x.yaml": "rules:\n  - pattern: a.com\n    catgory: ads\n"},
			[]string{"domains/x.yaml: invalid YAML", "catgory"},
		},
		{
			"unknown category",
			map[string]string{"domains/x.yaml": "rules:\n  - pattern: a.com\n    category: spying\n"},
			[]string{`domains/x.yaml: rules[0] "a.com": unknown category "spying"`},
		},
		{
			"missing category",
			map[string]string{"domains/x.yaml": "rules:\n  - pattern: a.com\n"},
			[]string{`rules[0] "a.com": unknown category ""`},
		},
		{
			"dangling rule company",
			map[string]string{"domains/x.yaml": "rules:\n  - pattern: a.com\n    company: ghost\n    category: ads\n"},
			[]string{`domains/x.yaml: rules[0] "a.com": unknown company "ghost"`},
		},
		{
			"dangling file company",
			map[string]string{"domains/x.yaml": "company: ghost\nrules: []\n"},
			[]string{`domains/x.yaml: unknown company "ghost"`},
		},
		{
			"every problem is reported",
			map[string]string{
				"companies/c.yaml": "companies: {}\n",
				"domains/x.yaml":   "rules:\n  - pattern: a.com\n    category: nope\n  - pattern: b.com\n    category: nah\n",
			},
			[]string{"companies/c.yaml: invalid YAML", `rules[0] "a.com"`, `rules[1] "b.com"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := fstest.MapFS{}
			for name, data := range tt.files {
				fsys[name] = &fstest.MapFile{Data: []byte(data)}
			}
			k, err := Load(fsys)
			if err == nil {
				t.Fatal("Load succeeded, want error")
			}
			if k != nil {
				t.Error("Load returned a KB alongside an error")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not mention %q", err, w)
				}
			}
		})
	}
}

// Editorial problems are Lint's job; Load must still accept the data.
func TestLoadToleratesLintProblems(t *testing.T) {
	k := mustLoad(t, fstest.MapFS{
		"domains/x.yaml": {Data: []byte("rules:\n  - pattern: Example.COM.\n    category: ads\n")},
	})
	if c := k.Classify("www.example.com"); c.Category != model.CatAds || c.Rule != "example.com" {
		t.Errorf("untidy pattern not loaded: %+v", c)
	}
}

func TestDefault(t *testing.T) {
	k := Default()
	if k != Default() {
		t.Error("Default() returned different instances")
	}
	s := k.Stats()
	if s.Rules == 0 || s.Companies == 0 {
		t.Errorf("embedded KB looks empty: %+v", s)
	}
}

func TestEmbeddedDataIsLintClean(t *testing.T) {
	for _, err := range Lint(kbdata.FS) {
		t.Error(err)
	}
}

func BenchmarkClassify(b *testing.B) {
	k := mustLoad(b, fixture())
	domains := []string{
		"x.acr.tv.example.com",            // deep match
		"ads.example.com",                 // direct match
		"time.example.com",                // exact match
		"cdn.unknown.example.org",         // miss
		"a.b.c.d.e.f.g.h.unknown.invalid", // long miss
	}
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		k.Classify(domains[i%len(domains)])
	}
}
