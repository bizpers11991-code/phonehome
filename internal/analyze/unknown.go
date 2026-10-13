package analyze

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
	"github.com/bizpers11991-code/phonehome/internal/redact"
)

// maxUnknown caps DeviceReport.Unknown.
const maxUnknown = 50

// reportable reports whether a domain is worth listing as unclassified or as
// a heartbeat: a public, multi-label name outside the local and reverse zones
// (redact.Local).
func reportable(domain string) bool {
	return strings.Contains(domain, ".") && !redact.Local(domain)
}

// unknownTop collects a device's unclassified domains and keeps the
// maxUnknown most looked up. A device can look up hundreds of thousands of
// distinct names the knowledge base has never heard of (CDN hash hosts,
// generated names), so it sorts and trims as it goes instead of holding them
// all until the end; names are distinct per device, so the order (count, then
// name) is total and the result does not depend on when it trims.
type unknownTop struct{ us []model.UnknownDomain }

// unknownTrimAt is how many candidates pile up before trimming to maxUnknown.
const unknownTrimAt = 8 * maxUnknown

func (t *unknownTop) add(name string, da *domainAcc) {
	u := model.UnknownDomain{Domain: name, Count: int(da.count)}
	if da.count > 0 {
		u.First, u.Last = time.Unix(0, da.first), time.Unix(0, da.last)
	}
	t.us = append(t.us, u)
	if len(t.us) >= unknownTrimAt {
		t.us = sortUnknown(t.us)[:maxUnknown]
	}
}

// result is the top maxUnknown, most looked up first, nil if there are none.
// The grouping (a string split per name) is only worked out for those.
func (t *unknownTop) result() []model.UnknownDomain {
	if len(t.us) == 0 {
		return nil
	}
	us := sortUnknown(t.us)
	us = slices.Clone(us[:min(len(us), maxUnknown)])
	for i := range us {
		us[i].Group = Registrable(us[i].Domain)
	}
	return us
}

func sortUnknown(us []model.UnknownDomain) []model.UnknownDomain {
	slices.SortFunc(us, func(x, y model.UnknownDomain) int {
		return cmp.Or(cmp.Compare(y.Count, x.Count), cmp.Compare(x.Domain, y.Domain))
	})
	return us
}

// secondLevel are second-level labels that country-code TLDs commonly
// delegate under (co.uk, com.au, ne.jp, ...).
var secondLevel = map[string]bool{
	"ac": true, "co": true, "com": true, "edu": true, "go": true, "gov": true,
	"ne": true, "net": true, "or": true, "org": true,
}

// sharedSuffixes are cloud domains whose subdomains belong to unrelated
// customers, so they are treated like public suffixes.
var sharedSuffixes = []string{
	"amazonaws.com", "cloudfront.net", "azurewebsites.net", "cloudapp.net",
	"appspot.com", "herokuapp.com", "firebaseio.com", "web.app",
}

// Registrable approximates the registrable domain ("eTLD+1") of a hostname
// for grouping: a.b.example.com → example.com, x.example.co.uk →
// example.co.uk. phonehome ships no public-suffix list, so this is a
// heuristic; it is only used to group, never to match rules.
func Registrable(domain string) string {
	labels := strings.Split(domain, ".")
	n := 2
	if k := len(labels); k >= 3 && len(labels[k-1]) == 2 && secondLevel[labels[k-2]] {
		n = 3
	}
	for _, s := range sharedSuffixes {
		if strings.HasSuffix(domain, "."+s) {
			n = strings.Count(s, ".") + 2
			break
		}
	}
	if len(labels) <= n {
		return domain
	}
	return strings.Join(labels[len(labels)-n:], ".")
}
