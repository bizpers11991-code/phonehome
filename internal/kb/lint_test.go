package kb

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
)

func TestLintCleanFixture(t *testing.T) {
	for _, err := range Lint(fixture()) {
		t.Error(err)
	}
}

// validRule is a rule that passes every check; tests break one thing at a time.
const validRule = `
  - pattern: %s
    category: ads
    purpose: Serves adverts.
    confidence: high
    evidence: [https://paper.example/x]
`

func TestLint(t *testing.T) {
	const companies = "companies:\n  - id: acme\n    name: Acme\n"
	tests := []struct {
		name  string
		files map[string]string
		want  string // substring of exactly one reported problem
	}{
		{"duplicate company across files", map[string]string{
			"companies/a.yaml": companies,
			"companies/b.yaml": companies,
		}, `companies/b.yaml: companies[0] "acme": duplicate company id, first defined in companies/a.yaml`},
		{"bad company id", map[string]string{
			"companies/a.yaml": "companies:\n  - id: Acme Inc\n    name: Acme\n",
		}, "id must be a lowercase slug"},
		{"company without name", map[string]string{
			"companies/a.yaml": "companies:\n  - id: acme\n",
		}, "name is required"},
		{"bad country", map[string]string{
			"companies/a.yaml": "companies:\n  - id: acme\n    name: Acme\n    country: usa\n",
		}, `country "usa"`},
		{"unknown company ref", map[string]string{
			"domains/a.yaml": rules("a.com", "    company: ghost\n"),
		}, `unknown company "ghost"`},
		{"invalid category", map[string]string{
			"domains/a.yaml": "rules:\n  - pattern: a.com\n    category: evil\n    purpose: P.\n    confidence: high\n    evidence: [https://x.example]\n",
		}, `unknown category "evil"`},
		{"invalid confidence", map[string]string{
			"domains/a.yaml": "rules:\n  - pattern: a.com\n    category: ads\n    purpose: P.\n    confidence: certain\n    evidence: [https://x.example]\n",
		}, `confidence "certain" must be high, medium or low`},
		{"missing confidence", map[string]string{
			"domains/a.yaml": "rules:\n  - pattern: a.com\n    category: ads\n    purpose: P.\n    evidence: [https://x.example]\n",
		}, `confidence "" must be`},
		{"invalid kind_hint", map[string]string{
			"domains/a.yaml": rules("a.com", "    kind_hint: toaster\n"),
		}, `kind_hint "toaster" is not a device kind`},
		{"missing evidence", map[string]string{
			"domains/a.yaml": "rules:\n  - pattern: a.com\n    category: ads\n    purpose: P.\n    confidence: low\n",
		}, "at least one evidence URL is required"},
		{"non-https evidence", map[string]string{
			"domains/a.yaml": "rules:\n  - pattern: a.com\n    category: ads\n    purpose: P.\n    confidence: low\n    evidence: [http://x.example]\n",
		}, `evidence "http://x.example" is not an https URL`},
		{"empty purpose", map[string]string{
			"domains/a.yaml": "rules:\n  - pattern: a.com\n    category: ads\n    confidence: low\n    evidence: [https://x.example]\n",
		}, "purpose is required"},
		{"long purpose", map[string]string{
			"domains/a.yaml": "rules:\n  - pattern: a.com\n    category: ads\n    purpose: " + strings.Repeat("a", 160) + ".\n    confidence: low\n    evidence: [https://x.example]\n",
		}, "purpose is 161 characters, the limit is 160"},
		{"purpose without period", map[string]string{
			"domains/a.yaml": "rules:\n  - pattern: a.com\n    category: ads\n    purpose: Serves adverts\n    confidence: low\n    evidence: [https://x.example]\n",
		}, "purpose must be a sentence ending in a period"},
		{"uppercase pattern", map[string]string{"domains/a.yaml": rules("Ads.com", "")}, "pattern must be a lowercase hostname"},
		{"trailing dot pattern", map[string]string{"domains/a.yaml": rules("ads.com.", "")}, "pattern must be a lowercase hostname"},
		{"single-label pattern", map[string]string{"domains/a.yaml": rules("com", "")}, "pattern must be a lowercase hostname"},
		{"wildcard pattern", map[string]string{"domains/a.yaml": rules(`"*.ads.com"`, "")}, "pattern must be a lowercase hostname"},
		{"double equals pattern", map[string]string{"domains/a.yaml": rules("==ads.com", "")}, "pattern must be a lowercase hostname"},
		{"duplicate pattern across files", map[string]string{
			"domains/a.yaml": rules("ads.com", ""),
			"domains/b.yaml": rules("ads.com", ""),
		}, `domains/b.yaml: rules[0] "ads.com": duplicate pattern, first defined in domains/a.yaml`},
		{"fix without title", map[string]string{
			"fixes/f.yaml": "id: f\nkinds: [tv]\nsteps: [s]\nevidence: [https://x.example]\n",
		}, "title is required"},
		{"fix without steps", map[string]string{
			"fixes/f.yaml": "id: f\ntitle: T\nkinds: [tv]\nevidence: [https://x.example]\n",
		}, "at least one step is required"},
		{"fix without evidence", map[string]string{
			"fixes/f.yaml": "id: f\ntitle: T\nkinds: [tv]\nsteps: [s]\n",
		}, `fixes/f.yaml: fix "f": at least one evidence URL is required`},
		{"fix for everything", map[string]string{
			"fixes/f.yaml": "id: f\ntitle: T\nsteps: [s]\nevidence: [https://x.example]\n",
		}, "vendors and kinds are both empty"},
		{"fix with invalid kind", map[string]string{
			"fixes/f.yaml": "id: f\ntitle: T\nkinds: [fridge]\nsteps: [s]\nevidence: [https://x.example]\n",
		}, `kind "fridge" is not a device kind`},
		{"fix id does not match file", map[string]string{
			"fixes/g.yaml": "id: f\ntitle: T\nkinds: [tv]\nsteps: [s]\nevidence: [https://x.example]\n",
		}, "file name should be f.yaml"},
		{"duplicate fix id", map[string]string{
			"fixes/f.yaml":     "id: f\ntitle: T\nkinds: [tv]\nsteps: [s]\nevidence: [https://x.example]\n",
			"fixes/sub/f.yaml": "id: f\ntitle: T\nkinds: [tv]\nsteps: [s]\nevidence: [https://x.example]\n",
		}, `fixes/sub/f.yaml: fix "f": duplicate fix id, first defined in fixes/f.yaml`},
		{"bad yaml", map[string]string{"fixes/f.yaml": "id: [\n"}, "fixes/f.yaml: invalid YAML"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := fstest.MapFS{}
			if _, ok := tt.files["companies/a.yaml"]; !ok {
				fsys["companies/base.yaml"] = &fstest.MapFile{Data: []byte(companies)}
			}
			for name, data := range tt.files {
				fsys[name] = &fstest.MapFile{Data: []byte(data)}
			}
			var matches int
			errs := Lint(fsys)
			for _, err := range errs {
				if strings.Contains(err.Error(), tt.want) {
					matches++
				}
			}
			if matches != 1 {
				t.Errorf("want exactly one problem containing %q, got %d in:\n%s", tt.want, matches, joinErrs(errs))
			}
		})
	}
}

// rules returns a domains file with one otherwise-valid rule for pattern,
// plus any extra YAML fields.
func rules(pattern, extra string) string {
	return "rules:" + fmt.Sprintf(validRule, pattern) + extra
}

func joinErrs(errs []error) string {
	var b strings.Builder
	for _, e := range errs {
		b.WriteString("  " + e.Error() + "\n")
	}
	return b.String()
}
