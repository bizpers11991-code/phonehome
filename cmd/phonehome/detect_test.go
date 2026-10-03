package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/analyze"
	"github.com/bizpers11991-code/phonehome/internal/config"
	"github.com/bizpers11991-code/phonehome/internal/kb"
	"github.com/bizpers11991-code/phonehome/internal/store"
)

var (
	piholeProblem = config.Problem{Type: config.TypePiholeDB, Path: "/etc/pihole/pihole-FTL.db",
		Err: "permission denied", Hint: `add group_add: ["1000"]`}
	conntrackProblem = config.Problem{Type: config.TypeConntrack, Path: "/proc/net/nf_conntrack",
		Err: "permission denied", Hint: "optional", Optional: true}
	piholeSource = config.Source{Type: config.TypePiholeDB, Name: config.TypePiholeDB, Path: "/etc/pihole/pihole-FTL.db"}
)

func TestRedetectStopsOnceDNSIsFound(t *testing.T) {
	results := []config.Detection{
		{},
		{Problems: []config.Problem{piholeProblem}},
		{Sources: []config.Source{{Type: config.TypeConntrack, Name: config.TypeConntrack, Path: "/proc/net/nf_conntrack"}}},
		{Sources: []config.Source{piholeSource}},
		{}, // never asked for: the found set is kept
	}
	calls := 0
	var applied []config.Detection
	done := make(chan struct{})
	go func() {
		defer close(done)
		redetect(context.Background(), time.Millisecond, config.Detection{},
			func() config.Detection { calls++; return results[calls-1] },
			func(prev, next config.Detection) bool {
				applied = append(applied, next)
				return len(applied) != 2 // the first try at the problem fails, so it is retried
			})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("redetect did not stop after finding a DNS source")
	}
	if calls != 4 || len(applied) != 4 || !applied[3].HasDNS() {
		t.Errorf("calls = %d, applied = %+v", calls, applied)
	}
}

func TestRedetectHonoursCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	redetect(ctx, time.Hour, config.Detection{}, func() config.Detection {
		t.Error("detect called after cancel")
		return config.Detection{}
	}, func(_, _ config.Detection) bool { return true })
}

func TestLogDetectionLogsChangesOnly(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	first := config.Detection{Problems: []config.Problem{piholeProblem, conntrackProblem}}
	logDetection(log, config.Detection{}, first)
	out := buf.String()
	for _, want := range []string{
		`level=WARN msg="source found but not readable" type=pihole-db path=/etc/pihole/pihole-FTL.db err="permission denied"`,
		`level=INFO msg="optional source not readable" type=conntrack`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}

	buf.Reset()
	logDetection(log, first, first)
	if buf.Len() != 0 {
		t.Errorf("unchanged detection logged:\n%s", buf.String())
	}

	logDetection(log, first, config.Detection{Sources: []config.Source{piholeSource}, Problems: []config.Problem{conntrackProblem}})
	if out := buf.String(); !strings.Contains(out, `msg="auto-detected source" type=pihole-db`) || strings.Contains(out, "not readable") {
		t.Errorf("after the fix:\n%s", out)
	}
}

func TestPrintSetup(t *testing.T) {
	det := config.Detection{Problems: []config.Problem{piholeProblem, conntrackProblem}}
	s := newSetup(nil, &det, time.Now())
	if !s.AutoDetect || len(s.Problems) != 2 || s.Problems[0].Problem != "permission denied" {
		t.Fatalf("setup = %+v", s)
	}

	var b strings.Builder
	printSetup(&b, s, true)
	out := b.String()
	for _, want := range []string{
		"Setup:",
		"! found /etc/pihole/pihole-FTL.db but permission denied:\n    add group_add",
		"/proc/net/nf_conntrack (optional)",
		"No DNS source is in use yet",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	// With data, optional sources are not mentioned.
	b.Reset()
	printSetup(&b, newSetup([]config.Source{piholeSource}, &config.Detection{Problems: []config.Problem{conntrackProblem}}, time.Now()), false)
	if b.Len() != 0 {
		t.Errorf("nagged about an optional source:\n%s", b.String())
	}

	// Configured sources: nothing detected, nothing to say.
	s = newSetup([]config.Source{{Type: config.TypePiholeAPI, URL: "http://pi.hole"}}, nil, time.Now())
	if s.AutoDetect || len(s.Sources) != 1 || s.Sources[0].Location != "http://pi.hole" {
		t.Errorf("configured setup = %+v", s)
	}
}

// TestWatchSourcesPicksUpNewFile runs the serve-time re-detection against a
// real store: a query log that appears later is found and ingested, and the
// status the dashboard reads follows along.
func TestWatchSourcesPicksUpNewFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "querylog.json")

	st, err := store.Open(filepath.Join(dir, "phonehome.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := config.Default()
	cfg.Interval = 10 * time.Millisecond
	a := newApp(st, kb.Default(), analyze.Options{}, false)
	setup := &setupState{}
	a.setup = setup
	g := &ingester{cfg: cfg, st: st, app: a, log: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))}
	if err := g.start(ctx, nil); err != nil {
		t.Fatal(err)
	}
	defer g.halt()

	// The fake filesystem: the log is unreadable until the test "fixes" it.
	var mu sync.Mutex
	fixed := false
	detect := func() config.Detection {
		mu.Lock()
		defer mu.Unlock()
		if !fixed {
			return config.Detection{Problems: []config.Problem{{Type: config.TypeAdGuardQueryLog, Path: logPath, Err: "permission denied", Hint: "run as root"}}}
		}
		return config.Detection{Sources: []config.Source{{Type: config.TypeAdGuardQueryLog, Name: config.TypeAdGuardQueryLog, Path: logPath}}}
	}
	setup.set(newSetup(nil, &config.Detection{}, time.Now()))
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		g.watchSources(ctx, 5*time.Millisecond, config.Detection{}, detect, setup)
	}()

	waitFor(t, "the problem to reach the status", func() bool {
		s, _ := a.Status(ctx)
		return len(s.Setup.Problems) == 1 && s.Setup.Problems[0].Hint == "run as root"
	})

	line := `{"IP":"192.168.1.30","T":"` + time.Now().Add(-time.Minute).Format(time.RFC3339Nano) +
		`","QH":"www.example.com","QT":"A","QC":"IN","CP":"","Result":{},"Elapsed":250000}` + "\n"
	if err := os.WriteFile(logPath, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	fixed = true
	mu.Unlock()

	select {
	case <-watched: // stopped looking once a DNS source was found
	case <-time.After(5 * time.Second):
		t.Fatal("watchSources kept looking after finding the query log")
	}
	waitFor(t, "the query log to be ingested", func() bool {
		s, _ := a.Status(ctx)
		for _, src := range s.Sources {
			if src.Name == config.TypeAdGuardQueryLog && src.Records == 1 {
				return true
			}
		}
		return false
	})
	s, err := a.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Setup.Problems) != 0 || len(s.Setup.Sources) != 1 || s.Setup.Sources[0].Location != logPath {
		t.Errorf("setup = %+v", s.Setup)
	}
	r, err := a.Report(ctx, lastDays(1))
	if err != nil || r.Total != 1 {
		t.Errorf("report total = %d, %v", r.Total, err)
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
