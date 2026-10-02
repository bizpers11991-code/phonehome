package pihole

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/bizpers11991-code/phonehome/internal/model"
	"github.com/bizpers11991-code/phonehome/internal/source"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// DB reads Pi-hole's long-term database, pihole-FTL.db (usually
// /etc/pihole/pihole-FTL.db), as a source.DNSSource and source.DeviceSource.
//
// The file is opened read-only while FTL keeps writing to it. FTL buffers
// queries in memory and flushes them to disk every DBINTERVAL
// (database.DBinterval in v6, 60 seconds by default), so lookups show up here
// up to a minute late. Both the Pi-hole v5 schema (a plain queries table)
// and the newer one (a queries view over query_storage and the *_by_id
// tables) are supported, since both expose the same queries columns.
//
// The cursor is the id of the last query read.
type DB struct {
	db   *sql.DB
	name string
}

// NewDB prepares to read the FTL database at path. The file is not touched
// until the first call, so Pi-hole may create it later.
func NewDB(path string, opts ...Option) (*DB, error) {
	o := applyOptions("pihole", opts)
	// A file: URI makes SQLite honour mode=ro: it then never creates,
	// migrates or writes the database. busy_timeout rides out FTL's writes;
	// query_only is belt and braces.
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("pihole: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	// Reopen the file for every query instead of keeping an idle
	// connection: a connection kept open across a replacement of the
	// database (FTL recreates it if it is deleted) would read the old,
	// unlinked file forever. Opening costs well under a millisecond.
	db.SetMaxIdleConns(0)
	return &DB{db: db, name: o.name}, nil
}

// Name implements source.DNSSource and source.DeviceSource.
func (d *DB) Name() string { return d.name }

// Close releases the database handle.
func (d *DB) Close() error { return d.db.Close() }

// FetchDNS implements source.DNSSource. Rows FTL could not attribute to a
// client IP or domain are skipped but still advance the cursor, so a call
// may return fewer than limit records even when more are waiting.
//
// If the newest id in the database is below the cursor, the database was
// replaced (deleted to recover from corruption, or restored from an older
// backup) and its ids started again; reading then restarts from the first
// row.
func (d *DB) FetchDNS(ctx context.Context, cursor string, limit int) ([]model.DNSQuery, string, error) {
	var last int64
	if cursor != "" {
		var err error
		if last, err = strconv.ParseInt(cursor, 10, 64); err != nil || last < 0 {
			return nil, cursor, fmt.Errorf("pihole: bad cursor %q: %w", cursor, source.ErrBadCursor)
		}
	}
	if limit <= 0 {
		return nil, cursor, nil
	}
	out, next, err := d.fetch(ctx, last, limit)
	if err != nil {
		return nil, cursor, err
	}
	if next == last && last > 0 {
		var newest sql.NullInt64
		if err := d.db.QueryRowContext(ctx, `SELECT id FROM queries ORDER BY id DESC LIMIT 1`).Scan(&newest); err != nil && err != sql.ErrNoRows {
			return nil, cursor, fmt.Errorf("pihole: read queries: %w", err)
		}
		if newest.Valid && newest.Int64 < last {
			out, next, err = d.fetch(ctx, 0, limit)
			if err != nil {
				return nil, cursor, err
			}
		}
	}
	if next == last {
		return nil, cursor, nil
	}
	return out, strconv.FormatInt(next, 10), nil
}

// fetch reads up to limit rows with ids above last and returns the
// lookups among them and the id of the last row read (last if none).
func (d *DB) fetch(ctx context.Context, last int64, limit int) ([]model.DNSQuery, int64, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT id, timestamp, type, status, domain, client
		   FROM queries WHERE id > ? ORDER BY id LIMIT ?`, last, limit)
	if err != nil {
		return nil, last, fmt.Errorf("pihole: read queries: %w", err)
	}
	defer rows.Close()

	var (
		out  []model.DNSQuery
		next = last
	)
	for rows.Next() {
		var (
			id             int64
			ts             float64
			typ, status    sql.NullInt64
			domain, client sql.NullString
		)
		if err := rows.Scan(&id, &ts, &typ, &status, &domain, &client); err != nil {
			return nil, last, fmt.Errorf("pihole: read queries: %w", err)
		}
		next = id
		ip, ok := parseAddr(client.String)
		dom := normalizeDomain(domain.String)
		if !ok || dom == "" {
			continue
		}
		out = append(out, model.DNSQuery{
			Time:     unixTime(ts),
			ClientIP: ip,
			Domain:   dom,
			QType:    queryTypeName(typ.Int64),
			Blocked:  statusBlocked(status.Int64),
			Source:   d.name,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, last, fmt.Errorf("pihole: read queries: %w", err)
	}
	return out, next, nil
}

// Devices implements source.DeviceSource using FTL's network table, which
// FTL fills from the kernel's neighbour cache and from client queries.
// Clients FTL never saw a MAC address for are stored with a pseudo hardware
// address "ip-<addr>" and become devices with ID "ip:<addr>".
func (d *DB) Devices(ctx context.Context) ([]model.Device, error) {
	byID := map[int64]*model.Device{}
	var devs []*model.Device

	rows, err := d.db.QueryContext(ctx,
		`SELECT id, hwaddr, firstSeen, lastQuery, macVendor FROM network`)
	if err != nil {
		return nil, fmt.Errorf("pihole: read network: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id                  int64
			hwaddr              string
			firstSeen, lastSeen sql.NullFloat64
			vendor              sql.NullString
		)
		if err := rows.Scan(&id, &hwaddr, &firstSeen, &lastSeen, &vendor); err != nil {
			return nil, fmt.Errorf("pihole: read network: %w", err)
		}
		dev, ok := deviceFromHWAddr(hwaddr)
		if !ok {
			continue
		}
		dev.Vendor = strings.TrimSpace(vendor.String)
		dev.FirstSeen = unixTime(firstSeen.Float64)
		dev.LastSeen = unixTime(lastSeen.Float64)
		byID[id] = dev
		devs = append(devs, dev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pihole: read network: %w", err)
	}
	rows.Close() // free the single connection for the next query

	if err := d.addAddresses(ctx, byID); err != nil {
		return nil, err
	}

	out := make([]model.Device, len(devs))
	for i, dev := range devs {
		out[i] = *dev
	}
	slices.SortFunc(out, func(a, b model.Device) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// addAddresses attaches IPs and host names from network_addresses, most
// recently seen first. Databases older than FTL's schema version 8 have no
// name column there; they simply yield no host names.
func (d *DB) addAddresses(ctx context.Context, byID map[int64]*model.Device) error {
	var hasName bool
	if err := d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) > 0 FROM pragma_table_info('network_addresses') WHERE name = 'name'`,
	).Scan(&hasName); err != nil {
		return fmt.Errorf("pihole: read network_addresses: %w", err)
	}
	nameCol := "NULL"
	if hasName {
		nameCol = "name"
	}
	rows, err := d.db.QueryContext(ctx,
		`SELECT network_id, ip, lastSeen, `+nameCol+`
		   FROM network_addresses ORDER BY network_id, lastSeen DESC`)
	if err != nil {
		return fmt.Errorf("pihole: read network_addresses: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			netID    int64
			ip       string
			lastSeen sql.NullFloat64
			name     sql.NullString
		)
		if err := rows.Scan(&netID, &ip, &lastSeen, &name); err != nil {
			return fmt.Errorf("pihole: read network_addresses: %w", err)
		}
		dev := byID[netID]
		addr, ok := parseAddr(ip)
		if dev == nil || !ok {
			continue
		}
		if !slices.Contains(dev.IPs, addr) {
			dev.IPs = append(dev.IPs, addr)
		}
		if dev.Hostname == "" {
			dev.Hostname = strings.TrimSpace(name.String)
		}
		if t := unixTime(lastSeen.Float64); t.After(dev.LastSeen) {
			dev.LastSeen = t
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("pihole: read network_addresses: %w", err)
	}
	return nil
}

// deviceFromHWAddr derives a device's identity from FTL's hwaddr column. It
// rejects the all-zero address FTL uses for virtual interfaces and anything
// it cannot parse.
func deviceFromHWAddr(hw string) (*model.Device, bool) {
	hw = strings.ToLower(strings.TrimSpace(hw))
	if rest, ok := strings.CutPrefix(hw, "ip-"); ok {
		addr, ok := parseAddr(rest)
		if !ok {
			return nil, false
		}
		return &model.Device{ID: "ip:" + addr.String(), IPs: []netip.Addr{addr}}, true
	}
	mac, err := net.ParseMAC(hw)
	if err != nil || len(mac) != 6 || isZero(mac) {
		return nil, false
	}
	return &model.Device{ID: "mac:" + mac.String(), MAC: mac.String()}, true
}

func isZero(mac net.HardwareAddr) bool {
	return slices.Equal(mac, make(net.HardwareAddr, len(mac)))
}
