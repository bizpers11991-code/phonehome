package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/analyze"
	"github.com/bizpers11991-code/phonehome/internal/config"
	"github.com/bizpers11991-code/phonehome/internal/demo"
	"github.com/bizpers11991-code/phonehome/internal/ingest"
	"github.com/bizpers11991-code/phonehome/internal/kb"
	"github.com/bizpers11991-code/phonehome/internal/model"
	"github.com/bizpers11991-code/phonehome/internal/source"
	"github.com/bizpers11991-code/phonehome/internal/source/adguard"
	"github.com/bizpers11991-code/phonehome/internal/source/conntrack"
	"github.com/bizpers11991-code/phonehome/internal/source/dnsmasq"
	"github.com/bizpers11991-code/phonehome/internal/source/leases"
	"github.com/bizpers11991-code/phonehome/internal/source/pihole"
	"github.com/bizpers11991-code/phonehome/internal/store"
	"github.com/bizpers11991-code/phonehome/internal/web"
	kbdata "github.com/bizpers11991-code/phonehome/kb"

	_ "time/tzdata" // honour `timezone:` on systems without a zoneinfo database
)

var logger = slog.New(slog.NewTextHandler(os.Stderr, nil))

// loadConfig finds and validates the configuration. A missing file is fine:
// defaults plus auto-detected sources get most Pi-hole users going. The
// detection is returned (and logged) when sources were auto-detected; it is
// nil when the config lists them.
func loadConfig(path string) (*config.Config, *config.Detection, error) {
	explicit := path != ""
	if !explicit {
		path = os.Getenv("PHONEHOME_CONFIG")
		explicit = path != ""
	}
	candidates := []string{path}
	if !explicit {
		candidates = []string{"phonehome.yaml", "/etc/phonehome/phonehome.yaml"}
	}

	cfg := config.Default()
	for _, p := range candidates {
		c, err := config.Load(p)
		if err == nil {
			cfg = c
			break
		}
		// The Docker image always passes --config; an absent file there just
		// means "use defaults".
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, nil, err
		}
	}
	cfg.ApplyEnv(os.Getenv)
	var det *config.Detection
	if len(cfg.Sources) == 0 {
		d := config.Detect(config.Check)
		det = &d
		cfg.Sources = d.Sources
		logDetection(logger, config.Detection{}, d)
	}
	if err := cfg.Validate(); err != nil {
		return nil, nil, fmt.Errorf("config:\n%w", err)
	}
	return cfg, det, nil
}

// sources turns config entries into readers. The returned closer releases
// open databases and API sessions.
type sources struct {
	dns     []source.DNSSource
	flows   []source.FlowSource
	devices []source.DeviceSource
	closers []io.Closer
	local   bool // a source reads files on this machine (so this host is likely the resolver)
}

func buildSources(list []config.Source) (_ *sources, err error) {
	s := &sources{}
	defer func() {
		if err != nil {
			s.Close()
		}
	}()
	for _, c := range list {
		switch c.Type {
		case config.TypePiholeDB:
			db, err := pihole.NewDB(c.Path, pihole.WithName(c.Name))
			if err != nil {
				return nil, err
			}
			s.dns, s.devices = append(s.dns, db), append(s.devices, db)
			s.closers = append(s.closers, db)
			s.local = true
		case config.TypePiholeAPI:
			pw, err := c.Secret()
			if err != nil {
				return nil, err
			}
			api, err := pihole.NewAPI(c.URL, pw, &http.Client{Timeout: 30 * time.Second}, pihole.WithName(c.Name))
			if err != nil {
				return nil, err
			}
			s.dns = append(s.dns, api)
			s.closers = append(s.closers, api)
		case config.TypeAdGuardQueryLog:
			s.dns = append(s.dns, adguard.NewQueryLog(c.Path, adguard.WithName(c.Name)))
			s.local = true
		case config.TypeDnsmasqLog:
			s.dns = append(s.dns, dnsmasq.NewLog(c.Path, dnsmasq.WithName(c.Name)))
			s.local = true
		case config.TypeLeases:
			s.devices = append(s.devices, leases.New(c.Path))
		case config.TypeConntrack:
			s.flows = append(s.flows, conntrack.New(c.Path))
		default:
			return nil, fmt.Errorf("source %q: unsupported type %q", c.Name, c.Type)
		}
	}
	return s, nil
}

func (s *sources) Close() {
	for _, c := range s.closers {
		c.Close()
	}
}

func analyzeOptions(cfg *config.Config, local bool) (analyze.Options, error) {
	loc, err := cfg.Location()
	if err != nil {
		return analyze.Options{}, err
	}
	o := analyze.Options{
		QuietStart: cfg.QuietHours.Start,
		QuietEnd:   cfg.QuietHours.End,
		Location:   loc,
		Resolvers:  cfg.ResolverAddrs(),
	}
	if local {
		o.Resolvers = append(o.Resolvers, hostAddrs()...)
	}
	return o, nil
}

// openStore opens the database and applies `labels:` from the config.
func openStore(ctx context.Context, cfg *config.Config) (*store.Store, error) {
	if dir := filepath.Dir(cfg.DB); dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("create data directory: %w", err)
		}
	}
	st, err := store.Open(cfg.DB)
	if err != nil {
		return nil, err
	}
	if err := applyLabels(ctx, st, cfg); err != nil {
		st.Close()
		return nil, err
	}
	return st, nil
}

// applyLabels copies `labels:` from the config into the store.
func applyLabels(ctx context.Context, st *store.Store, cfg *config.Config) error {
	for id, label := range cfg.DeviceLabels() {
		if err := st.SetLabel(ctx, id, label); err != nil {
			return err
		}
	}
	return nil
}

func cmdServe(ctx context.Context, args []string) error {
	fl := flag.NewFlagSet("serve", flag.ContinueOnError)
	cfgPath := fl.String("config", "", "config file")
	if err := fl.Parse(args); err != nil {
		return errUsage
	}
	cfg, det, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	pw, err := cfg.Auth.Secret()
	if err != nil {
		return err
	}
	st, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer st.Close()

	setup := &setupState{}
	setup.set(newSetup(cfg.Sources, det, time.Now()))
	a := newApp(st, kb.Default(), analyze.Options{}, false)
	a.setup = setup
	g := &ingester{cfg: cfg, st: st, app: a, log: logger}
	if err := g.start(ctx, cfg.Sources); err != nil {
		return err
	}
	defer g.halt()
	if det != nil && !det.HasDNS() {
		logger.Warn("no DNS source found yet; looking again every minute. The dashboard shows what was found and how to fix it",
			"every", redetectEvery)
		go g.watchSources(ctx, redetectEvery, *det, func() config.Detection { return config.Detect(config.Check) }, setup)
	}

	h := web.New(a, web.Options{
		Username: cfg.Auth.Username, Password: pw, Logger: logger, Metrics: cfg.Metrics,
	})
	return listenAndServe(ctx, cfg.Listen, h)
}

func cmdDemo(ctx context.Context, args []string) error {
	fl := flag.NewFlagSet("demo", flag.ContinueOnError)
	listen := fl.String("listen", "127.0.0.1:8099", "address to serve on")
	days := fl.Int("days", 30, "days of synthetic history")
	seed := fl.Int64("seed", 7, "random seed for the synthetic household")
	metrics := fl.Bool("metrics", false, "also serve /metrics for Prometheus")
	if err := fl.Parse(args); err != nil {
		return errUsage
	}
	st, err := loadDemo(ctx, *seed, *days)
	if err != nil {
		return err
	}
	defer st.Close()
	h := web.New(newApp(st, kb.Default(), analyze.DefaultOptions(), true), web.Options{Logger: logger, Metrics: *metrics})
	fmt.Fprintf(os.Stderr, "phonehome demo: a synthetic household — not a measurement.\nOpen http://%s\n", displayAddr(*listen))
	return listenAndServe(ctx, *listen, h)
}

// loadDemo fills an in-memory store with the synthetic household.
func loadDemo(ctx context.Context, seed int64, days int) (*store.Store, error) {
	st, err := store.Open(":memory:")
	if err != nil {
		return nil, err
	}
	h := demo.Generate(seed, time.Now(), days)
	if err := st.UpsertDevices(ctx, h.Devices); err != nil {
		return nil, err
	}
	for _, d := range h.Devices {
		if d.Label != "" {
			if err := st.SetLabel(ctx, d.ID, d.Label); err != nil {
				return nil, err
			}
		}
	}
	if err := st.InsertDNS(ctx, h.DNS); err != nil {
		return nil, err
	}
	if err := st.InsertFlows(ctx, h.Flows); err != nil {
		return nil, err
	}
	return st, st.RecordRun(ctx, model.SourceStatus{
		Name: demo.SourceName, Kind: "dns", LastRun: time.Now(), LastOK: time.Now(), Records: int64(len(h.DNS)),
	})
}

func listenAndServe(ctx context.Context, addr string, h http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	logger.Info("dashboard listening", "addr", addr)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	}
}

func displayAddr(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil || host == "" || host == "0.0.0.0" || host == "::" {
		return "localhost:" + port
	}
	return listen
}

func cmdIngest(ctx context.Context, args []string) error {
	fl := flag.NewFlagSet("ingest", flag.ContinueOnError)
	cfgPath := fl.String("config", "", "config file")
	once := fl.Bool("once", false, "ingest what is available and exit")
	if err := fl.Parse(args); err != nil {
		return errUsage
	}
	cfg, det, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	if det != nil && !det.HasDNS() {
		logger.Warn("no DNS source configured or found; see https://github.com/bizpers11991-code/phonehome/tree/main/docs/setup")
	}
	srcs, err := buildSources(cfg.Sources)
	if err != nil {
		return err
	}
	defer srcs.Close()
	st, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	runner := &ingest.Runner{
		Store: st, DNS: srcs.dns, Flows: srcs.flows, Devices: srcs.devices,
		Retention: cfg.Retention(), Logger: logger,
	}
	if *once {
		return runner.RunOnce(ctx)
	}
	runner.Run(ctx, cfg.Interval)
	return nil
}

// reportApp opens the configured store (or the demo household) for the
// one-shot report and receipt commands.
func reportApp(ctx context.Context, cfgPath string, useDemo bool) (*app, func(), error) {
	if useDemo {
		st, err := loadDemo(ctx, 7, 30)
		if err != nil {
			return nil, nil, err
		}
		return newApp(st, kb.Default(), analyze.DefaultOptions(), true), func() { st.Close() }, nil
	}
	cfg, det, err := loadConfig(cfgPath)
	if err != nil {
		return nil, nil, err
	}
	st, err := openStore(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	local := false
	for _, s := range cfg.Sources {
		local = local || s.Path != "" && s.Type != config.TypeLeases && s.Type != config.TypeConntrack
	}
	opts, err := analyzeOptions(cfg, local)
	if err != nil {
		st.Close()
		return nil, nil, err
	}
	a := newApp(st, kb.Default(), opts, false)
	a.setup = &setupState{}
	a.setup.set(newSetup(cfg.Sources, det, time.Now()))
	return a, func() { st.Close() }, nil
}

func cmdReport(ctx context.Context, args []string) error {
	fl := flag.NewFlagSet("report", flag.ContinueOnError)
	cfgPath := fl.String("config", "", "config file")
	days := fl.Int("days", 7, "period in days")
	useDemo := fl.Bool("demo", false, "report on the synthetic demo household")
	if err := fl.Parse(args); err != nil {
		return errUsage
	}
	a, done, err := reportApp(ctx, *cfgPath, *useDemo)
	if err != nil {
		return err
	}
	defer done()
	r, err := a.Report(ctx, lastDays(*days))
	if err != nil {
		return err
	}
	printReport(os.Stdout, r, *days)
	printSetup(os.Stdout, a.setup.get(), r.Total == 0)
	return nil
}

// printSetup explains, after a report, why sources may be missing: files
// auto-detection found but could not read, with how to fix each. Optional
// sources (leases, conntrack) are only mentioned when the report is empty,
// so that a working Pi-hole setup is not nagged on every run.
func printSetup(w io.Writer, s model.Setup, empty bool) {
	var lines []string
	for _, p := range s.Problems {
		if p.Optional && !empty {
			continue
		}
		note := ""
		if p.Optional {
			note = " (optional)"
		}
		lines = append(lines, fmt.Sprintf("  ! found %s%s but %s:\n    %s", p.Path, note, p.Problem, p.Hint))
	}
	dns := false
	for _, src := range s.Sources {
		dns = dns || config.IsDNSType(src.Type)
	}
	if empty && !dns {
		lines = append(lines, "  No DNS source is in use yet. Setup guides:\n"+
			"    https://github.com/bizpers11991-code/phonehome/tree/main/docs/setup")
	}
	if len(lines) == 0 {
		return
	}
	if !empty { // an empty report already ends with a blank line
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "Setup:")
	for _, l := range lines {
		fmt.Fprintln(w, l)
	}
}

func printReport(w io.Writer, r model.HomeReport, days int) {
	if r.Demo {
		fmt.Fprintln(w, "DEMO DATA — a synthetic household, not a measurement.")
		fmt.Fprintln(w)
	}
	share := 0
	if r.Total > 0 {
		share = r.Snooping * 100 / r.Total
	}
	fmt.Fprintf(w, "Your devices called home %s times in the last %d days. %d%% of that was about you.\n",
		thousands(r.Total), days, share)
	if r.Grade != "" {
		fmt.Fprintf(w, "Home grade: %s (as good as its worst device).\n", r.Grade)
	}
	since := ""
	if pc := r.Previous; pc != nil {
		since = previousLabel(pc.Period)
		fmt.Fprintf(w, "Compared with the %s: %s → %s snooping lookups/day (%s), home grade %s → %s.\n",
			since, thousands(round(pc.PerDay)), thousands(round(pc.NowPerDay)), changeText(pc.Comparison),
			gradeOrDash(pc.Grade), gradeOrDash(r.Grade))
		if pc.Partial {
			fmt.Fprintf(w, "  Only %.1f of those %.0f days have data; rates are per day of data.\n", pc.Days, pc.Period.Days())
		}
		for _, g := range pc.Gone {
			fmt.Fprintf(w, "  Not seen this period: %s (was %s)\n", g.Device.DisplayName(), gradeOrDash(g.Grade))
		}
	}
	fmt.Fprintln(w)
	for _, d := range r.Devices {
		fmt.Fprintf(w, "%s  %-28s %8s lookups  %7s snooping/day  (%s)\n",
			gradeOrDash(d.Grade), d.Device.DisplayName(), thousands(d.Total), thousands(round(d.PerDay)), d.Device.Kind)
		if c := d.Previous; c != nil {
			if !c.Seen {
				fmt.Fprintf(w, "     new: not seen in the %s\n", since)
			} else {
				fmt.Fprintf(w, "     vs %s: %s → %s snooping/day (%s), grade %s → %s\n", since,
					thousands(round(c.PerDay)), thousands(round(c.NowPerDay)), changeText(*c), gradeOrDash(c.Grade), gradeOrDash(d.Grade))
			}
			for _, h := range c.Stopped {
				fmt.Fprintf(w, "     stopped: %s heartbeat (%s)\n", h.Domain, h.Category.Label())
			}
			for _, h := range c.Started {
				fmt.Fprintf(w, "     new heartbeat: %s (%s)\n", h.Domain, h.Category.Label())
			}
		}
		cats := make([]model.Category, 0, len(d.ByCategory))
		for c, n := range d.ByCategory {
			if n > 0 && c.Snooping() {
				cats = append(cats, c)
			}
		}
		sort.Slice(cats, func(i, j int) bool { return d.ByCategory[cats[i]] > d.ByCategory[cats[j]] })
		for _, c := range cats {
			fmt.Fprintf(w, "     %-20s %8s\n", c.Label(), thousands(d.ByCategory[c]))
		}
		for _, h := range d.Heartbeats {
			if h.Category.Snooping() {
				fmt.Fprintf(w, "     heartbeat: %s every %s (%s)\n", h.Domain, h.Every.Round(time.Second), h.Category.Label())
			}
		}
		for _, b := range d.Bypasses {
			fmt.Fprintf(w, "     ! %s (%s)\n", b.Detail, b.Evidence)
		}
		if len(d.Fixes) > 0 {
			fmt.Fprintf(w, "     fix: %s\n", d.Fixes[0].Title)
		}
	}
}

// previousLabel names the period before a report: "previous 7 days".
func previousLabel(p model.Period) string {
	d := p.Days()
	switch {
	case d == 1:
		return "previous 24 hours"
	case d == math.Trunc(d):
		return fmt.Sprintf("previous %.0f days", d)
	}
	return fmt.Sprintf("previous %.1f days", d)
}

// changeText describes a change in snooping per day: "↓ 92%", "↑ 50%",
// "no change", or "up from none".
func changeText(c model.Comparison) string {
	ch, ok := c.Change()
	if !ok {
		return "up from none"
	}
	p := math.Round(ch * 100)
	switch {
	case p < 0:
		return fmt.Sprintf("↓ %.0f%%", -p)
	case p > 0:
		return fmt.Sprintf("↑ %.0f%%", p)
	}
	return "no change"
}

func round(f float64) int { return int(math.Round(f)) }

func gradeOrDash(g string) string {
	if g == "" {
		return "-"
	}
	return g
}

func thousands(n int) string {
	s := fmt.Sprint(n)
	if n < 0 {
		return "-" + thousands(-n)
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func cmdReceipt(ctx context.Context, args []string) error {
	fl := flag.NewFlagSet("receipt", flag.ContinueOnError)
	cfgPath := fl.String("config", "", "config file")
	days := fl.Int("days", 7, "period in days")
	device := fl.String("device", "", `device ID (e.g. "mac:aa:bb:cc:dd:ee:ff") or name; empty = whole home`)
	out := fl.String("o", "receipt.png", "output file (.png or .svg)")
	useDemo := fl.Bool("demo", false, "use the synthetic demo household")
	if err := fl.Parse(args); err != nil {
		return errUsage
	}
	format := strings.TrimPrefix(strings.ToLower(filepath.Ext(*out)), ".")
	if format != "png" && format != "svg" {
		return fmt.Errorf("-o %s: use a .png or .svg file name", *out)
	}
	a, done, err := reportApp(ctx, *cfgPath, *useDemo)
	if err != nil {
		return err
	}
	defer done()
	p := lastDays(*days)

	id := *device
	if id != "" {
		r, err := a.Report(ctx, p)
		if err != nil {
			return err
		}
		id, err = resolveDevice(r, id)
		if err != nil {
			return err
		}
	}
	b, err := a.Receipt(ctx, p, id, format)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "wrote", *out)
	return nil
}

// resolveDevice accepts a device ID or a case-insensitive display name.
func resolveDevice(r model.HomeReport, q string) (string, error) {
	var names []string
	for _, d := range r.Devices {
		if d.Device.ID == q || strings.EqualFold(d.Device.DisplayName(), q) {
			return d.Device.ID, nil
		}
		names = append(names, fmt.Sprintf("  %s  (%s)", d.Device.ID, d.Device.DisplayName()))
	}
	return "", fmt.Errorf("no device %q in this period; devices:\n%s", q, strings.Join(names, "\n"))
}

func cmdKB(args []string) error {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: phonehome kb lint | stats")
		return errUsage
	}
	switch args[0] {
	case "lint":
		errs := kb.Lint(kbdata.FS)
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, e)
		}
		if len(errs) > 0 {
			return fmt.Errorf("knowledge base: %d problem(s)", len(errs))
		}
		fmt.Println("knowledge base OK")
		return nil
	case "stats":
		s := kb.Default().Stats()
		fmt.Printf("%d rules, %d companies, %d fixes\n", s.Rules, s.Companies, s.Fixes)
		for _, c := range model.Categories() {
			if n := s.ByCategory[c]; n > 0 {
				fmt.Printf("  %-20s %4d\n", c.Label(), n)
			}
		}
		return nil
	}
	fmt.Fprintln(os.Stderr, "usage: phonehome kb lint | stats")
	return errUsage
}

func interfaceAddrs() []netip.Addr {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []netip.Addr
	for _, a := range addrs {
		if p, err := netip.ParsePrefix(a.String()); err == nil {
			out = append(out, p.Addr().Unmap())
		}
	}
	return out
}
