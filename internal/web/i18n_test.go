package web

import (
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

var (
	entryRe       = regexp.MustCompile(`'([\w.\-]+)':\s*(\{[^}]*\}|'(?:[^'\\]|\\.)*'|"(?:[^"\\]|\\.)*")`)
	placeholderRe = regexp.MustCompile(`\{(\w+)\}`)
	localeRe      = regexp.MustCompile(`(?m)^  ([a-z]{2}): \{$`)
)

// catalog parses i18n.js into locale → key → message (plural forms joined).
func catalog(t *testing.T) map[string]map[string]string {
	t.Helper()
	b, err := staticFS.ReadFile("static/i18n.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	starts := localeRe.FindAllStringSubmatchIndex(src, -1)
	if len(starts) == 0 {
		t.Fatal("no locales found in i18n.js")
	}
	out := map[string]map[string]string{}
	for i, s := range starts {
		end := len(src)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		lang := src[s[2]:s[3]]
		out[lang] = map[string]string{}
		for _, m := range entryRe.FindAllStringSubmatch(src[s[1]:end], -1) {
			if _, dup := out[lang][m[1]]; dup {
				t.Errorf("%s: duplicate key %s", lang, m[1])
			}
			out[lang][m[1]] = m[2]
		}
	}
	return out
}

func placeholders(msg string) []string {
	var ps []string
	for _, m := range placeholderRe.FindAllStringSubmatch(msg, -1) {
		if !slices.Contains(ps, m[1]) && m[1] != "one" && m[1] != "other" {
			ps = append(ps, m[1])
		}
	}
	slices.Sort(ps)
	return ps
}

// TestCatalogComplete checks that every language has every English
// message, with the same placeholders, and that the declared languages
// match the catalog.
func TestCatalogComplete(t *testing.T) {
	cat := catalog(t)
	en := cat["en"]
	if len(en) < 150 {
		t.Fatalf("only %d English messages parsed; is the parser out of date?", len(en))
	}
	b, _ := staticFS.ReadFile("static/i18n.js")
	for _, lang := range []string{"en", "de", "fr", "es", "nl"} {
		if cat[lang] == nil {
			t.Errorf("no %s messages", lang)
			continue
		}
		if !strings.Contains(string(b), lang+": '") {
			t.Errorf("%s is not listed in LOCALES", lang)
		}
		for _, k := range slices.Sorted(maps.Keys(en)) {
			msg, ok := cat[lang][k]
			if !ok {
				t.Errorf("%s: missing %s", lang, k)
				continue
			}
			if got, want := placeholders(msg), placeholders(en[k]); !slices.Equal(got, want) {
				t.Errorf("%s: %s has placeholders %v, English has %v", lang, k, got, want)
			}
			if strings.HasPrefix(en[k], "{") != strings.HasPrefix(msg, "{") {
				t.Errorf("%s: %s is plural in one language only", lang, k)
			}
		}
		for k := range cat[lang] {
			if _, ok := en[k]; !ok {
				t.Errorf("%s: %s is not an English key", lang, k)
			}
		}
	}
}

// TestCatalogCoversPage checks that every key app.js and index.html use
// exists, including the families app.js builds from ids.
func TestCatalogCoversPage(t *testing.T) {
	en := catalog(t)["en"]
	var used []string
	for _, f := range []string{"static/app.js", "static/index.html"} {
		b, err := staticFS.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		re := regexp.MustCompile(`\b(?:t|tn)\('([\w.\-]+)'|data-i18n(?:-label)?="([\w.\-]+)"|\['(guide\.\w+)'|note: '(snip\.\w+)'`)
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			if k := m[1] + m[2] + m[3] + m[4]; !strings.HasSuffix(k, ".") { // "cat." + id: checked below
				used = append(used, k)
			}
		}
	}
	for _, c := range model.Categories() {
		used = append(used, "cat."+string(c))
	}
	for _, k := range []model.DeviceKind{model.KindTV, model.KindStreamer, model.KindSpeaker, model.KindCamera, model.KindVacuum,
		model.KindPlug, model.KindHub, model.KindAppliance, model.KindConsole, model.KindPhone, model.KindComputer,
		model.KindNetwork, model.KindUnknown} {
		used = append(used, "kind."+string(k))
	}
	for _, k := range []string{"when.1", "when.7", "when.30", "conf.high", "conf.medium", "conf.low",
		"bypass.doh-flow", "bypass.dot-flow", "bypass.foreign-dns", "bypass.doh-lookup",
		"health.healthy", "health.stale", "health.error"} {
		used = append(used, k)
	}
	if len(used) < 100 {
		t.Fatalf("found only %d uses; is the pattern out of date?", len(used))
	}
	for _, k := range used {
		if _, ok := en[k]; !ok {
			t.Errorf("%s is used but not in the catalog", k)
		}
	}
}
