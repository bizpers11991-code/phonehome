package web_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
	"github.com/bizpers11991-code/phonehome/internal/web"
	"github.com/bizpers11991-code/phonehome/internal/web/internal/fixture"
)

var now = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

// fake records what the handler asked for and returns canned answers.
type fake struct {
	err      error
	status   model.Status
	period   model.Period
	labelID  string
	label    string
	receipt  struct{ id, format string }
	reported int
}

func (f *fake) Report(_ context.Context, p model.Period) (model.HomeReport, error) {
	f.period = p
	f.reported++
	return model.HomeReport{Period: p}, f.err
}

func (f *fake) Status(context.Context) (model.Status, error) { return f.status, f.err }

func (f *fake) SetLabel(_ context.Context, id, label string) error {
	f.labelID, f.label = id, label
	return f.err
}

func (f *fake) Receipt(_ context.Context, p model.Period, id, format string) ([]byte, error) {
	f.period = p
	f.receipt.id, f.receipt.format = id, format
	return []byte("<" + format + ">"), f.err
}

// newServer serves b at a fixed time. httptest requests are addressed to
// example.com, which is allowed here.
func newServer(b web.Backend, o web.Options) http.Handler {
	o.Now = func() time.Time { return now }
	o.AllowedHosts = append(o.AllowedHosts, "example.com")
	return web.New(b, o)
}

func do(t *testing.T, h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	return do(t, h, httptest.NewRequest(http.MethodGet, target, nil))
}

func postLabel(target, ctype, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	if ctype != "" {
		r.Header.Set("Content-Type", ctype)
	}
	return r
}

func TestStaticAndHealth(t *testing.T) {
	h := newServer(&fake{}, web.Options{})
	for _, tc := range []struct {
		path, ctype string
		code        int
	}{
		{"/", "text/html", 200},
		{"/assets/app.js", "javascript", 200},
		{"/assets/app.css", "text/css", 200},
		{"/assets/icons.svg", "image/svg+xml", 200},
		{"/assets/missing.js", "", 404},
		{"/healthz", "text/plain", 200},
		{"/nope", "", 404},
	} {
		w := get(t, h, tc.path)
		if w.Code != tc.code || !strings.Contains(w.Header().Get("Content-Type"), tc.ctype) {
			t.Errorf("GET %s = %d %q, want %d %q", tc.path, w.Code, w.Header().Get("Content-Type"), tc.code, tc.ctype)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := newServer(&fake{}, web.Options{})
	for _, path := range []string{"/", "/api/report", "/receipt/home.svg", "/nope"} {
		w := get(t, h, path)
		for k, want := range map[string]string{
			"X-Content-Type-Options": "nosniff",
			"Referrer-Policy":        "no-referrer",
			"X-Frame-Options":        "DENY",
		} {
			if got := w.Header().Get(k); got != want {
				t.Errorf("%s: %s = %q, want %q", path, k, got, want)
			}
		}
		csp := w.Header().Get("Content-Security-Policy")
		for _, d := range []string{"default-src 'self'", "img-src 'self' data:", "style-src 'self'", "script-src 'self'"} {
			if !strings.Contains(csp, d+";") {
				t.Errorf("%s: CSP %q lacks %q", path, csp, d)
			}
		}
	}
}

func TestAssetCaching(t *testing.T) {
	h := newServer(&fake{}, web.Options{})
	r := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	r.Header.Set("Accept-Encoding", "br, gzip")
	w := do(t, h, r)
	if w.Header().Get("Content-Encoding") != "gzip" || w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("headers = %v", w.Header())
	}
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(zr)
	if !bytes.Contains(body, []byte("use strict")) {
		t.Errorf("gunzipped body does not look like app.js")
	}

	r = httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	r.Header.Set("If-None-Match", get(t, h, "/assets/app.js").Header().Get("ETag"))
	if w := do(t, h, r); w.Code != http.StatusNotModified {
		t.Errorf("conditional GET = %d, want 304", w.Code)
	}
}

func TestReportPeriod(t *testing.T) {
	for _, tc := range []struct {
		query string
		code  int
		days  int
	}{
		{"", 200, 7},
		{"?days=1", 200, 1},
		{"?days=7", 200, 7},
		{"?days=30", 200, 30},
		{"?days=2", 400, 0},
		{"?days=-7", 400, 0},
		{"?days=week", 400, 0},
	} {
		f := &fake{}
		w := get(t, newServer(f, web.Options{}), "/api/report"+tc.query)
		if w.Code != tc.code {
			t.Errorf("%q: code %d, want %d", tc.query, w.Code, tc.code)
			continue
		}
		if tc.code != 200 {
			if f.reported != 0 {
				t.Errorf("%q: backend called despite bad params", tc.query)
			}
			continue
		}
		want := model.Period{From: now.AddDate(0, 0, -tc.days), To: now}
		if f.period != want {
			t.Errorf("%q: period %v, want %v", tc.query, f.period, want)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%q: API response is cacheable", tc.query)
		}
	}
}

func TestBackendErrors(t *testing.T) {
	f := &fake{err: errors.New("disk on fire at /var/lib/secret")}
	h := newServer(f, web.Options{})
	for _, path := range []string{"/api/report", "/api/status", "/receipt/home.png"} {
		w := get(t, h, path)
		if w.Code != 500 || strings.Contains(w.Body.String(), "secret") {
			t.Errorf("%s = %d %s; want 500 without details", path, w.Code, w.Body)
		}
	}
	f.err = fmt.Errorf("lookup: %w", web.ErrNotFound)
	if w := get(t, h, "/receipt/mac:aa:bb:cc:dd:ee:ff.svg"); w.Code != 404 {
		t.Errorf("not-found receipt = %d, want 404", w.Code)
	}
	if w := do(t, h, postLabel("/api/devices/ip:10.0.0.9/label", "application/json", `{"label":"x"}`)); w.Code != 404 {
		t.Errorf("not-found label = %d, want 404", w.Code)
	}
}

func TestReceipts(t *testing.T) {
	for _, tc := range []struct {
		path, id, format, ctype string
	}{
		{"/receipt/home.svg", "", "svg", "image/svg+xml"},
		{"/receipt/home.png?days=30", "", "png", "image/png"},
		{"/receipt/mac:f4:7b:09:3c:a1:2e.svg", "mac:f4:7b:09:3c:a1:2e", "svg", "image/svg+xml"},
		{"/receipt/mac%3Af4%3A7b%3A09%3A3c%3Aa1%3A2e.png", "mac:f4:7b:09:3c:a1:2e", "png", "image/png"},
		{"/receipt/ip:192.168.1.5.png", "ip:192.168.1.5", "png", "image/png"},
		{"/receipt/ip:fe80::1%25eth0.svg", "ip:fe80::1%eth0", "svg", "image/svg+xml"},
		{"/receipt/weird%2Fid.svg", "weird/id", "svg", "image/svg+xml"},
	} {
		f := &fake{}
		w := get(t, newServer(f, web.Options{}), tc.path)
		if w.Code != 200 || w.Header().Get("Content-Type") != tc.ctype {
			t.Errorf("%s = %d %q", tc.path, w.Code, w.Header().Get("Content-Type"))
			continue
		}
		if f.receipt.id != tc.id || f.receipt.format != tc.format {
			t.Errorf("%s: backend got (%q, %q), want (%q, %q)", tc.path, f.receipt.id, f.receipt.format, tc.id, tc.format)
		}
	}
	h := newServer(&fake{}, web.Options{})
	for _, path := range []string{"/receipt/home.pdf", "/receipt/home", "/receipt/.svg", "/receipt/svg"} {
		if w := get(t, h, path); w.Code != 404 {
			t.Errorf("%s = %d, want 404", path, w.Code)
		}
	}
	if w := get(t, h, "/receipt/home.svg?days=3"); w.Code != 400 {
		t.Errorf("bad days = %d, want 400", w.Code)
	}
}

func TestSetLabel(t *testing.T) {
	long := strings.Repeat("é", web.MaxLabelLen)
	for _, tc := range []struct {
		name, path, body string
		code             int
		id, label        string
	}{
		{"trimmed", "/api/devices/mac:aa:bb:cc:dd:ee:ff/label", `{"label":"  Kitchen Echo \n"}`, 204, "mac:aa:bb:cc:dd:ee:ff", "Kitchen Echo"},
		{"escaped id", "/api/devices/mac%3Aaa%3Abb%3Acc%3Add%3Aee%3Aff/label", `{"label":"TV"}`, 204, "mac:aa:bb:cc:dd:ee:ff", "TV"},
		{"ipv6 id", "/api/devices/ip:fe80::1/label", `{"label":"x"}`, 204, "ip:fe80::1", "x"},
		{"clear", "/api/devices/ip:10.0.0.2/label", `{"label":"   "}`, 204, "ip:10.0.0.2", ""},
		{"max length", "/api/devices/ip:10.0.0.2/label", `{"label":"` + long + `"}`, 204, "ip:10.0.0.2", long},
		{"too long", "/api/devices/ip:10.0.0.2/label", `{"label":"` + long + `x"}`, 400, "", ""},
		{"control char", "/api/devices/ip:10.0.0.2/label", `{"label":"a\u0007b"}`, 400, "", ""},
		{"missing label", "/api/devices/ip:10.0.0.2/label", `{}`, 400, "", ""},
		{"null label", "/api/devices/ip:10.0.0.2/label", `{"label":null}`, 400, "", ""},
		{"wrong type", "/api/devices/ip:10.0.0.2/label", `{"label":7}`, 400, "", ""},
		{"unknown field", "/api/devices/ip:10.0.0.2/label", `{"label":"a","admin":true}`, 400, "", ""},
		{"not json", "/api/devices/ip:10.0.0.2/label", `label=a`, 400, "", ""},
		{"huge body", "/api/devices/ip:10.0.0.2/label", `{"label":"` + strings.Repeat("a", 10000) + `"}`, 400, "", ""},
	} {
		f := &fake{}
		w := do(t, newServer(f, web.Options{}), postLabel(tc.path, "application/json; charset=utf-8", tc.body))
		if w.Code != tc.code {
			t.Errorf("%s: code %d (%s), want %d", tc.name, w.Code, w.Body, tc.code)
			continue
		}
		if f.labelID != tc.id || f.label != tc.label {
			t.Errorf("%s: backend got (%q, %q), want (%q, %q)", tc.name, f.labelID, f.label, tc.id, tc.label)
		}
	}
	if w := get(t, newServer(&fake{}, web.Options{}), "/api/devices/x/label"); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET label = %d, want 405", w.Code)
	}
}

func TestCSRF(t *testing.T) {
	const path, body = "/api/devices/ip:10.0.0.2/label", `{"label":"x"}`
	for _, tc := range []struct {
		name    string
		ctype   string
		headers map[string]string
		code    int
	}{
		{"no content type", "", nil, 403},
		{"form post", "application/x-www-form-urlencoded", nil, 403},
		{"text/plain", "text/plain", nil, 403},
		{"foreign origin", "application/json", map[string]string{"Origin": "http://evil.example"}, 403},
		{"cross-site fetch", "application/json", map[string]string{"Sec-Fetch-Site": "cross-site"}, 403},
		{"same origin", "application/json", map[string]string{"Origin": "http://example.com", "Sec-Fetch-Site": "same-origin"}, 204},
		{"matching origin only", "application/json", map[string]string{"Origin": "http://example.com"}, 204},
		{"no origin (curl)", "application/json", nil, 204},
	} {
		f := &fake{}
		r := postLabel(path, tc.ctype, body)
		for k, v := range tc.headers {
			r.Header.Set(k, v)
		}
		w := do(t, newServer(f, web.Options{}), r)
		if w.Code != tc.code {
			t.Errorf("%s: code %d (%s), want %d", tc.name, w.Code, w.Body, tc.code)
		}
		if tc.code == 403 && f.labelID != "" {
			t.Errorf("%s: label was written despite rejection", tc.name)
		}
	}
}

func TestBasicAuth(t *testing.T) {
	h := newServer(&fake{}, web.Options{Username: "admin", Password: "hunter2"})
	for _, tc := range []struct {
		name, path, user, pass string
		code                   int
	}{
		{"no credentials", "/api/report", "", "", 401},
		{"wrong password", "/api/report", "admin", "hunter3", 401},
		{"wrong user", "/", "root", "hunter2", 401},
		{"right", "/api/report", "admin", "hunter2", 200},
		{"assets need auth", "/assets/app.js", "", "", 401},
		{"health is open", "/healthz", "", "", 200},
	} {
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		if tc.user != "" {
			r.SetBasicAuth(tc.user, tc.pass)
		}
		w := do(t, h, r)
		if w.Code != tc.code {
			t.Errorf("%s: code %d, want %d", tc.name, w.Code, tc.code)
		}
		if tc.code == 401 && !strings.HasPrefix(w.Header().Get("WWW-Authenticate"), "Basic ") {
			t.Errorf("%s: missing WWW-Authenticate", tc.name)
		}
	}
	if w := get(t, newServer(&fake{}, web.Options{}), "/api/report"); w.Code != 200 {
		t.Errorf("auth enforced without a password: %d", w.Code)
	}
}

// Only requests addressed to this machine by a local name, an address or
// a configured name are answered, so a foreign site re-pointed at the
// server's address (DNS rebinding) cannot read or change anything.
func TestHostCheck(t *testing.T) {
	f := &fake{}
	h := web.New(f, web.Options{AllowedHosts: []string{"phonehome.example.com", "*.ts.net"}})
	for _, tc := range []struct {
		host string
		code int
	}{
		{"192.168.1.2:8099", 200},
		{"192.168.1.2", 200},
		{"[fe80::1]:8099", 200},
		{"[::1]", 200},
		{"localhost:8099", 200},
		{"pi:8099", 200},
		{"NAS", 200},
		{"pi.hole:8099", 200},
		{"phonehome.local:8099", 200},
		{"pi.lan.", 200},
		{"phonehome.home.arpa", 200},
		{"box.internal", 200},
		{"phonehome.example.com", 200},
		{"PhoneHome.Example.com:443", 200},
		{"pi.tailnet-1234.ts.net", 200},
		{"ts.net", 421},
		{"example.com", 421},
		{"rebind.attacker.example:8099", 421},
		{"phonehome.example.com.attacker.example", 421},
		{"sub.phonehome.example.com", 421}, // exact names match only themselves
		{"evilts.net", 421},
		{"evil-lan", 200}, // single-label names only resolve on the LAN
		{"evillan.attacker.example", 421},
		{"", 421},
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/report", nil)
		r.Host = tc.host
		w := do(t, h, r)
		if w.Code != tc.code {
			t.Errorf("Host %q: %d, want %d", tc.host, w.Code, tc.code)
		}
		if tc.code == 421 && !strings.Contains(w.Body.String(), "allowed_hosts") {
			t.Errorf("Host %q: refusal does not name allowed_hosts: %q", tc.host, w.Body.String())
		}
	}

	f.labelID = ""
	r := postLabel("/api/devices/mac:aa/label", "application/json", `{"label":"x"}`)
	r.Host = "rebind.attacker.example:8099"
	r.Header.Set("Origin", "http://rebind.attacker.example:8099")
	if w := do(t, h, r); w.Code != 421 || f.labelID != "" {
		t.Errorf("rename through a foreign Host: %d, label written for %q", w.Code, f.labelID)
	}
	// Only "*." patterns are wildcards; a bare "*" (refused by the config)
	// does not turn the check off.
	r = httptest.NewRequest(http.MethodGet, "/api/report", nil)
	r.Host = "rebind.attacker.example"
	if w := do(t, web.New(f, web.Options{AllowedHosts: []string{"*", "*example"}}), r); w.Code != 421 {
		t.Errorf("AllowedHosts * let a foreign Host in: %d", w.Code)
	}
	r = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	r.Host = "rebind.attacker.example"
	if w := do(t, h, r); w.Code != 200 {
		t.Errorf("healthz with a foreign Host: %d", w.Code)
	}
}

func TestStatusHealth(t *testing.T) {
	f := &fake{status: model.Status{Version: "1.2.3", Devices: 4, Sources: []model.SourceStatus{
		{Name: "pihole", Kind: "dns", LastRun: now.Add(-time.Minute), LastOK: now.Add(-time.Minute), Records: 10},
		{Name: "leases", Kind: "devices", LastRun: now.Add(-2 * time.Hour), LastOK: now.Add(-2 * time.Hour)},
		{Name: "conntrack", Kind: "flow", LastRun: now, LastOK: now.Add(-time.Hour), LastError: "permission denied"},
	}}}
	var got struct {
		Version string
		Health  string
		Sources []struct {
			Name, Health, LastOk, LastError string
		}
	}
	w := get(t, newServer(f, web.Options{}), "/api/status")
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != "1.2.3" || got.Health != "error" || len(got.Sources) != 3 {
		t.Fatalf("status = %+v", got)
	}
	for i, want := range []string{"ok", "stale", "error"} {
		if got.Sources[i].Health != want {
			t.Errorf("%s health = %q, want %q", got.Sources[i].Name, got.Sources[i].Health, want)
		}
	}
	if _, err := time.Parse(time.RFC3339, got.Sources[0].LastOk); err != nil {
		t.Errorf("lastOk not RFC 3339: %v", err)
	}
}

// TestStatusSetup pins the first-run fields the dashboard's setup panel reads.
func TestStatusSetup(t *testing.T) {
	f := &fake{status: model.Status{Setup: model.Setup{
		AutoDetect: true, CheckedAt: now,
		Problems: []model.SetupProblem{{
			Type: "pihole-db", Path: "/etc/pihole/pihole-FTL.db", Problem: "permission denied", Hint: "add the group",
		}},
	}}}
	var got struct {
		Health string
		Setup  struct {
			AutoDetect bool
			CheckedAt  string
			Sources    []struct{ Type, Location string }
			Problems   []struct {
				Type, Path, Problem, Hint string
				Optional                  bool
			}
		}
	}
	w := get(t, newServer(f, web.Options{}), "/api/status")
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	s := got.Setup
	if !s.AutoDetect || s.CheckedAt == "" || s.Sources == nil || len(s.Problems) != 1 || got.Health != "stale" {
		t.Fatalf("status = %s", w.Body)
	}
	if p := s.Problems[0]; p.Path != "/etc/pihole/pihole-FTL.db" || p.Problem != "permission denied" || p.Hint != "add the group" {
		t.Errorf("problem = %+v", p)
	}

	// The frontend relies on both lists being arrays, never null.
	w = get(t, newServer(&fake{}, web.Options{}), "/api/status")
	if !strings.Contains(w.Body.String(), `"sources":[],"problems":[]`) {
		t.Errorf("empty setup = %s", w.Body)
	}
}

// TestReportJSON pins the wire format the frontend relies on.
func TestReportJSON(t *testing.T) {
	b := fixture.New(now)
	w := get(t, newServer(b, web.Options{}), "/api/report?days=7")
	var rep struct {
		From       string
		Total      int
		SnoopShare float64
		Grade      string
		Categories []struct {
			ID, Label string
			Snooping  bool
		}
		Devices []struct {
			ID, Name, DefaultName, Kind, Grade string
			PrivateMAC                         bool `json:"privateMac"`
			Hourly                             []int
			Quiet                              struct{ StartHour, EndHour *int }
			Heartbeats                         []struct {
				CategoryLabel string
				EverySeconds  float64
			}
			TopDomains []struct{ CategoryLabel string }
			Bypasses   []struct{ Evidence string }
			Reason     struct {
				Rule, Text, VolumeGrade   string
				Lookups, BandFrom, BandTo int
				EverySeconds, DataHours   float64
				Provisional               bool
			}
			Coverage        float64
			CoveragePercent int  `json:"coveragePercent"`
			LowCoverage     bool `json:"lowCoverage"`
			ACRUnseen       bool `json:"acrUnseen"`
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse(time.RFC3339, rep.From); err != nil {
		t.Errorf("from: %v", err)
	}
	if len(rep.Categories) != len(model.Categories()) || rep.Categories[0].Label != "Content recognition" || !rep.Categories[0].Snooping {
		t.Errorf("categories = %+v", rep.Categories)
	}
	if rep.Total == 0 || rep.SnoopShare <= 0 || rep.SnoopShare >= 1 {
		t.Errorf("total %d, share %v", rep.Total, rep.SnoopShare)
	}
	if rep.Grade != "F" {
		t.Errorf("home grade = %q, want F (the worst device's)", rep.Grade)
	}
	tv := rep.Devices[0]
	if tv.Name != "Living room TV" || tv.DefaultName != "Samsung-QN65Q80B" || tv.Grade != "F" || tv.Kind != "tv" {
		t.Errorf("worst device = %+v", tv)
	}
	if len(tv.Hourly) != 24 || tv.Quiet.StartHour == nil || *tv.Quiet.StartHour != 1 || *tv.Quiet.EndHour != 6 {
		t.Errorf("hourly/quiet = %v %+v", len(tv.Hourly), tv.Quiet)
	}
	if hb := tv.Heartbeats[0]; hb.EverySeconds != 15 || hb.CategoryLabel != "Content recognition" {
		t.Errorf("heartbeat = %+v", hb)
	}
	if tv.TopDomains[0].CategoryLabel == "" || tv.Bypasses[0].Evidence != "dns.google" {
		t.Errorf("domains/bypass = %+v %+v", tv.TopDomains[0], tv.Bypasses)
	}
	var phone bool
	for _, d := range rep.Devices {
		if d.Kind == "phone" {
			phone = d.PrivateMAC
		}
	}
	if !phone {
		t.Error("iPhone's randomised MAC not flagged as private")
	}
	if r := tv.Reason; r.Rule != "acr-heartbeat" || r.EverySeconds != 15 || !strings.Contains(r.Text, "always F") || r.Provisional || r.DataHours != 7*24 {
		t.Errorf("reason = %+v", r)
	}
	if tv.Coverage != 1 || tv.CoveragePercent != 100 || tv.LowCoverage || tv.ACRUnseen {
		t.Errorf("coverage %v (%d%%), low %v, ACR unseen %v", tv.Coverage, tv.CoveragePercent, tv.LowCoverage, tv.ACRUnseen)
	}
	for _, d := range rep.Devices {
		if d.Kind != "streamer" {
			continue
		}
		// The fixture's Roku: graded on volume, with no ACR seen.
		r := d.Reason
		if d.Grade != "D" || r.Rule != "volume" || r.VolumeGrade != "D" || r.Lookups != 2360 || r.BandFrom != 1500 || r.BandTo != 5000 ||
			r.Text != "2,360 snooping lookups a day (D is 1,500–4,999)" || !d.ACRUnseen {
			t.Errorf("streamer: grade %s, reason %+v, ACR unseen %v", d.Grade, r, d.ACRUnseen)
		}
	}

	// Go field names must not leak into the wire format.
	raw := get(t, newServer(b, web.Options{}), "/api/report").Body.String()
	for _, k := range []string{`"Device"`, `"ByCategory"`, `"TopDomains"`, `"Every"`, `"Reason"`, `"VolumeGrade"`} {
		if strings.Contains(raw, k) {
			t.Errorf("Go field %s leaked into JSON", k)
		}
	}
}

func TestEmptyReportUsesArrays(t *testing.T) {
	b := fixture.New(now)
	b.Empty = true
	body := get(t, newServer(b, web.Options{}), "/api/report").Body.String()
	if !strings.Contains(body, `"devices":[]`) {
		t.Errorf("empty report should have devices: [], got %s", body)
	}
	if !strings.Contains(body, `"grade":""`) {
		t.Errorf("empty report should have an empty home grade, got %s", body)
	}
}

func TestLabelRoundTrip(t *testing.T) {
	b := fixture.New(now)
	h := newServer(b, web.Options{})
	w := do(t, h, postLabel("/api/devices/mac%3A68%3A54%3Afd%3A12%3A9b%3Ac0/label", "application/json", `{"label":"Den speaker"}`))
	if w.Code != 204 {
		t.Fatalf("label = %d %s", w.Code, w.Body)
	}
	if !strings.Contains(get(t, h, "/api/report").Body.String(), `"name":"Den speaker"`) {
		t.Error("new label not reflected in report")
	}
}

// TestUnknownAndSuggestLink checks the "help us classify" data: every device
// lists its unknown domains, and the GitHub link carries none of the device's
// private details. Demo data gets no link.
func TestUnknownAndSuggestLink(t *testing.T) {
	type device struct {
		Name, Hostname, Label, MAC string
		IPs                        []string
		Unknown                    []struct {
			Domain, Group, FirstSeen string
			Count                    int
		}
		SuggestURL *string `json:"suggestUrl"`
	}
	decode := func(b *fixture.Backend) []device {
		var rep struct{ Devices []device }
		if err := json.Unmarshal(get(t, newServer(b, web.Options{}), "/api/report?days=7").Body.Bytes(), &rep); err != nil {
			t.Fatal(err)
		}
		return rep.Devices
	}

	b := fixture.New(now)
	links := 0
	for _, d := range decode(b) {
		if d.Unknown == nil {
			t.Errorf("%s: unknown is null, want []", d.Name)
		}
		if len(d.Unknown) == 0 {
			if d.SuggestURL != nil {
				t.Errorf("%s: suggest link without unknown domains", d.Name)
			}
			continue
		}
		if u := d.Unknown[0]; u.Domain == "" || u.Group == "" || u.Count == 0 || u.FirstSeen == "" {
			t.Errorf("%s: unknown[0] = %+v", d.Name, u)
		}
		if d.SuggestURL == nil {
			t.Errorf("%s: no suggest link", d.Name)
			continue
		}
		links++
		link, err := url.QueryUnescape(strings.ToLower(*d.SuggestURL))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(link, "https://github.com/bizpers11991-code/phonehome/issues/new?") {
			t.Errorf("%s: link %s", d.Name, link)
		}
		private := append([]string{d.Name, d.Hostname, d.Label, d.MAC, strings.ReplaceAll(d.MAC, ":", "")}, d.IPs...)
		for _, p := range private {
			if p != "" && strings.Contains(link, strings.ToLower(p)) {
				t.Errorf("%s: link contains private %q: %s", d.Name, p, link)
			}
		}
		if !strings.Contains(link, "cdn.example-unknown.io") {
			t.Errorf("%s: link lacks its unknown domains: %s", d.Name, link)
		}
	}
	if links == 0 {
		t.Error("no device got a suggest link")
	}

	b.Demo = true
	for _, d := range decode(b) {
		if d.SuggestURL != nil {
			t.Errorf("demo device %s has a suggest link", d.Name)
		}
	}
}

func TestMetricsEndpoint(t *testing.T) {
	f := &fake{status: model.Status{Version: "v1"}}
	if w := get(t, newServer(f, web.Options{}), "/metrics"); w.Code != http.StatusNotFound {
		t.Fatalf("metrics off by default: status %d", w.Code)
	}

	h := newServer(f, web.Options{Metrics: true, Username: "u", Password: "p"})
	if w := get(t, h, "/metrics"); w.Code != http.StatusUnauthorized {
		t.Fatalf("metrics without credentials: status %d", w.Code)
	}
	scrape := func(target string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, target, nil)
		r.SetBasicAuth("u", "p")
		return do(t, h, r)
	}
	w := scrape("/metrics")
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain; version=0.0.4") ||
		!strings.Contains(w.Body.String(), `phonehome_info{version="v1"} 1`) {
		t.Fatalf("metrics: %d %q\n%s", w.Code, w.Header().Get("Content-Type"), w.Body)
	}
	if got := f.period.To.Sub(f.period.From); got != 7*24*time.Hour {
		t.Errorf("default window %v, want 7 days", got)
	}
	// Scrapes within a minute reuse the report; another window does not.
	scrape("/metrics")
	scrape("/metrics?days=1")
	if f.reported != 2 {
		t.Errorf("built %d reports, want 2", f.reported)
	}
	if w := scrape("/metrics?days=2"); w.Code != http.StatusBadRequest {
		t.Errorf("days=2: status %d", w.Code)
	}
}

// domainsBackend reports one device with two domains.
type domainsBackend struct{ fake }

func (b *domainsBackend) Report(_ context.Context, p model.Period) (model.HomeReport, error) {
	first := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	return model.HomeReport{Period: p, Devices: []model.DeviceReport{{
		Device: model.Device{ID: "mac:aa:bb:cc:00:11:22", Label: "TV"},
		Domains: []model.DomainStat{
			{Domain: "acr.example", Count: 40, Category: model.CatACR, CompanyName: "Example TV", Purpose: "Content recognition.",
				Rule: "acr.example", Confidence: "high", Evidence: []string{"https://example.org/paper"}, First: first, Last: first.Add(time.Hour)},
			{Domain: "mystery.example", Count: 3, Category: model.CatUnknown, First: first, Last: first},
		},
	}}}, nil
}

func TestDomainsEndpoint(t *testing.T) {
	h := newServer(&domainsBackend{}, web.Options{})
	w := get(t, h, "/api/devices/"+url.PathEscape("mac:aa:bb:cc:00:11:22")+"/domains?days=1")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var got struct {
		ID, Name string
		Domains  []map[string]any
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "TV" || len(got.Domains) != 2 {
		t.Fatalf("%+v", got)
	}
	d := got.Domains[0]
	if d["domain"] != "acr.example" || d["rule"] != "acr.example" || d["confidence"] != "high" ||
		d["firstSeen"] != "2026-10-01T08:00:00Z" || d["lastSeen"] != "2026-10-01T09:00:00Z" ||
		d["categoryLabel"] != "Content recognition" || len(d["evidence"].([]any)) != 1 {
		t.Errorf("first domain %v", d)
	}
	if u := got.Domains[1]; u["rule"] != "" || u["confidence"] != "" || len(u["evidence"].([]any)) != 0 {
		t.Errorf("unclassified domain %v", u)
	}
	if w := get(t, h, "/api/devices/mac:00:00:00:00:00:01/domains"); w.Code != http.StatusNotFound {
		t.Errorf("unknown device: status %d", w.Code)
	}
	if w := get(t, h, "/api/devices/x/domains?days=3"); w.Code != http.StatusBadRequest {
		t.Errorf("days=3: status %d", w.Code)
	}
}
