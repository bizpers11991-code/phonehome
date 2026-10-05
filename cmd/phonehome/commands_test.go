package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/analyze"
	"github.com/bizpers11991-code/phonehome/internal/config"
	"github.com/bizpers11991-code/phonehome/internal/kb"
	"github.com/bizpers11991-code/phonehome/internal/model"
)

func TestLoadConfigNamedFileMustExist(t *testing.T) {
	t.Setenv("PHONEHOME_CONFIG", "")
	missing := filepath.Join(t.TempDir(), "nope.yaml")
	if _, _, err := loadConfig(missing); err == nil || !strings.Contains(err.Error(), "--config "+missing+": no such file") {
		t.Errorf("--config: err = %v", err)
	}
	t.Setenv("PHONEHOME_CONFIG", missing)
	if _, _, err := loadConfig(""); err == nil || !strings.Contains(err.Error(), "$PHONEHOME_CONFIG "+missing) {
		t.Errorf("$PHONEHOME_CONFIG: err = %v", err)
	}

	// The Docker image's default stays quiet when nothing is mounted there.
	if _, err := os.Stat("/config/phonehome.yaml"); err == nil {
		t.Skip("/config/phonehome.yaml exists here")
	}
	t.Setenv("PHONEHOME_DB", filepath.Join(t.TempDir(), "phonehome.db"))
	if _, _, err := loadConfig("/config/phonehome.yaml"); err != nil {
		t.Errorf("Docker default: %v", err)
	}
}

// TestConfiguredSourceProblems: a configured file that is missing or
// unreadable gets the hints auto-detection gives, in the source's error
// and in the setup status.
func TestConfiguredSourceProblems(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db := filepath.Join(dir, "pihole-FTL.db")
	srcs, err := buildSources([]config.Source{{Type: config.TypePiholeDB, Name: config.TypePiholeDB, Path: db}})
	if err != nil {
		t.Fatal(err)
	}
	defer srcs.Close()
	if _, _, err := srcs.dns[0].FetchDNS(ctx, "", 10); err == nil || !strings.Contains(err.Error(), db+" does not exist: check the path") {
		t.Errorf("missing database: err = %v", err)
	}
	if _, err := srcs.devices[0].Devices(ctx); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("missing database, devices: err = %v", err)
	}

	logPath := filepath.Join(dir, "querylog.json")
	a := newApp(nil, kb.Default(), analyze.Options{}, false)
	a.setup = &setupState{}
	a.setup.set(newSetup([]config.Source{{Type: config.TypeAdGuardQueryLog, Path: logPath}}, nil, time.Now()))
	if s := a.setup.get(); len(s.Problems) != 1 || !s.Problems[0].Missing {
		t.Fatalf("setup = %+v", s)
	}
	var b strings.Builder
	printSetup(&b, a.setup.get(), true)
	if !strings.Contains(b.String(), "! "+logPath+" does not exist:\n    AdGuard Home creates it") {
		t.Errorf("printSetup:\n%s", b.String())
	}
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// Status checks again, so a fixed file shows at once.
	if s := a.statusSetup(); len(s.Problems) != 0 {
		t.Errorf("after creating the file: %+v", s.Problems)
	}

	if os.Geteuid() == 0 {
		t.Skip("root reads files regardless of their mode")
	}
	if err := os.Chmod(logPath, 0); err != nil {
		t.Fatal(err)
	}
	if s := a.statusSetup(); len(s.Problems) != 1 || s.Problems[0].Problem != "permission denied" {
		t.Errorf("unreadable file: %+v", s.Problems)
	}
	srcs, err = buildSources([]config.Source{{Type: config.TypeDnsmasqLog, Name: config.TypeDnsmasqLog, Path: logPath}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := srcs.dns[0].FetchDNS(ctx, "", 10); err == nil || !strings.Contains(err.Error(), "found "+logPath+" but permission denied: run phonehome with") {
		t.Errorf("unreadable log: err = %v", err)
	}
}

func TestHasLookups(t *testing.T) {
	if err := hasLookups(model.HomeReport{}, "", 7); err == nil ||
		err.Error() != "no lookups in the last 7 days; check your sources with `phonehome report`" {
		t.Errorf("empty home: %v", err)
	}
	r := model.HomeReport{Total: 5, Devices: []model.DeviceReport{
		{Device: model.Device{ID: "ip:192.168.1.2", Label: "TV"}, Total: 5},
		{Device: model.Device{ID: "ip:192.168.1.3", Label: "Plug"}},
	}}
	if err := hasLookups(r, "", 7); err != nil {
		t.Errorf("home with lookups: %v", err)
	}
	if err := hasLookups(r, "ip:192.168.1.2", 7); err != nil {
		t.Errorf("device with lookups: %v", err)
	}
	if err := hasLookups(r, "ip:192.168.1.3", 1); err == nil || !strings.HasPrefix(err.Error(), "no lookups from Plug in the last 1 days") {
		t.Errorf("quiet device: %v", err)
	}
}

func TestCheckDays(t *testing.T) {
	for _, d := range []int{-5, 0, config.MaxRetentionDays + 1} {
		if err := checkDays(d); err == nil || !strings.Contains(err.Error(), "use a number of days from 1") {
			t.Errorf("checkDays(%d) = %v", d, err)
		}
	}
	for _, d := range []int{1, 7, config.MaxRetentionDays} {
		if err := checkDays(d); err != nil {
			t.Errorf("checkDays(%d) = %v", d, err)
		}
	}
	if err := cmdReport(context.Background(), []string{"--demo", "--days", "0"}); err == nil {
		t.Error("report --days 0 accepted")
	}
}

func TestListenBusyPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = listenAndServe(ctx, ln.Addr().String(), http.NotFoundHandler())
	if err == nil || !strings.Contains(err.Error(), ln.Addr().String()+" is already in use; is phonehome already running? Choose another address with --listen") {
		t.Errorf("err = %v", err)
	}
}

func TestDemoListen(t *testing.T) {
	env := func(v string) func(string) string { return func(string) string { return v } }
	if got := demoListen(env("")); got != "127.0.0.1:8099" {
		t.Errorf("no env: %q", got)
	}
	if got := demoListen(env(":8099")); got != ":8099" {
		t.Errorf("PHONEHOME_LISTEN: %q", got)
	}
}

func TestBuildVersion(t *testing.T) {
	info := func(v string) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) { return &debug.BuildInfo{Main: debug.Module{Version: v}}, true }
	}
	for _, c := range []struct{ ldflags, module, want string }{
		{"dev", "v0.3.0", "v0.3.0"},     // go install …@v0.3.0
		{"dev", "(devel)", "dev"},       // local checkout
		{"dev", "", "dev"},              // no module version
		{"v0.3.0", "(devel)", "v0.3.0"}, // release build
	} {
		if got := buildVersion(c.ldflags, info(c.module)); got != c.want {
			t.Errorf("buildVersion(%q, %q) = %q, want %q", c.ldflags, c.module, got, c.want)
		}
	}
	if got := buildVersion("dev", func() (*debug.BuildInfo, bool) { return nil, false }); got != "dev" {
		t.Errorf("no build info: %q", got)
	}
}
