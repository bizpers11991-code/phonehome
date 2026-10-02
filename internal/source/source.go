// Package source defines how phonehome pulls observations out of the
// network gear people already run. Implementations live in subpackages
// (pihole, adguard, dnsmasq, conntrack, leases) and must not import the store.
package source

import (
	"context"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

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
