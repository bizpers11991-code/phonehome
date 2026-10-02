package leases

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
	"github.com/bizpers11991-code/phonehome/internal/source"
)

var _ source.DeviceSource = (*File)(nil)

func ips(s ...string) []netip.Addr {
	out := make([]netip.Addr, len(s))
	for i, a := range s {
		out[i] = netip.MustParseAddr(a)
	}
	return out
}

func utc(s string) time.Time {
	t, err := time.Parse(time.DateTime, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestDevices(t *testing.T) {
	tests := []struct {
		file string
		want []model.Device
	}{
		{"dnsmasq.leases", []model.Device{
			{ID: "mac:24:a1:60:0a:0b:0c", MAC: "24:a1:60:0a:0b:0c", IPs: ips("192.168.1.60"), Hostname: "ESP_0A0B0C", Vendor: "Espressif"},
			{ID: "mac:b8:27:eb:aa:bb:cc", MAC: "b8:27:eb:aa:bb:cc", IPs: ips("192.168.1.2"), Hostname: "pihole", Vendor: "Raspberry Pi"},
			{ID: "mac:cc:6e:a4:12:34:56", MAC: "cc:6e:a4:12:34:56", IPs: ips("192.168.1.20"), Hostname: "Samsung-TV", Vendor: "Samsung"},
			{ID: "mac:da:a1:19:5e:7f:01", MAC: "da:a1:19:5e:7f:01", IPs: ips("192.168.1.33")}, // private address
			{ID: "mac:f0:27:2d:01:02:03", MAC: "f0:27:2d:01:02:03", IPs: ips("192.168.1.41"), Hostname: "amazon-4f2a1b", Vendor: "Amazon"},
		}},
		// The Hue bridge's address (.51) was later leased to the ESP device,
		// so the Hue no longer appears; .54 was abandoned and has no MAC.
		{"dhcpd.leases", []model.Device{
			{ID: "mac:24:a1:60:0a:0b:0c", MAC: "24:a1:60:0a:0b:0c", IPs: ips("192.168.1.51"), Vendor: "Espressif", LastSeen: utc("2025-10-02 11:00:00")},
			{ID: "mac:38:f7:3d:11:22:33", MAC: "38:f7:3d:11:22:33", IPs: ips("192.168.1.50"), Hostname: `Kitchen "Echo"`, Vendor: "Amazon", LastSeen: utc("2025-10-02 10:30:00")},
			{ID: "mac:3c:22:fb:44:55:66", MAC: "3c:22:fb:44:55:66", IPs: ips("192.168.1.52"), Hostname: "MacBook-Air", Vendor: "Apple", LastSeen: utc("2025-10-02 12:00:00")},
			{ID: "mac:dc:a6:32:01:02:03", MAC: "dc:a6:32:01:02:03", IPs: ips("192.168.1.53"), Hostname: "café-pi", Vendor: "Raspberry Pi", LastSeen: utc("2025-10-02 06:30:00")},
		}},
		{"odhcpd", []model.Device{
			{ID: "mac:3c:22:fb:11:22:33", MAC: "3c:22:fb:11:22:33", IPs: ips("192.168.1.141", "192.168.1.140"), Hostname: "iPhone", Vendor: "Apple"},
			{ID: "mac:d8:a0:11:44:55:66", MAC: "d8:a0:11:44:55:66", IPs: ips("192.168.1.150"), Vendor: "WiZ"},
		}},
		{"empty.leases", []model.Device{}},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			got, err := New("testdata/" + tt.file).Devices(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

func TestDevicesErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := New("testdata/notleases.log").Devices(ctx); !errors.Is(err, errUnknownFormat) {
		t.Errorf("syslog file: err = %v, want errUnknownFormat", err)
	}
	if _, err := New("testdata/missing.leases").Devices(ctx); err == nil {
		t.Error("missing file: no error")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := New("testdata/dnsmasq.leases").Devices(cancelled); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled context: err = %v", err)
	}
}

func TestName(t *testing.T) {
	if got := New("/etc/pihole/dhcp.leases").Name(); got != "leases:dhcp.leases" {
		t.Errorf("Name() = %q", got)
	}
}

func TestParseDhcpdMalformed(t *testing.T) {
	tests := map[string]string{
		"unterminated block":     "lease 10.0.0.1 {\n  hardware ethernet 00:11:22:33:44:55;\n",
		"unterminated statement": "authoring-byte-order little-endian",
		"unterminated string":    "lease 10.0.0.1 {\n  client-hostname \"oops;\n}\n",
		"bad address":            "lease 10.0.0.300 {\n}\n",
	}
	for name, in := range tests {
		if _, err := parse([]byte(in)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestQuoted(t *testing.T) {
	tests := []struct{ in, want string }{
		{`"plain"`, "plain"},
		{`"say \"hi\""`, `say "hi"`},
		{`"back\\slash"`, `back\slash`},
		{`"caf\303\251"`, "café"},
		{`"\001<\"\373DUf"`, "\x01<\"\xfbDUf"},
	}
	for _, tt := range tests {
		got, n, err := quoted(tt.in)
		if err != nil || got != tt.want || n != len(tt.in) {
			t.Errorf("quoted(%s) = %q, %d, %v; want %q, %d", tt.in, got, n, err, tt.want, len(tt.in))
		}
	}
}

func TestDhcpdTime(t *testing.T) {
	tests := []struct {
		in   []string
		want time.Time
	}{
		{[]string{"4", "2025/10/02", "08:10:00"}, utc("2025-10-02 08:10:00")},
		{[]string{"epoch", "1759401000"}, utc("2025-10-02 10:30:00")},
		{[]string{"never"}, time.Time{}},
		{[]string{"epoch", "soon"}, time.Time{}},
		{[]string{"4", "2025-10-02", "08:10:00"}, time.Time{}},
	}
	for _, tt := range tests {
		if got := dhcpdTime(tt.in); !got.Equal(tt.want) {
			t.Errorf("dhcpdTime(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
