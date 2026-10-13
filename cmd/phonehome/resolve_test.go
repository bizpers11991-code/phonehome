package main

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

func TestResolveDevice(t *testing.T) {
	dev := func(id, mac, host string, ips ...string) model.DeviceReport {
		d := model.Device{ID: id, MAC: mac, Hostname: host}
		for _, ip := range ips {
			d.IPs = append(d.IPs, netip.MustParseAddr(ip))
		}
		return model.DeviceReport{Device: d}
	}
	r := model.HomeReport{Devices: []model.DeviceReport{
		dev("mac:aa:bb:cc:00:11:22", "aa:bb:cc:00:11:22", "Living-TV", "192.168.1.20", "fd00::20"),
		dev("ip:192.168.1.30", "", "", "192.168.1.30"),
		dev("mac:aa:bb:cc:00:11:33", "aa:bb:cc:00:11:33", "twin", "192.168.1.41"),
		dev("mac:aa:bb:cc:00:11:44", "aa:bb:cc:00:11:44", "TWIN", "192.168.1.42"),
	}}
	for q, want := range map[string]string{
		"mac:aa:bb:cc:00:11:22": "mac:aa:bb:cc:00:11:22", // ID
		"living-tv":             "mac:aa:bb:cc:00:11:22", // name, any case
		"192.168.1.20":          "mac:aa:bb:cc:00:11:22", // IP of a MAC-keyed device
		"FD00:0:0:0:0:0:0:20":   "mac:aa:bb:cc:00:11:22", // same address, spelled differently
		"AA:BB:CC:00:11:22":     "mac:aa:bb:cc:00:11:22", // bare MAC, any case
		"192.168.1.30":          "ip:192.168.1.30",
		"192.168.1.42":          "mac:aa:bb:cc:00:11:44",
	} {
		got, err := resolveDevice(r, q)
		if err != nil || got != want {
			t.Errorf("resolveDevice(%q) = %q, %v; want %q", q, got, err, want)
		}
	}

	_, err := resolveDevice(r, "twin")
	if err == nil || !strings.Contains(err.Error(), "matches more than one") ||
		!strings.Contains(err.Error(), "mac:aa:bb:cc:00:11:33") || !strings.Contains(err.Error(), "mac:aa:bb:cc:00:11:44") {
		t.Errorf("ambiguous name: %v", err)
	}
	if _, err := resolveDevice(r, "10.9.9.9"); err == nil || !strings.Contains(err.Error(), "no device") {
		t.Errorf("unknown device: %v", err)
	}
}
