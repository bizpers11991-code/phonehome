package pihole

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
	"github.com/bizpers11991-code/phonehome/internal/source"
)

var _ source.DNSSource = (*API)(nil)

// fakeFTL mimics the parts of FTL's v6 API that API uses: login, logout
// and /api/queries with time filtering, sorting and offset pagination.
type fakeFTL struct {
	t           *testing.T
	password    string
	queries     []apiQuery
	ignoreOrder bool // behave like a server that only sorts newest first

	mu      sync.Mutex
	nextSID int
	sids    map[string]bool
	logins  int
	logouts []string
	pages   int
}

func (f *fakeFTL) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.URL.Path == "/api/auth" && r.Method == http.MethodPost:
		var body struct{ Password string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Password != f.password {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"session":{"valid":false,"totp":false,"sid":null,"validity":-1}}`)
			return
		}
		f.logins++
		f.nextSID++
		sid := "sid-" + strconv.Itoa(f.nextSID)
		f.sids[sid] = true
		fmt.Fprintf(w, `{"session":{"valid":true,"totp":false,"sid":%q,"csrf":"x","validity":1800},"took":0.001}`, sid)
	case r.URL.Path == "/api/auth" && r.Method == http.MethodDelete:
		sid := r.Header.Get("X-FTL-SID")
		f.logouts = append(f.logouts, sid)
		delete(f.sids, sid)
		w.WriteHeader(http.StatusNoContent)
	case r.URL.Path == "/api/queries" && r.Method == http.MethodGet:
		if !f.sids[r.Header.Get("X-FTL-SID")] {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":{"key":"unauthorized","message":"Unauthorized","hint":null},"took":0}`)
			return
		}
		f.pages++
		f.serveQueries(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeFTL) serveQueries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	length, _ := strconv.Atoi(q.Get("length"))
	start, _ := strconv.Atoi(q.Get("start"))
	var rows []apiQuery
	for _, row := range f.queries {
		if from := q.Get("from"); from != "" {
			ts, err := strconv.ParseFloat(from, 64)
			if err != nil {
				f.t.Errorf("bad from %q", from)
			}
			if row.Time < ts {
				continue
			}
		}
		if until := q.Get("until"); until != "" { // FTL: timestamp < until
			ts, err := strconv.ParseFloat(until, 64)
			if err != nil {
				f.t.Errorf("bad until %q", until)
			}
			if row.Time >= ts {
				continue
			}
		}
		rows = append(rows, row)
	}
	asc := q.Get("columns[0][data]") == "time" && q.Get("order[0][column]") == "0" && q.Get("order[0][dir]") == "asc"
	if asc && !f.ignoreOrder {
		// FTL's timestamp index yields ties in id order.
		slices.SortFunc(rows, func(a, b apiQuery) int {
			return cmp.Or(cmp.Compare(a.Time, b.Time), cmp.Compare(a.ID, b.ID))
		})
	} else {
		slices.SortFunc(rows, func(a, b apiQuery) int { return cmp.Compare(b.ID, a.ID) })
	}
	rows = rows[min(start, len(rows)):]
	rows = rows[:min(length, len(rows))]

	type client struct {
		IP   string  `json:"ip"`
		Name *string `json:"name"`
	}
	out := make([]map[string]any, len(rows))
	for i, row := range rows {
		out[i] = map[string]any{
			"id": row.ID, "time": row.Time, "type": row.Type, "status": row.Status,
			"dnssec": "UNKNOWN", "domain": row.Domain, "upstream": nil,
			"reply":  map[string]any{"type": "IP", "time": 0.0012},
			"client": client{IP: row.Client.IP}, "list_id": nil,
			"ede": map[string]any{"code": -1, "text": nil}, "cname": nil,
		}
	}
	json.NewEncoder(w).Encode(map[string]any{
		"queries": out, "cursor": len(f.queries), "recordsTotal": len(f.queries),
		"recordsFiltered": len(f.queries), "draw": 0, "took": 0.003,
	})
}

func newFakeFTL(t *testing.T, queries []apiQuery) (*fakeFTL, *API) {
	t.Helper()
	f := &fakeFTL{t: t, password: "app-pass", queries: queries, sids: map[string]bool{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	a, err := NewAPI(srv.URL+"/admin/", "app-pass", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	return f, a
}

func q(id int64, ts float64, typ, status, domain, ip string) apiQuery {
	r := apiQuery{ID: id, Time: ts, Type: typ, Status: status, Domain: domain}
	r.Client.IP = ip
	return r
}

func TestAPIFetch(t *testing.T) {
	const t0 = 1727870281.5
	f, a := newFakeFTL(t, []apiQuery{
		q(1, t0, "A", "FORWARDED", "Example.org.", "192.168.1.20"),
		q(2, t0, "AAAA", "GRAVITY", "ads.example.net", "192.168.1.20"),
		q(3, t0, "HTTPS", "CACHE", "example.org", "192.168.1.21"),
		q(4, t0+1, "TYPE65", "DENYLIST_CNAME", "cname.example.net", "fe80::2"),
		q(5, t0+2, "A", "SPECIAL_DOMAIN", "use-application-dns.net", "not-an-ip"),
		q(6, t0+3, "OTHER", "EXTERNAL_BLOCKED_NULL", "blocked.example", "192.168.1.22"),
	})
	ctx := context.Background()
	tm := func(off float64) time.Time { return unixTime(t0 + off) }

	got, cur, err := a.FetchDNS(ctx, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	assertQueries(t, got, []model.DNSQuery{
		{Time: tm(0), ClientIP: netip.MustParseAddr("192.168.1.20"), Domain: "example.org", QType: "A", Source: "pihole-api"},
		{Time: tm(0), ClientIP: netip.MustParseAddr("192.168.1.20"), Domain: "ads.example.net", QType: "AAAA", Blocked: true, Source: "pihole-api"},
	})
	if want := "2@1727870281.5"; cur != want {
		t.Fatalf("cursor = %q, want %q", cur, want)
	}

	got, cur, err = a.FetchDNS(ctx, cur, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertQueries(t, got, []model.DNSQuery{
		{Time: tm(0), ClientIP: netip.MustParseAddr("192.168.1.21"), Domain: "example.org", QType: "HTTPS", Source: "pihole-api"},
		{Time: tm(1), ClientIP: netip.MustParseAddr("fe80::2"), Domain: "cname.example.net", QType: "TYPE65", Blocked: true, Source: "pihole-api"},
		{Time: tm(3), ClientIP: netip.MustParseAddr("192.168.1.22"), Domain: "blocked.example", Blocked: true, Source: "pihole-api"},
	})
	if want := "6@1727870284.5"; cur != want {
		t.Fatalf("cursor = %q, want %q", cur, want)
	}

	got, cur2, err := a.FetchDNS(ctx, cur, 10)
	if err != nil || got != nil || cur2 != cur {
		t.Fatalf("nothing new: got %v, %q, %v", got, cur2, err)
	}

	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if f.logins != 1 || !slices.Equal(f.logouts, []string{"sid-1"}) {
		t.Fatalf("logins %d, logouts %v: want one session, closed", f.logins, f.logouts)
	}
}

// TestAPISettle checks that queries are read only once FTL has settled
// their status: a forwarded query turns GRAVITY_CNAME when the reply's
// CNAME chain hits a blocked name.
func TestAPISettle(t *testing.T) {
	const t0 = 1727870000.0
	f, a := newFakeFTL(t, []apiQuery{
		q(1, t0, "A", "FORWARDED", "a.example", "192.168.1.20"),
		q(2, t0+20, "A", "FORWARDED", "metrics.vendor.example", "192.168.1.20"),
		q(3, t0+21, "A", "NONE", "x.example", "192.168.1.20"),
	})
	now := time.Unix(t0+40, 0)
	a.now = func() time.Time { return now }
	ctx := context.Background()

	got, cur, err := a.FetchDNS(ctx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Domain != "a.example" || cur != "1@1727870000" {
		t.Fatalf("got %+v, cursor %q: want only the settled query", got, cur)
	}

	f.mu.Lock()
	f.queries[1].Status = "GRAVITY_CNAME"
	f.queries[2].Type = "N/A"
	f.mu.Unlock()
	now = now.Add(15 * time.Second)
	got, _, err = a.FetchDNS(ctx, cur, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertQueries(t, got, []model.DNSQuery{
		{Time: unixTime(t0 + 20), ClientIP: netip.MustParseAddr("192.168.1.20"), Domain: "metrics.vendor.example", QType: "A", Blocked: true, Source: "pihole-api"},
		{Time: unixTime(t0 + 21), ClientIP: netip.MustParseAddr("192.168.1.20"), Domain: "x.example", Source: "pihole-api"},
	})
}

// TestAPIPagination puts more already-seen queries at the cursor's instant
// than fit in one page, so the reader has to page past them.
func TestAPIPagination(t *testing.T) {
	const t0 = 1727870000.0
	var rows []apiQuery
	for i := range int64(250) {
		rows = append(rows, q(i+1, t0, "A", "FORWARDED", fmt.Sprintf("d%d.example", i+1), "192.168.1.20"))
	}
	f, a := newFakeFTL(t, rows)

	got, cur, err := a.FetchDNS(context.Background(), "180@1727870000", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 10 || got[0].Domain != "d181.example" || got[9].Domain != "d190.example" {
		t.Fatalf("got %d queries starting %+v", len(got), got)
	}
	if cur != "190@1727870000" {
		t.Fatalf("cursor = %q", cur)
	}
	if f.pages != 2 {
		t.Fatalf("fetched %d pages, want 2", f.pages)
	}
}

func TestAPIReauth(t *testing.T) {
	f, a := newFakeFTL(t, []apiQuery{
		q(1, 100, "A", "FORWARDED", "a.example", "192.168.1.20"),
		q(2, 200, "A", "FORWARDED", "b.example", "192.168.1.20"),
	})
	ctx := context.Background()
	_, cur, err := a.FetchDNS(ctx, "", 1)
	if err != nil {
		t.Fatal(err)
	}

	f.mu.Lock()
	clear(f.sids) // the session times out
	f.mu.Unlock()

	got, _, err := a.FetchDNS(ctx, cur, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Domain != "b.example" {
		t.Fatalf("got %+v", got)
	}
	if f.logins != 2 {
		t.Fatalf("logins = %d, want 2", f.logins)
	}
}

func TestAPIErrors(t *testing.T) {
	ctx := context.Background()

	f, a := newFakeFTL(t, nil)
	f.mu.Lock()
	f.password = "something-else"
	f.mu.Unlock()
	if _, _, err := a.FetchDNS(ctx, "", 10); err == nil || !strings.Contains(err.Error(), "app password") {
		t.Fatalf("wrong password: err = %v", err)
	}

	f, a = newFakeFTL(t, []apiQuery{
		q(1, 100, "A", "FORWARDED", "a.example", "192.168.1.20"),
		q(2, 200, "A", "FORWARDED", "b.example", "192.168.1.20"),
	})
	f.mu.Lock()
	f.ignoreOrder = true
	f.mu.Unlock()
	if _, _, err := a.FetchDNS(ctx, "", 10); err == nil || !strings.Contains(err.Error(), "sort order") {
		t.Fatalf("unsorted reply: err = %v", err)
	}
	if _, _, err := a.FetchDNS(ctx, "17", 10); err == nil {
		t.Fatal("expected error for bad cursor")
	}
	if _, err := NewAPI("pi.hole", "", nil); err == nil {
		t.Fatal("expected error for URL without scheme")
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := a.FetchDNS(cctx, "", 10); err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
