package pihole

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
	"github.com/bizpers11991-code/phonehome/internal/source"
)

// maxPage is FTL's cap on rows per /api/queries response
// (API_QUERIES_MAX_ROWS).
const maxPage = 10000

// settle is how old a query must be before it is read. FTL keeps updating
// a query's status after it arrives: a forwarded query becomes
// GRAVITY_CNAME when the reply's CNAME chain hits a blocked name, or
// EXTERNAL_BLOCKED_* when the upstream blocked it. FTL itself waits this
// long (REPLY_TIMEOUT) before writing a query to its database.
const settle = 30 * time.Second

// API reads DNS lookups from the Pi-hole v6 REST API as a source.DNSSource.
// Use it when phonehome cannot read pihole-FTL.db directly. It only sees
// FTL's in-memory history (24 hours by default), so phonehome must poll at
// least that often to miss nothing.
//
// Authenticate with an app password (Settings › Web interface / API ›
// Configure app password): it works without two-factor codes. The session is
// renewed automatically when it expires, and Close ends it, because FTL only
// allows a handful of concurrent sessions.
//
// Queries are read once they are 30 seconds old (by this machine's clock),
// when FTL has settled whether they were blocked.
//
// The cursor is "<id>@<time>": the FTL id and Unix time of the last query
// read. The API cannot filter by id, so the time is needed to ask only for
// newer queries; the id then settles ties within the same instant.
type API struct {
	base     string
	password string
	client   *http.Client
	name     string
	now      func() time.Time

	mu  sync.Mutex
	sid string
}

// NewAPI returns a reader for the Pi-hole at baseURL, the address of its web
// interface such as "http://pi.hole" (a trailing /admin or /api is ignored).
// An empty password means the Pi-hole has none set. A nil client uses one
// with a 30 second timeout; pass your own to trust a self-signed
// certificate.
func NewAPI(baseURL, password string, client *http.Client, opts ...Option) (*API, error) {
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("pihole: base URL %q must be http(s)://host", baseURL)
	}
	base := strings.TrimRight(u.String(), "/")
	base = strings.TrimSuffix(strings.TrimSuffix(base, "/api"), "/admin")
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	o := applyOptions("pihole-api", opts)
	return &API{base: base, password: password, client: client, name: o.name, now: time.Now}, nil
}

// Name implements source.DNSSource.
func (a *API) Name() string { return a.name }

// apiQuery is the subset of an /api/queries row phonehome uses.
type apiQuery struct {
	ID     int64   `json:"id"`
	Time   float64 `json:"time"`
	Type   string  `json:"type"`
	Status string  `json:"status"`
	Domain string  `json:"domain"`
	Client struct {
		IP string `json:"ip"`
	} `json:"client"`
}

// FetchDNS implements source.DNSSource. It asks FTL for queries at or after
// the cursor's time and settled, sorted oldest first, and drops the ones
// already seen.
func (a *API) FetchDNS(ctx context.Context, cursor string, limit int) ([]model.DNSQuery, string, error) {
	lastID, lastTime, err := parseAPICursor(cursor)
	if err != nil {
		return nil, cursor, err
	}
	if limit <= 0 {
		return nil, cursor, nil
	}

	var (
		out      []model.DNSQuery
		consumed bool
		page     = min(max(limit, 100), maxPage)
		until    = strconv.FormatFloat(float64(a.now().Add(-settle).UnixMicro())/1e6, 'f', -1, 64)
	)
	for start := 0; len(out) < limit; start += page {
		q := url.Values{
			"length":           {strconv.Itoa(page)},
			"start":            {strconv.Itoa(start)},
			"order[0][column]": {"0"},
			"order[0][dir]":    {"asc"},
			"columns[0][data]": {"time"},
			"until":            {until},
		}
		if cursor != "" {
			q.Set("from", strconv.FormatFloat(lastTime, 'f', -1, 64))
		}
		var resp struct {
			Queries []apiQuery `json:"queries"`
		}
		if err := a.do(ctx, http.MethodGet, "/api/queries?"+q.Encode(), nil, &resp); err != nil {
			return nil, cursor, err
		}
		rows := resp.Queries
		if !slices.IsSortedFunc(rows, func(x, y apiQuery) int { return cmp.Compare(x.Time, y.Time) }) {
			return nil, cursor, errors.New("pihole: API ignored the requested sort order; phonehome needs Pi-hole FTL v6.0 or newer")
		}
		// Within one instant the order is unspecified; ids settle it.
		slices.SortStableFunc(rows, func(x, y apiQuery) int {
			return cmp.Or(cmp.Compare(x.Time, y.Time), cmp.Compare(x.ID, y.ID))
		})
		for _, r := range rows {
			if r.ID <= lastID {
				continue
			}
			if len(out) == limit {
				break
			}
			lastID, lastTime, consumed = r.ID, r.Time, true
			ip, ok := parseAddr(r.Client.IP)
			dom := normalizeDomain(r.Domain)
			if !ok || dom == "" {
				continue
			}
			out = append(out, model.DNSQuery{
				Time:     unixTime(r.Time),
				ClientIP: ip,
				Domain:   dom,
				QType:    apiTypeName(r.Type),
				Blocked:  apiStatusBlocked(r.Status),
				Source:   a.name,
			})
		}
		if len(rows) < page {
			break
		}
	}
	if !consumed {
		return nil, cursor, nil
	}
	return out, strconv.FormatInt(lastID, 10) + "@" + strconv.FormatFloat(lastTime, 'f', -1, 64), nil
}

func parseAPICursor(c string) (id int64, t float64, err error) {
	if c == "" {
		return 0, 0, nil
	}
	ids, ts, ok := strings.Cut(c, "@")
	if ok {
		id, err = strconv.ParseInt(ids, 10, 64)
	}
	if ok && err == nil {
		t, err = strconv.ParseFloat(ts, 64)
	}
	if !ok || err != nil {
		return 0, 0, fmt.Errorf("pihole: bad API cursor %q: %w", c, source.ErrBadCursor)
	}
	return id, t, nil
}

// Close ends the API session, if any.
func (a *API) Close() error {
	a.mu.Lock()
	sid := a.sid
	a.sid = ""
	a.mu.Unlock()
	if sid == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := a.send(ctx, http.MethodDelete, "/api/auth", nil, sid)
	if err != nil {
		return err
	}
	resp.Body.Close()
	// 401: the session had already expired, which is just as good.
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusUnauthorized {
		return fmt.Errorf("pihole: logout: %s", resp.Status)
	}
	return nil
}

// do performs an authenticated request and decodes the JSON reply into v,
// logging in first if needed and once more if the session has expired.
func (a *API) do(ctx context.Context, method, path string, body, v any) error {
	for attempt := 0; ; attempt++ {
		sid, err := a.session(ctx)
		if err != nil {
			return err
		}
		resp, err := a.send(ctx, method, path, body, sid)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusUnauthorized && a.password != "" && attempt == 0 {
			resp.Body.Close()
			a.mu.Lock()
			if a.sid == sid {
				a.sid = ""
			}
			a.mu.Unlock()
			continue
		}
		return decode(resp, v)
	}
}

// session returns the current session id, logging in if there is none.
func (a *API) session(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sid != "" || a.password == "" {
		return a.sid, nil
	}
	resp, err := a.send(ctx, http.MethodPost, "/api/auth", map[string]string{"password": a.password}, "")
	if err != nil {
		return "", err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		return "", errors.New("pihole: login refused: check the app password")
	}
	var auth struct {
		Session struct {
			Valid bool   `json:"valid"`
			SID   string `json:"sid"`
		} `json:"session"`
	}
	if err := decode(resp, &auth); err != nil {
		return "", err
	}
	if !auth.Session.Valid || auth.Session.SID == "" {
		return "", errors.New("pihole: login refused: no session granted (is two-factor authentication on? use an app password)")
	}
	a.sid = auth.Session.SID
	return a.sid, nil
}

func (a *API) send(ctx context.Context, method, path string, body any, sid string) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if sid != "" {
		req.Header.Set("X-FTL-SID", sid)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pihole: %s %s: %w", method, req.URL.Path, err)
	}
	return resp, nil
}

// decode reads a JSON reply, turning FTL's error objects into Go errors.
func decode(resp *http.Response, v any) error {
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error struct {
				Key     string `json:"key"`
				Message string `json:"message"`
			} `json:"error"`
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if json.Unmarshal(b, &e) == nil && e.Error.Message != "" {
			return fmt.Errorf("pihole: %s %s: %s: %s", resp.Request.Method, resp.Request.URL.Path, resp.Status, e.Error.Message)
		}
		return fmt.Errorf("pihole: %s %s: %s", resp.Request.Method, resp.Request.URL.Path, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("pihole: %s %s: decode reply: %w", resp.Request.Method, resp.Request.URL.Path, err)
	}
	return nil
}

// maxNetworkDevices and maxNetworkAddresses lift /api/network/devices's
// defaults (10 devices, 3 addresses each) so every device is listed.
const (
	maxNetworkDevices   = 10000
	maxNetworkAddresses = 20
)

// apiNetworkDevice is a device in FTL's /api/network/devices reply, which
// mirrors the database's network and network_addresses tables.
type apiNetworkDevice struct {
	HWAddr    string           `json:"hwaddr"`
	FirstSeen float64          `json:"firstSeen"`
	LastQuery float64          `json:"lastQuery"`
	MACVendor string           `json:"macVendor"`
	IPs       []apiNetworkAddr `json:"ips"`
}

type apiNetworkAddr struct {
	IP       string  `json:"ip"`
	Name     string  `json:"name"`
	LastSeen float64 `json:"lastSeen"`
}

// Network returns a source.DeviceSource that lists the devices FTL knows
// (MAC address, vendor, addresses and host names) through the API, as DB
// does from the database's network table. Its name is the API source's
// name plus "-devices", so it never shares the DNS reader's cursor key.
func (a *API) Network() source.DeviceSource { return apiNetwork{a} }

type apiNetwork struct{ a *API }

func (n apiNetwork) Name() string { return n.a.name + "-devices" }

// Devices implements source.DeviceSource.
func (n apiNetwork) Devices(ctx context.Context) ([]model.Device, error) {
	var resp struct {
		Devices []apiNetworkDevice `json:"devices"`
	}
	q := url.Values{"max_devices": {strconv.Itoa(maxNetworkDevices)}, "max_addresses": {strconv.Itoa(maxNetworkAddresses)}}
	if err := n.a.do(ctx, http.MethodGet, "/api/network/devices?"+q.Encode(), nil, &resp); err != nil {
		return nil, err
	}
	var out []model.Device
	for _, nd := range resp.Devices {
		dev, ok := deviceFromHWAddr(nd.HWAddr)
		if !ok {
			continue
		}
		dev.Vendor = strings.TrimSpace(nd.MACVendor)
		dev.FirstSeen = unixTime(nd.FirstSeen)
		dev.LastSeen = unixTime(nd.LastQuery)
		ips := nd.IPs
		slices.SortStableFunc(ips, func(x, y apiNetworkAddr) int { return cmp.Compare(y.LastSeen, x.LastSeen) })
		for _, ip := range ips {
			addr, ok := parseAddr(ip.IP)
			if !ok {
				continue
			}
			if !slices.Contains(dev.IPs, addr) {
				dev.IPs = append(dev.IPs, addr)
			}
			if dev.Hostname == "" {
				dev.Hostname = strings.TrimSpace(ip.Name)
			}
			if t := unixTime(ip.LastSeen); t.After(dev.LastSeen) {
				dev.LastSeen = t
			}
		}
		out = append(out, *dev)
	}
	slices.SortFunc(out, func(x, y model.Device) int { return strings.Compare(x.ID, y.ID) })
	return out, nil
}
