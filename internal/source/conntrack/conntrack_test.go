package conntrack

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
	"github.com/bizpers11991-code/phonehome/internal/source"
)

var _ source.FlowSource = (*Table)(nil)

var t0 = time.Date(2025, 10, 2, 8, 0, 0, 0, time.UTC)

func flow(proto, client, remote string, port uint16, out, in uint64, start time.Time, src string) model.Flow {
	return model.Flow{
		Start:      start,
		ClientIP:   netip.MustParseAddr(client),
		RemoteIP:   netip.MustParseAddr(remote),
		RemotePort: port,
		Proto:      proto,
		BytesOut:   out,
		BytesIn:    in,
		Source:     src,
	}
}

func fixedClock() func() time.Time { return func() time.Time { return t0 } }

func TestFetchFlowsFormats(t *testing.T) {
	const proc = "conntrack:nf_conntrack"
	tests := []struct {
		name string
		path string
		opts []Option
		want []model.Flow
	}{
		{
			name: "proc with accounting",
			path: "testdata/nf_conntrack",
			want: []model.Flow{
				flow("tcp", "192.168.1.20", "34.117.59.81", 443, 3456, 7890, t0, proc),
				flow("udp", "192.168.1.41", "8.8.8.8", 53, 72, 136, t0, proc),
				flow("tcp", "192.168.1.60", "52.29.124.10", 8883, 60, 0, t0, proc),
				flow("tcp", "fd00::1c2b:3cff:fe4d:5e6f", "2607:f8b0:4005:80b::200e", 443, 4810, 20511, t0, proc),
			},
		},
		{
			name: "proc with delegated IPv6 prefix",
			path: "testdata/nf_conntrack",
			opts: []Option{WithLocalPrefixes(netip.MustParsePrefix("2001:db8:1234::/48"))},
			want: []model.Flow{
				flow("tcp", "192.168.1.20", "34.117.59.81", 443, 3456, 7890, t0, proc),
				flow("udp", "192.168.1.41", "8.8.8.8", 53, 72, 136, t0, proc),
				flow("tcp", "192.168.1.60", "52.29.124.10", 8883, 60, 0, t0, proc),
				flow("tcp", "fd00::1c2b:3cff:fe4d:5e6f", "2607:f8b0:4005:80b::200e", 443, 4810, 20511, t0, proc),
				flow("udp", "2001:db8:1234:5678::50", "2a00:1450:4001:830::200e", 443, 9000, 81000, t0, proc),
			},
		},
		{
			name: "conntrack -L without accounting",
			path: "testdata/conntrack-L.txt",
			want: []model.Flow{
				flow("tcp", "192.168.1.41", "52.94.233.129", 443, 0, 0, t0, "conntrack:conntrack-L.txt"),
				flow("udp", "192.168.1.60", "129.6.15.28", 123, 0, 0, t0, "conntrack:conntrack-L.txt"),
				flow("tcp", "10.0.0.15", "17.253.144.10", 80, 0, 0, t0, "conntrack:conntrack-L.txt"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tab := New(tt.path, append(tt.opts, WithClock(fixedClock()))...)
			got, cursor, err := tab.FetchFlows(context.Background(), "", 0)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got\n%+v\nwant\n%+v", got, tt.want)
			}
			if want := t0.Format(time.RFC3339Nano); cursor != want {
				t.Errorf("cursor = %q, want %q", cursor, want)
			}
		})
	}
}

// snapshots serves the named files in turn, one per poll.
type snapshots struct {
	files []string
	polls int
}

func (s *snapshots) open(context.Context) (io.ReadCloser, error) {
	f, err := os.Open(s.files[s.polls])
	s.polls++
	return f, err
}

func TestFetchFlowsAcrossPolls(t *testing.T) {
	snaps := &snapshots{files: []string{"testdata/poll1.txt", "testdata/poll2.txt", "testdata/poll3.txt", "testdata/poll4.txt"}}
	now := t0
	tab := New("nf_conntrack", WithReader(snaps.open), WithClock(func() time.Time { return now }))
	const src = "conntrack:nf_conntrack"

	steps := []struct {
		name string
		want []model.Flow
	}{
		{"first poll reports everything open", []model.Flow{
			flow("tcp", "192.168.1.20", "34.117.59.81", 443, 3456, 7890, t0, src),
			flow("tcp", "192.168.1.41", "52.94.233.129", 443, 517, 4110, t0, src),
		}},
		{"still-open connections are not repeated", []model.Flow{
			flow("udp", "192.168.1.41", "8.8.8.8", 53, 72, 136, t0.Add(time.Minute), src),
		}},
		{"nothing new", nil},
		{"a closed connection that reappears is new", []model.Flow{
			flow("tcp", "192.168.1.41", "52.94.233.129", 443, 60, 0, t0.Add(3*time.Minute), src),
		}},
	}
	cursor := ""
	for i, step := range steps {
		now = t0.Add(time.Duration(i) * time.Minute)
		got, next, err := tab.FetchFlows(context.Background(), cursor, 100)
		if err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if !reflect.DeepEqual(got, step.want) {
			t.Errorf("%s: got\n%+v\nwant\n%+v", step.name, got, step.want)
		}
		if step.want == nil && next != cursor {
			t.Errorf("%s: cursor moved from %q to %q", step.name, cursor, next)
		}
		if step.want != nil && next != now.Format(time.RFC3339Nano) {
			t.Errorf("%s: cursor = %q, want poll time", step.name, next)
		}
		cursor = next
	}
}

func TestFetchFlowsLimit(t *testing.T) {
	snaps := &snapshots{files: []string{"testdata/nf_conntrack", "testdata/nf_conntrack"}}
	tab := New("nf_conntrack", WithReader(snaps.open), WithClock(fixedClock()))
	ctx := context.Background()

	var all []model.Flow
	for range 2 {
		got, _, err := tab.FetchFlows(ctx, "", 3)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, got...)
	}
	if len(all) != 4 || snaps.polls != 1 {
		t.Fatalf("got %d flows in %d polls, want 4 flows from 1 poll", len(all), snaps.polls)
	}
	if all[3].RemoteIP != netip.MustParseAddr("2607:f8b0:4005:80b::200e") {
		t.Errorf("flows out of order: %+v", all)
	}
	got, _, err := tab.FetchFlows(ctx, "", 3)
	if err != nil || got != nil || snaps.polls != 2 {
		t.Errorf("after draining: got %v, %v after %d polls; want nil after a second poll", got, err, snaps.polls)
	}
}

func TestFetchFlowsErrors(t *testing.T) {
	boom := errors.New("boom")
	tab := New("x", WithReader(func(context.Context) (io.ReadCloser, error) { return nil, boom }))
	if _, cursor, err := tab.FetchFlows(context.Background(), "c", 10); !errors.Is(err, boom) || cursor != "c" {
		t.Errorf("reader error: cursor %q, err %v", cursor, err)
	}
	if _, _, err := New("testdata/missing").FetchFlows(context.Background(), "", 10); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file: err = %v", err)
	}
}

func TestName(t *testing.T) {
	if got := New(DefaultPath).Name(); got != "conntrack:nf_conntrack" {
		t.Errorf("Name() = %q", got)
	}
	if got := New("", Command("/usr/sbin/conntrack", "-L", "-o", "extended")).Name(); got != "conntrack:conntrack" {
		t.Errorf("Name() with Command = %q", got)
	}
}

func TestParseEntry(t *testing.T) {
	tcp := tuple{
		proto: "tcp",
		src:   netip.MustParseAddr("192.168.1.20"), dst: netip.MustParseAddr("34.117.59.81"),
		sport: 50412, dport: 443,
	}
	tests := []struct {
		name string
		line string
		want entry
		ok   bool
	}{
		{"extended", "ipv4     2 tcp      6 431999 ESTABLISHED src=192.168.1.20 dst=34.117.59.81 sport=50412 dport=443 packets=12 bytes=3456 src=34.117.59.81 dst=203.0.113.7 sport=443 dport=50412 packets=10 bytes=7890 [ASSURED] mark=0 use=2",
			entry{orig: tcp, bytesOut: 3456, bytesIn: 7890}, true},
		{"plain", "tcp      6 431999 ESTABLISHED src=192.168.1.20 dst=34.117.59.81 sport=50412 dport=443 src=34.117.59.81 dst=203.0.113.7 sport=443 dport=50412 [ASSURED] mark=0 use=1",
			entry{orig: tcp}, true},
		{"ipv4-mapped", "tcp 6 10 ESTABLISHED src=::ffff:192.168.1.20 dst=::ffff:34.117.59.81 sport=50412 dport=443",
			entry{orig: tcp}, true},
		{"icmp", "ipv4     2 icmp     1 29 src=192.168.1.20 dst=1.1.1.1 type=8 code=0 id=1234 src=1.1.1.1 dst=192.168.1.20 type=0 code=0 id=1234 mark=0 use=1", entry{}, false},
		{"sctp", "ipv4     2 sctp     132 10 ESTABLISHED src=192.168.1.20 dst=34.117.59.81 sport=1 dport=2", entry{}, false},
		{"summary", "conntrack v1.4.7 (conntrack-tools): 42 flow entries have been shown.", entry{}, false},
		{"bad port", "tcp 6 10 ESTABLISHED src=192.168.1.20 dst=34.117.59.81 sport=99999 dport=443", entry{}, false},
		{"bad address", "tcp 6 10 ESTABLISHED src=192.168.1 dst=34.117.59.81 sport=1 dport=443", entry{}, false},
		{"bad bytes", "tcp 6 10 ESTABLISHED src=192.168.1.20 dst=34.117.59.81 sport=1 dport=443 packets=1 bytes=x", entry{}, false},
		{"truncated", "tcp 6 10 ESTABLISHED src=192.168.1.20", entry{}, false},
		{"family only", "ipv4", entry{}, false},
		{"empty", "", entry{}, false},
	}
	for _, tt := range tests {
		got, ok := parseEntry(tt.line)
		if ok != tt.ok || got != tt.want {
			t.Errorf("%s: got %+v, %v; want %+v, %v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
}

func TestOutbound(t *testing.T) {
	tab := New(DefaultPath)
	tests := []struct {
		src, dst string
		want     bool
	}{
		{"192.168.1.20", "34.117.59.81", true},
		{"10.1.2.3", "1.1.1.1", true},
		{"172.16.5.5", "9.9.9.9", true},
		{"169.254.10.1", "8.8.8.8", true},
		{"fd00::1", "2606:4700:4700::1111", true},
		{"fe80::1", "2606:4700:4700::1111", true},
		{"192.168.1.20", "192.168.1.1", false},          // inside the LAN
		{"192.168.1.20", "100.101.102.103", false},      // CGNAT / Tailscale
		{"192.168.1.20", "224.0.0.251", false},          // multicast
		{"192.168.1.20", "255.255.255.255", false},      // broadcast
		{"203.0.113.7", "1.1.1.1", false},               // the router itself
		{"198.51.100.23", "203.0.113.7", false},         // inbound
		{"127.0.0.1", "1.1.1.1", false},                 // loopback
		{"2001:db8:1234::50", "2606:4700::1111", false}, // global IPv6 without WithLocalPrefixes
	}
	for _, tt := range tests {
		c := tuple{src: netip.MustParseAddr(tt.src), dst: netip.MustParseAddr(tt.dst)}
		if got := tab.outbound(c); got != tt.want {
			t.Errorf("outbound(%s → %s) = %v, want %v", tt.src, tt.dst, got, tt.want)
		}
	}
}
