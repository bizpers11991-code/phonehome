package analyze

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// maxUnknown caps DeviceReport.Unknown.
const maxUnknown = 50

// localSuffixes are names that only mean something inside one network, plus
// reverse-lookup zones. They are never reported as unclassified: no knowledge
// base rule can describe them, and they tend to carry hostnames and addresses.
var localSuffixes = []string{
	"arpa", // in-addr.arpa, ip6.arpa, home.arpa, resolver.arpa
	"local", "lan", "home", "internal", "intranet", "corp", "private",
	"localdomain", "localhost", "test", "invalid",
}

// reportable reports whether an unclassified domain is worth listing: a
// public, multi-label name outside the local and reverse zones.
func reportable(domain string) bool {
	if !strings.Contains(domain, ".") {
		return false
	}
	for _, s := range localSuffixes {
		if domain == s || strings.HasSuffix(domain, "."+s) {
			return false
		}
	}
	return true
}

func unknownDomain(name string, da *domainAcc) model.UnknownDomain {
	u := model.UnknownDomain{Domain: name, Group: Registrable(name), Count: da.count}
	if len(da.times) > 0 {
		u.First = time.Unix(0, slices.Min(da.times))
		u.Last = time.Unix(0, slices.Max(da.times))
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
