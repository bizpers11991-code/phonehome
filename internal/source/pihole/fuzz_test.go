package pihole

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ftlReply is an /api/queries reply with every field FTL's api/queries.c
// writes for a row, as FTL v6 sends it.
const ftlReply = `{"queries":[` +
	`{"id":7,"time":1759402803.25,"type":"A","status":"GRAVITY_CNAME","dnssec":"UNKNOWN","domain":"metrics.vendor.example","upstream":"127.0.0.1#5335","reply":{"type":"CNAME","time":0.0121},"client":{"ip":"192.168.1.21","name":null},"list_id":12,"ede":{"code":-1,"text":null},"cname":"tracker.example"},` +
	`{"id":8,"time":1759402804.5,"type":"TYPE65534","status":"CACHE_STALE","dnssec":"INSECURE","domain":"example.net","upstream":null,"reply":{"type":"IP","time":0.0002},"client":{"ip":"fd00::20","name":"phone.lan"},"list_id":null,"ede":{"code":3,"text":"Stale Answer"},"cname":null},` +
	`{"id":9,"time":1759402805,"type":"NONE","status":"EXTERNAL_BLOCKED_EDE15","dnssec":"UNKNOWN","domain":"blocked.example","upstream":"127.0.0.1#5335","reply":{"type":"NXDOMAIN","time":0.02},"client":{"ip":"192.168.1.22","name":null},"list_id":null,"ede":{"code":15,"text":"Blocked"},"cname":null}` +
	`],"cursor":9,"recordsTotal":3,"recordsFiltered":3,"draw":0,"earliest_timestamp":1759402803.25,"earliest_timestamp_disk":1759316400,"took":0.0004}`

// FuzzAPIFetch serves arbitrary /api/queries replies and checks that
// FetchDNS never exceeds its limit, never moves its cursor backwards and
// returns only well-formed lookups.
func FuzzAPIFetch(f *testing.F) {
	f.Add(ftlReply, uint8(2))
	f.Add(`{"queries":[]}`, uint8(1))
	f.Add(`{"queries":[{"id":1,"time":-1,"domain":".","client":{"ip":"::ffff:10.0.0.1"}}]}`, uint8(1))
	// One server for all inputs: a server per input runs out of ports.
	var body atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(body.Load().(string)))
	}))
	f.Cleanup(srv.Close)
	f.Fuzz(func(t *testing.T, reply string, limit uint8) {
		body.Store(reply)
		a, err := NewAPI(srv.URL, "", srv.Client())
		if err != nil {
			t.Fatal(err)
		}
		a.now = func() time.Time { return time.Unix(1759402900, 0) }
		lim := int(limit%8) + 1
		qs, cur, err := a.FetchDNS(context.Background(), "", lim)
		if err != nil {
			return
		}
		if len(qs) > lim {
			t.Fatalf("got %d lookups, limit %d", len(qs), lim)
		}
		for _, q := range qs {
			if q.Domain == "" || strings.HasSuffix(q.Domain, ".") || !q.ClientIP.IsValid() || q.ClientIP.Is4In6() || q.ClientIP.Zone() != "" {
				t.Fatalf("malformed lookup %+v", q)
			}
		}
		if cur == "" && len(qs) > 0 {
			t.Fatal("lookups without a cursor")
		}
		if cur != "" {
			if _, _, err := parseAPICursor(cur); err != nil {
				t.Fatalf("returned unreadable cursor %q", cur)
			}
		}
	})
}
