// Package kb loads phonehome's knowledge base and answers the one question the
// rest of the program keeps asking: what is this domain for, and who runs it?
//
// The data is YAML under companies/, domains/ and fixes/ (see kb/README.md for
// the schema and contribution rules). Domain rules are suffix patterns
// matched on label boundaries; a leading "=" restricts a pattern to exact
// matches, and the longest matching pattern wins.
package kb

import (
	"cmp"
	"errors"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/bizpers11991-code/phonehome/internal/model"
	kbdata "github.com/bizpers11991-code/phonehome/kb"
)

// KB is a loaded, immutable knowledge base. It is safe for concurrent use.
//
// Values it hands out (Company pointers, Evidence slices, Fix fields) are
// shared with the KB and must be treated as read-only.
type KB struct {
	companies map[string]*model.Company
	exact     map[string]*model.Classification // "=" patterns, keyed without the "="
	suffix    map[string]*model.Classification // keyed by pattern
	fixes     []model.Fix                      // sorted by ID
	stats     Stats
}

// Stats summarises the size of a knowledge base.
type Stats struct {
	Rules      int
	ByCategory map[model.Category]int
	Companies  int
	Fixes      int
}

// Load reads companies/, domains/ and fixes/ from fsys. Missing directories
// are treated as empty and files without a .yaml or .yml extension are
// ignored.
//
// Load fails only on structural problems that would make the data
// meaningless: unparseable YAML, unknown fields, unknown categories and
// references to companies that do not exist. The returned error joins every
// such problem, each prefixed with its file and rule. Use Lint for the full,
// stricter set of checks.
func Load(fsys fs.FS) (*KB, error) {
	k, probs := parse(fsys)
	var errs []error
	for _, p := range probs {
		if p.fatal {
			errs = append(errs, p)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return k, nil
}

// Lint returns every problem found in the knowledge base in fsys, structural
// or editorial, in a stable order. It returns nil when the data is clean.
func Lint(fsys fs.FS) []error {
	_, probs := parse(fsys)
	var errs []error
	for _, p := range probs {
		errs = append(errs, p)
	}
	return errs
}

var (
	defaultOnce sync.Once
	defaultKB   *KB
)

// Default returns the knowledge base embedded in the binary. The embedded data
// is validated by tests, so a load failure is a build defect and panics.
func Default() *KB {
	defaultOnce.Do(func() {
		k, err := Load(kbdata.FS)
		if err != nil {
			panic("kb: embedded knowledge base is invalid: " + err.Error())
		}
		defaultKB = k
	})
	return defaultKB
}

// Classify reports what the knowledge base knows about domain. The input is
// normalised (trimmed, lowercased, trailing dot removed) and the result's
// Domain is that normalised name. Domains no rule matches get CatUnknown.
//
// Matching costs one map lookup per label of domain.
func (k *KB) Classify(domain string) model.Classification {
	d := normalize(domain)
	if c, ok := k.exact[d]; ok {
		return withDomain(c, d)
	}
	for s := d; s != ""; {
		if c, ok := k.suffix[s]; ok {
			return withDomain(c, d)
		}
		i := strings.IndexByte(s, '.')
		if i < 0 {
			break
		}
		s = s[i+1:]
	}
	return model.Classification{Domain: d, Category: model.CatUnknown}
}

func withDomain(c *model.Classification, domain string) model.Classification {
	r := *c
	r.Domain = domain
	return r
}

// Company returns the company with the given ID, or nil if there is none.
func (k *KB) Company(id string) *model.Company {
	return k.companies[id]
}

// Fixes returns every fix, sorted by ID.
func (k *KB) Fixes() []model.Fix {
	return slices.Clone(k.fixes)
}

// FixesFor returns the fixes that apply to d, most direct remedy first.
// companyIDs are the companies d talked to, most traffic first. Fix.When is
// left to the caller, which knows the findings.
//
// A fix must allow d.Kind (empty Kinds allows any). Brand-specific fixes are
// then matched, case-insensitively, against who the device is (its Vendor,
// Hostname or Label) and against the platform it mostly talks to
// (companyIDs[0]) — so a Roku TV built by TCL gets Roku's fixes, but a Samsung
// TV that merely fetches Google ads does not get Google TV's. Order: identity
// matches, then platform matches, then generic fixes (no Vendors), each by ID.
func (k *KB) FixesFor(d model.Device, companyIDs []string) []model.Fix {
	var identity []string
	for _, s := range []string{d.Vendor, d.Hostname, d.Label} {
		if s != "" {
			identity = append(identity, strings.ToLower(s))
		}
	}
	var platform []string
	if len(companyIDs) > 0 {
		platform = []string{strings.ToLower(companyIDs[0])}
	}

	const (
		byIdentity = iota
		byPlatform
		generic
	)
	type ranked struct {
		fix  model.Fix
		rank int
	}
	var hits []ranked
	for _, f := range k.fixes {
		if len(f.Kinds) > 0 && !slices.Contains(f.Kinds, d.Kind) {
			continue
		}
		switch {
		case len(f.Vendors) == 0:
			hits = append(hits, ranked{f, generic})
		case anyContains(identity, f.Vendors):
			hits = append(hits, ranked{f, byIdentity})
		case anyContains(platform, f.Vendors):
			hits = append(hits, ranked{f, byPlatform})
		}
	}
	slices.SortStableFunc(hits, func(a, b ranked) int { return cmp.Compare(a.rank, b.rank) })
	out := make([]model.Fix, len(hits))
	for i, h := range hits {
		out[i] = h.fix
	}
	return out
}

// anyContains reports whether any needle is a substring of any haystack entry.
// Needles are already lowercase.
func anyContains(haystack, needles []string) bool {
	for _, h := range haystack {
		for _, n := range needles {
			if strings.Contains(h, n) {
				return true
			}
		}
	}
	return false
}

// Stats returns rule, company and fix counts.
func (k *KB) Stats() Stats {
	s := k.stats
	s.ByCategory = maps.Clone(s.ByCategory)
	return s
}

// normalize canonicalises a domain name for lookup. It does not allocate for
// names that are already canonical.
func normalize(domain string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
}
