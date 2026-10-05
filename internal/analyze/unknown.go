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

func unknownDomain(name string, da *domainAcc) model.UnknownDomain {
	u := model.UnknownDomain{Domain: name, Group: Registrable(name), Count: da.count}
	if da.count > 0 {
		u.First, u.Last = time.Unix(0, da.first), time.Unix(0, da.last)
	}
	return u
}

func topUnknown(us []model.UnknownDomain) []model.UnknownDomain {
	slices.SortFunc(us, func(x, y model.UnknownDomain) int {
		return cmp.Or(cmp.Compare(y.Count, x.Count), cmp.Compare(x.Domain, y.Domain))
	})
	return us[:min(len(us), maxUnknown)]
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
