package analyze

import (
	"cmp"
	"slices"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// compareDomainStats orders a device's domains: most looked up first, then by
// name (names are distinct per device, so the order is total).
func compareDomainStats(x, y model.DomainStat) int {
	return cmp.Or(cmp.Compare(y.Count, x.Count), cmp.Compare(x.Domain, y.Domain))
}

// topDomains keeps the maxTopDomains most looked up of the domains added to
// it, trimming as it goes so a device with hundreds of thousands of distinct
// domains never holds them all. The order is total, so the result does not
// depend on when it trims, and equals the head of the fully sorted list.
type topDomains struct{ ds []model.DomainStat }

// topDomainsTrimAt is how many candidates pile up before trimming.
const topDomainsTrimAt = 8 * maxTopDomains

func (t *topDomains) add(ds model.DomainStat) {
	t.ds = append(t.ds, ds)
	if len(t.ds) >= topDomainsTrimAt {
		slices.SortFunc(t.ds, compareDomainStats)
		t.ds = t.ds[:maxTopDomains]
	}
}

// result is the top maxTopDomains, most looked up first; empty, not nil,
// when there are none, as the head of a full list is.
func (t *topDomains) result() []model.DomainStat {
	slices.SortFunc(t.ds, compareDomainStats)
	n := min(len(t.ds), maxTopDomains)
	return append(make([]model.DomainStat, 0, n), t.ds[:n]...)
}
