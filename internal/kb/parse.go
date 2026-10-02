package kb

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// maxPurpose is the longest a purpose sentence may be, in characters. It has
// to fit on one or two lines of a receipt.
const maxPurpose = 160

// YAML shapes. Unknown fields are rejected so that a typo such as "catgory"
// fails loudly instead of being silently dropped.

type companiesFile struct {
	Companies []companyEntry `yaml:"companies"`
}

type companyEntry struct {
	ID      string `yaml:"id"`
	Name    string `yaml:"name"`
	Country string `yaml:"country"`
	URL     string `yaml:"url"`
}

type domainsFile struct {
	Company string      `yaml:"company"`
	Rules   []ruleEntry `yaml:"rules"`
}

type ruleEntry struct {
	Pattern    string   `yaml:"pattern"`
	Company    string   `yaml:"company"`
	Category   string   `yaml:"category"`
	Purpose    string   `yaml:"purpose"`
	Confidence string   `yaml:"confidence"`
	KindHint   string   `yaml:"kind_hint"`
	Evidence   []string `yaml:"evidence"`
}

type fixEntry struct {
	ID       string   `yaml:"id"`
	Title    string   `yaml:"title"`
	Vendors  []string `yaml:"vendors"`
	Kinds    []string `yaml:"kinds"`
	When     []string `yaml:"when"`
	Steps    []string `yaml:"steps"`
	Notes    string   `yaml:"notes"`
	Evidence []string `yaml:"evidence"`
}

// problem is one thing wrong with the knowledge base. Fatal problems make
// Load fail; the rest are reported only by Lint.
type problem struct {
	file  string // path within the FS
	item  string // e.g. `rules[3] "ads.example.com"`; "" for file-level problems
	msg   string
	fatal bool
}

func (p problem) Error() string {
	if p.item == "" {
		return p.file + ": " + p.msg
	}
	return p.file + ": " + p.item + ": " + p.msg
}

// loader accumulates the KB and its problems across files.
type loader struct {
	fsys  fs.FS
	kb    *KB
	probs []problem

	companyFile map[string]string // company ID → file that defined it
	patternFile map[string]string // raw pattern → file that defined it
	fixFile     map[string]string // fix ID → file that defined it
}

// parse reads everything in fsys. It always returns a usable KB built from
// the entries that could be read, along with every problem found.
func parse(fsys fs.FS) (*KB, []problem) {
	l := &loader{
		fsys: fsys,
		kb: &KB{
			companies: map[string]*model.Company{},
			exact:     map[string]*model.Classification{},
			suffix:    map[string]*model.Classification{},
			stats:     Stats{ByCategory: map[model.Category]int{}},
		},
		companyFile: map[string]string{},
		patternFile: map[string]string{},
		fixFile:     map[string]string{},
	}
	// Companies first: domain rules refer to them.
	l.each("companies", l.companies)
	l.each("domains", l.domains)
	l.each("fixes", l.fix)

	slices.SortStableFunc(l.kb.fixes, func(a, b model.Fix) int { return strings.Compare(a.ID, b.ID) })
	l.kb.stats.Companies = len(l.kb.companies)
	l.kb.stats.Fixes = len(l.kb.fixes)
	return l.kb, l.probs
}

func (l *loader) add(file, item string, fatal bool, format string, args ...any) {
	l.probs = append(l.probs, problem{file: file, item: item, msg: fmt.Sprintf(format, args...), fatal: fatal})
}

// each calls fn for every YAML file under dir, in lexical order.
func (l *loader) each(dir string, fn func(file string, data []byte)) {
	err := fs.WalkDir(l.fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir && errors.Is(err, fs.ErrNotExist) {
				return fs.SkipDir
			}
			return err
		}
		if d.IsDir() || !isYAML(p) {
			return nil
		}
		data, err := fs.ReadFile(l.fsys, p)
		if err != nil {
			return err
		}
		fn(p, data)
		return nil
	})
	if err != nil {
		l.add(dir, "", true, "%v", err)
	}
}

func isYAML(p string) bool {
	ext := path.Ext(p)
	return ext == ".yaml" || ext == ".yml"
}

// decode strictly unmarshals one YAML document. An empty file is not an error.
func (l *loader) decode(file string, data []byte, v any) bool {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		l.add(file, "", true, "invalid YAML: %v", err)
		return false
	}
	return true
}

func (l *loader) companies(file string, data []byte) {
	var f companiesFile
	if !l.decode(file, data, &f) {
		return
	}
	for i, c := range f.Companies {
		item := fmt.Sprintf("companies[%d] %q", i, c.ID)
		if !isSlug(c.ID) {
			l.add(file, item, false, "id must be a lowercase slug (a-z, 0-9, -)")
		}
		if strings.TrimSpace(c.Name) == "" {
			l.add(file, item, false, "name is required")
		}
		if c.Country != "" && !isCountry(c.Country) {
			l.add(file, item, false, "country %q is not an ISO 3166-1 alpha-2 code like \"US\"", c.Country)
		}
		if c.URL != "" && !isHTTPS(c.URL) {
			l.add(file, item, false, "url %q is not an https URL", c.URL)
		}
		if prev, dup := l.companyFile[c.ID]; dup {
			l.add(file, item, false, "duplicate company id, first defined in %s", prev)
			continue
		}
		l.companyFile[c.ID] = file
		l.kb.companies[c.ID] = &model.Company{ID: c.ID, Name: c.Name, Country: c.Country, URL: c.URL}
	}
}

func (l *loader) domains(file string, data []byte) {
	var f domainsFile
	if !l.decode(file, data, &f) {
		return
	}
	if f.Company != "" && l.kb.companies[f.Company] == nil {
		l.add(file, "", true, "unknown company %q", f.Company)
	}
	for i, r := range f.Rules {
		item := fmt.Sprintf("rules[%d] %q", i, r.Pattern)
		ok := true
		fail := func(format string, args ...any) {
			l.add(file, item, true, format, args...)
			ok = false
		}
		warn := func(format string, args ...any) { l.add(file, item, false, format, args...) }

		cat := model.Category(r.Category)
		if !cat.Valid() || cat == model.CatUnknown {
			fail("unknown category %q (want one of %s)", r.Category, categoryList())
		}
		companyID := r.Company
		if companyID == "" {
			companyID = f.Company
		}
		company := l.kb.companies[companyID]
		if r.Company != "" && company == nil {
			fail("unknown company %q", r.Company)
		}

		host, exact := strings.CutPrefix(r.Pattern, "=")
		if !isHostname(host) {
			warn("pattern must be a lowercase hostname with at least two labels, optionally prefixed with \"=\"")
			// Still load patterns that are merely untidy ("Example.COM."),
			// but never ones that could match far too much ("com").
			if host = normalize(host); !isHostname(host) {
				ok = false
			}
		}
		pattern := host
		if exact {
			pattern = "=" + host
		}
		if prev, dup := l.patternFile[pattern]; dup {
			warn("duplicate pattern, first defined in %s", prev)
			ok = false
		}

		if strings.TrimSpace(r.Purpose) == "" {
			warn("purpose is required")
		} else {
			if n := utf8.RuneCountInString(r.Purpose); n > maxPurpose {
				warn("purpose is %d characters, the limit is %d", n, maxPurpose)
			}
			if !strings.HasSuffix(r.Purpose, ".") {
				warn("purpose must be a sentence ending in a period")
			}
		}
		switch r.Confidence {
		case "high", "medium", "low":
		default:
			warn("confidence %q must be high, medium or low", r.Confidence)
		}
		if r.KindHint != "" && !isKind(r.KindHint) {
			warn("kind_hint %q is not a device kind (want one of %s)", r.KindHint, kindList())
		}
		l.checkEvidence(file, item, r.Evidence)

		if !ok {
			continue
		}
		l.patternFile[pattern] = file
		c := &model.Classification{
			Rule:       pattern,
			Category:   cat,
			Company:    company,
			Purpose:    r.Purpose,
			Confidence: r.Confidence,
			Evidence:   slices.Clip(r.Evidence),
			KindHint:   model.DeviceKind(r.KindHint),
		}
		if exact {
			l.kb.exact[host] = c
		} else {
			l.kb.suffix[host] = c
		}
		l.kb.stats.Rules++
		l.kb.stats.ByCategory[cat]++
	}
}

func (l *loader) fix(file string, data []byte) {
	var f fixEntry
	if !l.decode(file, data, &f) {
		return
	}
	item := fmt.Sprintf("fix %q", f.ID)
	warn := func(format string, args ...any) { l.add(file, item, false, format, args...) }

	if !isSlug(f.ID) {
		warn("id must be a lowercase slug (a-z, 0-9, -)")
	} else if base := strings.TrimSuffix(path.Base(file), path.Ext(file)); base != f.ID {
		warn("file name should be %s.yaml to match the id", f.ID)
	}
	if strings.TrimSpace(f.Title) == "" {
		warn("title is required")
	}
	if len(f.Steps) == 0 {
		warn("at least one step is required")
	}
	if len(f.Vendors) == 0 && len(f.Kinds) == 0 {
		warn("vendors and kinds are both empty, so this fix would be shown for every device")
	}
	kinds := make([]model.DeviceKind, 0, len(f.Kinds))
	for _, k := range f.Kinds {
		if !isKind(k) {
			warn("kind %q is not a device kind (want one of %s)", k, kindList())
		}
		kinds = append(kinds, model.DeviceKind(k))
	}
	for _, w := range f.When {
		if w != "bypass" && !model.Category(w).Valid() {
			warn(`when %q is not a category or "bypass"`, w)
		}
	}
	vendors := make([]string, 0, len(f.Vendors))
	for _, v := range f.Vendors {
		if v = strings.ToLower(strings.TrimSpace(v)); v == "" {
			warn("empty vendor")
			continue
		}
		vendors = append(vendors, v)
	}
	l.checkEvidence(file, item, f.Evidence)

	if prev, dup := l.fixFile[f.ID]; dup {
		warn("duplicate fix id, first defined in %s", prev)
		return
	}
	l.fixFile[f.ID] = file
	l.kb.fixes = append(l.kb.fixes, model.Fix{
		ID:       f.ID,
		Title:    f.Title,
		Vendors:  vendors,
		Kinds:    kinds,
		When:     f.When,
		Steps:    f.Steps,
		Notes:    f.Notes,
		Evidence: f.Evidence,
	})
}

func (l *loader) checkEvidence(file, item string, urls []string) {
	if len(urls) == 0 {
		l.add(file, item, false, "at least one evidence URL is required")
	}
	for _, u := range urls {
		if !isHTTPS(u) {
			l.add(file, item, false, "evidence %q is not an https URL", u)
		}
	}
}

func isHTTPS(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != ""
}

// isHostname reports whether s is a canonical (lowercase, no trailing dot)
// DNS name of at least two labels. Underscores are allowed because real
// lookups contain them (e.g. _dns.resolver.arpa).
func isHostname(s string) bool {
	if len(s) > 253 {
		return false
	}
	labels := strings.Split(s, ".")
	if len(labels) < 2 {
		return false
	}
	for _, lb := range labels {
		if lb == "" || len(lb) > 63 || lb[0] == '-' || lb[len(lb)-1] == '-' {
			return false
		}
		for _, r := range lb {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
				return false
			}
		}
	}
	return true
}

func isSlug(s string) bool {
	if s == "" || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

func isCountry(s string) bool {
	return len(s) == 2 && s[0] >= 'A' && s[0] <= 'Z' && s[1] >= 'A' && s[1] <= 'Z'
}

// kinds are the device kinds a rule or fix may name. KindUnknown is excluded:
// hinting or targeting "unknown" says nothing.
var kinds = []model.DeviceKind{
	model.KindTV, model.KindStreamer, model.KindSpeaker, model.KindCamera,
	model.KindVacuum, model.KindPlug, model.KindHub, model.KindAppliance,
	model.KindConsole, model.KindPhone, model.KindComputer, model.KindNetwork,
}

func isKind(s string) bool { return slices.Contains(kinds, model.DeviceKind(s)) }

func kindList() string {
	s := make([]string, len(kinds))
	for i, k := range kinds {
		s[i] = string(k)
	}
	return strings.Join(s, ", ")
}

func categoryList() string {
	var s []string
	for _, c := range model.Categories() {
		if c != model.CatUnknown {
			s = append(s, string(c))
		}
	}
	return strings.Join(s, ", ")
}
