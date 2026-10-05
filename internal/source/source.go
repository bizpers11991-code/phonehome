// Package source defines how phonehome pulls observations out of the
// network gear people already run. Implementations live in subpackages
// (pihole, adguard, dnsmasq, conntrack, leases) and must not import the store.
package source

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// ErrBadCursor is wrapped by the error a source returns for a cursor it
// cannot interpret: one saved by another kind of source under the same
// name, or damaged. The caller may start over from "".
var ErrBadCursor = errors.New("unreadable cursor")

// Domain normalises a queried name as the store keeps it: lowercase, no
// trailing dot. It returns "" for a name to skip: longer than DNS allows
// (253 bytes), not UTF-8, or containing a space or a control character.
// Resolvers log whatever a client sends, and such a name would otherwise
// reach the terminal of whoever runs `phonehome report`.
func Domain(name string) string {
	name = strings.TrimSuffix(name, ".")
	if len(name) > 253 || !utf8.ValidString(name) {
		return ""
	}
	for _, r := range name {
		if r <= ' ' || r >= 0x7f && r <= 0x9f { // C0, space, DEL, C1
			return ""
		}
	}
	return strings.ToLower(name)
}

// DNSSource yields DNS lookups incrementally.
//
// cursor is an opaque string previously returned by the source ("" on first
// run). FetchDNS returns records strictly newer than cursor, oldest first, at
// most limit of them, and the cursor to pass next time. When nothing is new it
// returns (nil, cursor, nil). Sources must be safe to call repeatedly and must
// never modify the system they read from.
type DNSSource interface {
	Name() string
	FetchDNS(ctx context.Context, cursor string, limit int) ([]model.DNSQuery, string, error)
}

// FlowSource yields outbound connections incrementally, same cursor contract.
type FlowSource interface {
	Name() string
	FetchFlows(ctx context.Context, cursor string, limit int) ([]model.Flow, string, error)
}

// DeviceSource returns its current view of devices on the network.
// Returned devices are merged by ID into the store; empty fields never
// overwrite known values, and Label is never set by a source.
type DeviceSource interface {
	Name() string
	Devices(ctx context.Context) ([]model.Device, error)
}
