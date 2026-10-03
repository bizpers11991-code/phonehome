# phonehome architecture

> See what your devices say about you.

phonehome is a single, self-hosted Go binary. It reads the logs of network
gear people already run (Pi-hole, AdGuard Home, dnsmasq, conntrack), works out
which device made each lookup, classifies every destination with a
community-maintained knowledge base, and turns that into plain-English reports
and a shareable **Privacy Receipt**.

It never sends anything anywhere. No telemetry, no cloud, no accounts. A
privacy tool that phones home would be a joke at our own expense.

```
  Pi-hole DB/API ─┐
  AdGuard log ────┤                    ┌──────────┐
  dnsmasq log ────┼─▶ source.* ─▶ ingest ─▶│  store   │ (SQLite, pure Go)
  conntrack ──────┤                    └────┬─────┘
  leases/network ─┘                         │
                                            ▼
                     kb (YAML, embedded) ─▶ analyze ─▶ model.HomeReport
                                                          │
                                         ┌────────────────┼──────────────┐
                                         ▼                ▼              ▼
                                    web (UI+API)    receipt (SVG/PNG)   CLI
```

## Ground rules

1. **Stdlib first.** The only third-party modules are `modernc.org/sqlite`
   (pure-Go SQLite, no cgo), `gopkg.in/yaml.v3`, and `golang.org/x/image`
   (receipt rendering). Adding a dependency needs a very good reason.
2. **No cgo.** `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./...` must work
   so it runs on a Raspberry Pi.
3. **Read-only toward the network.** Sources open files/DBs read-only and never
   write to Pi-hole, AdGuard, or the router. No ARP spoofing, no active scanning.
4. **Honesty in every string.** Encrypted traffic tells us *who, when, and how
   often*, never *what*. Never claim more. Every KB claim carries evidence URLs
   and a confidence level. Demo data is always labelled as demo data.
5. **Tests live next to code** (`*_test.go`), use `testdata/` fixtures, and
   must pass with `go test -race`. No network access in tests.
6. `internal/model` is the contract between packages; change it deliberately.

## Packages and their public APIs

Module: `github.com/bizpers11991-code/phonehome`

### `internal/model`
Shared types: `Category`, `DNSQuery`, `Flow`, `Device`, `DeviceKind`,
`Company`, `Classification`, `Fix`, `Period`, `DeviceReport`, `HomeReport`,
`Status`. Read `internal/model/model.go`.

### `internal/source`
`DNSSource`, `FlowSource`, `DeviceSource`. Opaque string cursors, oldest-first,
never modify upstream.

### `kb/` + `internal/kb`: the knowledge base
Data lives at the repo root so contributors find it:

```
kb/embed.go            package kbdata; //go:embed companies domains fixes; var FS embed.FS
kb/companies/*.yaml
kb/domains/*.yaml
kb/fixes/*.yaml
```

`kb/companies/<group>.yaml`
```yaml
companies:
  - id: samsung            # slug, unique across all files
    name: Samsung Electronics
    country: KR            # ISO 3166-1 alpha-2 of HQ
    url: https://www.samsung.com
```

`kb/domains/<group>.yaml`
```yaml
company: samsung          # default company for rules in this file (optional)
rules:
  - pattern: acr.samsungcloudsolution.com   # suffix match on label boundary
    company: samsung      # optional override
    category: acr         # model.Category
    purpose: Sends fingerprints of what is on screen so Samsung can identify what you watch.
    confidence: high      # high | medium | low
    kind_hint: tv         # optional model.DeviceKind this traffic implies
    evidence:
      - https://example.org/paper-or-docs
```
Pattern semantics: `example.com` matches `example.com` and any subdomain
(`a.b.example.com`) but not `badexample.com`. A leading `=` means exact match
only (`=time.example.com`). The longest (most specific) matching pattern wins.

`kb/fixes/<id>.yaml`
```yaml
id: samsung-tv-viewing-info
title: Turn off Viewing Information Services (Samsung's ACR)
vendors: [samsung]         # matched against the device's vendor/hostname/label,
                           # or the company it talks to most
kinds: [tv]                # model.DeviceKind; empty = any
when: [acr]                # only shown when this finding is present: a category
                           # or "bypass"; empty = always
steps:
  - Settings › All Settings › General & Privacy › Terms & Privacy
  - Viewing Information Services → Off
notes: Menu names vary by model year (2022+ shown).
evidence:
  - https://...
```

`internal/kb` API:
```go
func Load(fsys fs.FS) (*KB, error)          // loads companies/, domains/, fixes/; validates
func Default() *KB                          // Load(kbdata.FS), panics on error (it is tested)
func (k *KB) Classify(domain string) model.Classification   // never nil-panics; unknown → CatUnknown
func (k *KB) Company(id string) *model.Company
func (k *KB) Fixes() []model.Fix
func (k *KB) FixesFor(d model.Device, companyIDs []string) []model.Fix
func (k *KB) Stats() Stats                  // counts of rules per category, companies, fixes
func Lint(fsys fs.FS) []error               // all problems, for `phonehome kb lint` and CI
```

### `internal/source/*`: readers (each implements a `source` interface)
| package | type | reads |
|---|---|---|
| `source/pihole` | `DB` (DNSSource + DeviceSource) | `pihole-FTL.db` read-only: `queries` (v5 table / v6 view), `network` + `network_addresses` |
| `source/pihole` | `API` (DNSSource) | Pi-hole v6 REST `/api/queries` with app password |
| `source/adguard` | `QueryLog` (DNSSource) | AdGuard Home `querylog.json` + rotated `querylog.json.1` (JSON lines) |
| `source/dnsmasq` | `Log` (DNSSource) | dnsmasq `log-queries` syslog lines (OpenWrt, plain dnsmasq) |
| `source/leases` | `File` (DeviceSource) | dnsmasq / odhcpd / ISC dhcpd lease files |
| `source/conntrack` | `Table` (FlowSource) | `/proc/net/nf_conntrack` snapshots or `conntrack -L -o extended` output |
| `internal/oui` | `Lookup(mac) string` | embedded curated MAC-prefix → vendor table for consumer IoT brands; detects randomized (locally administered) MACs |

### `internal/store`: SQLite persistence
```go
func Open(path string) (*Store, error)      // creates/migrates; WAL; ":memory:" ok
func (s *Store) Close() error
func (s *Store) InsertDNS(ctx, []model.DNSQuery) error
func (s *Store) InsertFlows(ctx, []model.Flow) error
func (s *Store) UpsertDevices(ctx, []model.Device) error   // merge semantics, see source.DeviceSource
func (s *Store) SetLabel(ctx, deviceID, label string) error
func (s *Store) Devices(ctx) ([]model.Device, error)
func (s *Store) DNSBetween(ctx, model.Period) ([]model.DNSQuery, error)  // ordered by time
func (s *Store) FlowsBetween(ctx, model.Period) ([]model.Flow, error)
func (s *Store) Cursor(ctx, source string) (string, error)
func (s *Store) SetCursor(ctx, source, cursor string) error
func (s *Store) RecordRun(ctx, model.SourceStatus) error   // upsert by Name; Records is added
func (s *Store) Status(ctx) (model.Status, error)          // fills Sources, Devices, Oldest, Newest
func (s *Store) Prune(ctx, before time.Time) (int64, error)
```

### `internal/analyze`: turning rows into findings
```go
type Options struct {
    QuietStart, QuietEnd int   // local hours, default 1 and 6
    Location *time.Location    // default time.Local
    MinHeartbeat int           // min lookups to consider a heartbeat, default 20
}
// Classifier is satisfied by *kb.KB; analyze does not import kb.
type Classifier interface {
    Classify(domain string) model.Classification
    FixesFor(d model.Device, companyIDs []string) []model.Fix
}
func Analyze(c Classifier, p model.Period, devs []model.Device, qs []model.DNSQuery, fl []model.Flow, o Options) model.HomeReport
// AnalyzeCompared also fills HomeReport.Previous / DeviceReport.Previous by
// comparing p with p.Previous(); qs and fl cover both periods (one store
// read), dataFrom is Status.Oldest. Rules: docs/grading.md.
func AnalyzeCompared(c Classifier, p model.Period, devs []model.Device, qs []model.DNSQuery, fl []model.Flow, o Options, dataFrom time.Time) model.HomeReport
func InferKind(d model.Device, hints map[model.DeviceKind]int) model.DeviceKind
func Grade(r model.DeviceReport) string
func HomeGrade(devs []model.DeviceReport) string   // worst device grade; "" with no devices
func Registrable(domain string) string      // best-effort eTLD+1, for grouping only
```
Lookups are attributed to devices by `ClientIP ∈ Device.IPs`; unattributed
client IPs become synthetic devices `ip:<addr>`. Heartbeats, quiet-hours
counts, DNS-bypass detection, grading: see `docs/grading.md` and
`docs/detectors.md`.

### `internal/receipt`: the shareable artifact
```go
type Options struct { Demo bool; Now time.Time; Width int /* px, default 576 like a thermal printer */ }
func Device(r model.DeviceReport, o Options) Doc     // layout model, renderer-agnostic
func Home(r model.HomeReport, o Options) Doc
func (d Doc) SVG() []byte
func (d Doc) PNG() ([]byte, error)                   // pure Go, embedded Go Mono font
```

### `internal/web`: dashboard + JSON API
```go
type Backend interface {
    Report(ctx context.Context, p model.Period) (model.HomeReport, error)
    Status(ctx context.Context) (model.Status, error)
    SetLabel(ctx context.Context, deviceID, label string) error
    // Receipt renders the home receipt (deviceID == "") or one device's.
    // format is "svg" or "png". web does not import receipt.
    Receipt(ctx context.Context, p model.Period, deviceID, format string) ([]byte, error)
}
func New(b Backend, o Options) http.Handler
```
`DeviceReport.Unknown` lists each device's unclassified domains (count, first
and last seen), leaving out local names (`.lan`, `.local`, `.home.arpa`, ...)
and reverse lookups. They feed the "help us classify" loop: `phonehome
unknown` prints them grouped by registrable domain, and the dashboard's device
view lists them with a **Suggest a rule** link (`suggestUrl` in the report
JSON, built in `internal/web/suggest.go`). The link opens GitHub's
`new-device.yml` issue form prefilled with domain names and the device's
vendor and kind only; ID-like labels become `*`, and names containing the
device's hostname, label, MAC or address are dropped. phonehome never fetches
it; the person clicks it, and the UI says GitHub will see the names. Demo
reports get no link.

Routes: `GET /` (embedded SPA), `GET /api/report?days=7`, `GET /api/status`,
`POST /api/devices/{id}/label`, `GET /receipt/home.svg|png?days=7`,
`GET /receipt/{deviceID}.svg|png?days=7`, `GET /healthz`. Assets embedded with
`go:embed`; no CDN, no external fonts (it runs on a LAN, often offline).

### `internal/config`, `internal/ingest`, `internal/demo`, `cmd/phonehome`
- `config`: YAML config file + defaults + `Validate()`; `Detect` finds
  `/etc/pihole/pihole-FTL.db`, AdGuard and dnsmasq paths when no sources are
  configured, and reports files it found but cannot read (with a hint) as
  `Problem`s. `serve` re-runs detection every minute until it has a DNS
  source and shows the findings in `/api/status` (`setup`, from
  `model.Status.Setup`), the dashboard's first-run panel and `phonehome
  report`.
- `ingest`: the loop. For each source: read cursor, fetch in batches, write,
  save cursor, record status. Survives source errors and backs off. A
  cursor a source rejects (`source.ErrBadCursor`) is reset and the source
  read from the start.
- Retention: `retention_days` (default 90, max 36500, 0 = keep forever)
  makes ingest call `Store.Prune` for anything older: once a day under
  `serve` / `ingest`, and at the end of every `ingest --once`. Prune
  deletes in 5000-row transactions so ingestion is never blocked for long.
  Measured sizes and timings are in `docs/performance.md`.
- `demo`: deterministic synthetic household (seeded), clearly labelled.
- `alert`: opt-in notifications (`alerts:` in the config). An `Engine`
  checks the 7-day report every interval, compares it with a snapshot kept
  in the store (`alert_state`), and sends new events to webhook, ntfy,
  Gotify and MQTT targets (a minimal MQTT 3.1.1 client, with Home Assistant
  discovery). Nothing runs unless a target is configured. docs/alerts.md.
- CLI: `phonehome serve | ingest --once | report | receipt | unknown | demo | kb lint | version`.
