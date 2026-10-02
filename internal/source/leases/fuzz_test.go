package leases

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzParse feeds arbitrary lease files to the format detector and all
// three parsers.
func FuzzParse(f *testing.F) {
	for _, name := range []string{"dnsmasq.leases", "dhcpd.leases", "odhcpd", "notleases.log", "empty.leases"} {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte(`lease 10.0.0.2 { hardware ethernet 00:11:22:33:44:55; client-hostname "a\"}{;\101"; }`))
	f.Fuzz(func(t *testing.T, data []byte) {
		ls, err := parse(data)
		if err != nil {
			return
		}
		for _, l := range ls {
			if l.MAC == "" || !l.IP.Is4() {
				t.Fatalf("bad lease %+v", l)
			}
		}
		seen := map[string]bool{}
		for _, d := range devices(ls) {
			if seen[d.ID] || d.ID != "mac:"+d.MAC || len(d.IPs) == 0 {
				t.Fatalf("bad device %+v", d)
			}
			seen[d.ID] = true
		}
	})
}
