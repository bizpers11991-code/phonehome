# Changelog

## v0.3.0

This release makes every grade explain itself and lets phonehome tell you
when something changes, through your own services. Grades can differ from
v0.2.0 because a repeated lookup is now counted once, a grade says when it
rests on little data, and content-recognition rules apply only to screens
(see Grading below). It also adds a five-language dashboard, a host check
against DNS rebinding, and 151 more knowledge-base rules.

### New
- **Prometheus metrics.** With `metrics: true`, `/metrics` serves per-device
  grades, snooping per day, lookups by category, quiet-hours lookups,
  heartbeats and DNS-bypass findings, home totals and the health of each
  source, behind the dashboard's basic auth. Off by default.
  [docs/integrations.md](docs/integrations.md) lists every series and shows
  Prometheus, Grafana and Home Assistant set-ups.
- **Alerts to your own services.** phonehome can notify a webhook, ntfy,
  Gotify or an MQTT broker when a new device appears, a grade gets worse, a
  device starts a snooping heartbeat or starts bypassing DNS. Off unless a
  target is configured; de-duplicated, rate-limited and remembered across
  restarts. Delivery is at most once: one attempt per target per batch, no
  retry or queue, and a missed batch is not resent. MQTT also keeps a Home
  Assistant grade sensor per device via MQTT discovery. Exactly what is sent
  and when: [docs/alerts.md](docs/alerts.md).
- **Device names over the Pi-hole API.** `pihole-api` sources now also read
  the devices Pi-hole knows (MAC, maker, addresses and host names) from
  `/api/network/devices`, so they are no longer bare IP addresses. Each
  `pihole-api` source therefore shows a second entry, `<name>-devices`, in
  the source health list.

### Grading
- **A lookup is now counted once, not once per query.** A device asking for
  the same name again within 30 seconds of a counted lookup (A, AAAA and
  HTTPS records together, retries, re-queries of a blocked name answered
  with Pi-hole's 2-second TTL) makes one lookup, blocked if any of its
  queries was. Before, iPhones, Macs and Chrome looked up to three times
  worse than other devices doing the same thing, and blocking a tracker
  could make a device's grade worse. Totals, categories, snooping per day,
  hourly and quiet-hours counts, the domain table, blocked counts,
  comparisons, receipts, `/metrics`, alerts and exports all drop
  accordingly, and some grades improve; heartbeat detection is unchanged.
  See [docs/grading.md](docs/grading.md#the-terms).
- **Content-recognition rules apply only to screens.** The ACR rules (F for
  an ACR heartbeat, at least D for any ACR lookup) now apply only to TVs,
  streaming players and devices of unknown kind. A laptop or phone opening
  an ACR company's site was graded D; its ACR lookups now count as snooping
  like any other. Content-recognition hosts no longer count when phonehome
  infers a device's kind, so a laptop is not re-inferred as a TV for
  looking one up.
  [Why](docs/grading.md#which-devices-the-acr-rules-apply-to)
- **Every grade says why.** The report, dashboard (device cards and
  details, "Why D:", in all five languages), receipt and `/api/report`
  (`reason`) name the rule that decided each grade, with its numbers: "1,282
  snooping lookups a day (C is 300–1,499)", "Bypasses your DNS via
  8.8.8.8:443: at least D (its 520 snooping lookups a day alone would be
  C)", or content recognition. The receipt's verdict follows the rule too,
  so a D for a DNS bypass reads "GOES AROUND YOUR DNS FILTER" rather than
  "TALKS ABOUT YOU A LOT". The per-day figure beside a grade is rounded the
  same way as the reason, never across a grade edge (299.6 reads 299 beside
  a B). [docs/grading.md](docs/grading.md#why-a-device-got-its-grade)
- **Provisional grades.** A grade based on less than 20 hours of data for
  the device (a fresh install, or a device that joined an hour ago) is
  marked "Provisional: based on 3 hours of data". Rates per day are per day
  of the device's own data, so a device that joined 48 hours ago with 1,200
  lookups is 600 a day (C), not 171 spread over the home's week. The home
  grade is marked provisional too when every device at that grade is
  (report header, home receipt, dashboard, `/api/report` `provisional`, and
  `phonehome_home_grade_provisional`); `/metrics` also has
  `phonehome_device_grade_provisional`. Alerts and the MQTT grade sensor
  ignore provisional grades: a device that appears after alerts were turned
  on and then earns its first real D or F is alerted, with no previous
  grade, while one known at install learns it silently.
- **Coverage.** Each device shows the share of its lookups the knowledge
  base recognised, and a grade resting on less than half of them says so
  ("Graded on the 40% of lookups phonehome recognises"; an A there gets the
  receipt headline "TOO LITTLE RECOGNISED TO SAY MUCH" instead of "MINDS ITS
  OWN BUSINESS"). TVs and streaming players with no known content-
  recognition server seen get a note that not every brand's servers are
  known. `/metrics` has `phonehome_device_classified_ratio`.

### Privacy and Security
- **Receipts no longer print names that identify your home.** Heartbeat and
  "heartbeats stopped" lines could show local names (`annas-iphone.lan`),
  reverse lookups that contain a home address, and vendor names carrying the
  device's MAC or serial number. Receipts now redact names the way the
  Suggest a rule link does (ID-like labels become `*`, names mentioning the
  device are left out), and heartbeats on local names and reverse lookups
  are no longer reported anywhere.
- **The dashboard answers only to local names.** Requests addressed to a
  host name other than an IP address, `localhost`, `pi.hole`, a
  single-label name or a name under `.local`, `.lan`, `.home.arpa` or
  `.internal` get 421, so a web page that re-points its own domain at
  phonehome (DNS rebinding) can no longer read reports or rename devices
  through a browser on your LAN. List other names you use, such as a reverse
  proxy's, in the new `allowed_hosts:` setting. `/healthz` is unaffected.
- **A warning at startup** when the dashboard has no password and listens
  beyond this machine.
- **Concurrent dashboard requests share one analysis.** Requests for the
  same period wait for the analysis already running instead of starting
  their own, at most two analyses run at once, and rendered receipts are
  cached for 30 seconds like reports. Sixteen simultaneous 30-day reports
  on the demo household now allocate 0.15 GiB instead of 2.3 GiB.
- **Server timeouts:** a request must arrive within 30 seconds, and idle
  connections close after two minutes.
- **Hostile names stay out of your terminal.** Domain names longer than 253
  bytes, not UTF-8, or containing spaces or control characters are skipped
  when reading Pi-hole, AdGuard Home and dnsmasq, and `phonehome report` and
  `phonehome unknown` escape non-printing characters in domain and device
  names.
- **Log lines over 64 KiB are skipped** by the dnsmasq and AdGuard Home
  readers instead of being held in memory whole; reading resumes correctly
  after them.
- **`install.sh --systemd` installs the config readable only by root and
  the service** (0640, group `phonehome`), since it may hold the dashboard
  password.

### Dashboard
- **Five languages.** English, German, French, Spanish and Dutch, chosen
  from the browser's languages or the new selector. The non-English texts
  are machine-translated; corrections welcome. Knowledge-base texts and the
  receipt stay English.
- **Every domain, sortable and filterable.** A device's details list every
  domain it looked up, with company, category, lookups, blocks, first and
  last seen, and the rule's confidence and evidence links. The list comes
  from `GET /api/devices/{id}/domains`, which returns 5000 domains by
  default (`limit` up to 20000, `offset` to page, `total` in the reply); the
  details view shows the first page and says when the list is cut.
- **Export.** The report (CSV, one row per device, or JSON) and a device's
  domain table, generated in the browser. The CSV ends with `grade_rule`,
  `grade_provisional` and `classified_percent`, so a grade's reason travels
  with it.
- **Accessibility.** Arrow keys move between device cards; dialogs keep
  focus inside and return it on close; light-mode colours now meet WCAG AA
  contrast, checked by a test for light and dark.

### Knowledge base
- **151 more rules, mined from open-source clients.** 715 → 866 rules and
  138 → 147 companies since v0.2.0, from the clients that talk to each
  vendor's cloud (Home Assistant integrations and the libraries behind
  them) and from Android, Chromium and SDK source. Every new rule links to
  the exact line of code that names the host. New companies include
  August/Yale (ASSA ABLOY), LG ThinQ, Home Connect (Bosch/Siemens), Miele,
  SolarEdge, Enphase, SwitchBot, Nuki, LIFX and Tesla (Fleet API and
  sign-in hosts). More hosts for Ring, ecobee, Tuya, Xiaomi, Wyze, tado,
  SmartThings, eufy, Govee, Meross, Tapo, Arlo, Netatmo and Ecovacs
  (regional REST, MQTT and XMPP hosts), plus `time.android.com` (essential),
  Chrome's variations and Optimization Guide downloads, Nest's
  `home.nest.com` API, Apple's Wi-Fi location service `gs-loc.apple.com`,
  and LaunchDarkly's and Optimizely's configuration (content) and event
  (telemetry) hosts.
- **Fewer false alarms and less overclaiming, checked against sources.**
  The bare `lgtvcommon.com` and `samba.tv` rules are gone: the evidence
  that could be checked lists only subdomains, or states no purpose, and
  the evidenced subdomains keep their rules. A laptop visiting Samba TV's
  site is no longer flagged for content recognition;
  `samsungelectronics.com`, whose function is undocumented, is dropped;
  OneSignal push notifications count as content; `graph.oculus.com` is
  medium confidence.
- **Shared hosts no longer marked as pure snooping.** `xp.apple.com` (Apple
  lists it for software updates) and `play.googleapis.com` (Play app
  downloads) are essential, as the knowledge base's policy requires for a
  host shared by updates and telemetry; their purposes keep the measured
  telemetry. `ls.apple.com`, which serves Apple Maps, is content.
- **Every evidence link checked before launch.** All 687 cited URLs were
  fetched; three dead or moved links were replaced, and citations that did
  not name their host were swapped for ones that do (Datadog, Meross, Ring,
  EZVIZ and Roborock now cite client code) or dropped. 67 confidence levels
  were corrected to the policy in [kb/README.md](kb/README.md): evidence that
  is only a blocklist, tracker dataset or SDK is at most medium, a single
  community post is low. `update.playstation.net` and `watson.microsoft.com`
  are gone, as no readable source names them. The websites of
  `alphonso.tv` and 26 ad-tech and analytics vendors (apex and `www`; only
  `www` for Sentry and Comscore, whose bare domains also take data) are
  ordinary content, so a laptop reading them is not flagged. Panasonic's
  `mhc-*.myhomescreen.tv` connectivity hosts are essential.

### Fixed
- **Readers checked against upstream source.** Each reader was compared
  with the code of the software it reads, and the fixtures now come from
  that code. Supported versions are listed in
  [docs/setup/README.md](docs/setup/README.md#supported-versions). Fixes:
  - `pihole.log` with Pi-hole's extra logging (`log-queries=proto`, lines
    starting `UDP`/`TCP`) was not read at all.
  - Lookups Pi-hole blocks because of their CNAME chain counted as not
    blocked in `pihole.log`, and so did special domains such as
    `use-application-dns.net`, rate-limited queries and queries refused
    while gravity was busy.
  - dnsmasq answers ending in `(DNSSEC signed)` were not recognised.
  - The Pi-hole API reader could record a lookup before Pi-hole had
    decided to block it (CNAME inspection, a blocking upstream). It now
    waits until a lookup is 30 seconds old, as FTL does before saving.
  - The Pi-hole API reader skipped lookups when one read needed more than
    one page (a backlog of 100 or more queries).
  - AdGuard Home `$dnsrewrite` rules that answer `REFUSED` or `NXDOMAIN`
    now count as blocks.
- A dnsmasq query that never got an answer no longer holds back every later
  query until 100 more log lines arrive (hours on a quiet router). It is
  released, as not blocked, once a later line's own timestamp is 10 seconds
  newer; the wall clock is never consulted, so a quiet router's last query
  appears when its next log line does.
- A syslog line stamped just after New Year, or Feb 29, gets the right year.
- A dnsmasq log that was emptied and grew past the old offset while
  phonehome was stopped is read from the top, not from the middle of a line.
- `phonehome receipt --device` matches a device by ID, name, MAC or any
  known IP address (case-insensitive; surrounding spaces, an IPv6 zone and
  a dashed MAC are fine), and says so when a name or address fits more than
  one device. Before, an IP never matched a device whose ID is a MAC.
- Renaming a device the dashboard doesn't know now answers 404 instead of
  creating it.
- The headline no longer shows the word "null" when there is no earlier
  period to compare with.
- `?days=` with a malformed query string is a 400, not the 7-day default.
- A Pi-hole v6 `https://` address with its self-signed certificate says to
  use the plain `http://` address.
- `install.sh --help` also works when the script is piped into `sh`.
- `serve` no longer logs "ingested" for the unchanged device list every
  interval.
- **First-run rough edges.**
  - A configured `pihole-db`, `adguard-querylog` or `dnsmasq-log` file that
    is missing or unreadable now says so, with the hints auto-detection
    gives (group to add, Docker mounts), in the log, in the source's error,
    in `/api/status` setup problems and after `phonehome report`, instead
    of SQLite's `unable to open database file (14)`.
  - A `--config` or `$PHONEHOME_CONFIG` file that does not exist is an
    error instead of a silent switch to auto-detection. The default
    locations, including the Docker image's `/config/phonehome.yaml`, stay
    optional.
  - `phonehome receipt` refuses to write an empty receipt (grade "?") when
    the home, or the `--device`, has no lookups in the period.
  - `--days` must be at least 1 (`report`, `receipt`, `unknown`, `demo`).
  - A busy port fails before "dashboard listening" is logged, with a hint
    to use `--listen`; `serve` now takes `--listen` too.
  - `demo` honours `$PHONEHOME_LISTEN`, so the Docker image's demo is
    reachable without `--listen :8099`.
  - `phonehome version` shows the module version for `go install …@vX`
    builds instead of `dev`.
  - A misspelt config key is reported with its line and the key it was
    probably meant to be (`line 1: unknown key "listne" (did you mean
    "listen"?)`) instead of yaml.v3's `field listne not found in type
    config.Config`.
  - The permission hint for `pihole-FTL.db` also says how to join the group
    from a shell (`sudo usermod -aG pihole $USER`), and a database its group
    may not read (e.g. `chmod 600`) is told to go back to Pi-hole's 0640
    instead of being told to join a group that would not help.
  - `pihole-api` sources say what to fix when the Pi-hole wants a password
    and none is set, refuses the one given, or cannot be reached, instead
    of `GET /api/queries: 401 Unauthorized` or a dial error.
  - `phonehome ingest --once` reports each problem (an unreadable file, an
    unreachable Pi-hole) once, instead of once per reader of that source
    wrapped in `<name>: fetching:`.
  - `serve` or `ingest` run as an ordinary user, who cannot create the
    default `/var/lib/phonehome`, say to set `$PHONEHOME_DB` or `db:`.
  - `phonehome help` lists every flag with a one-line description, each
    command's `-h` shows what it does and its flags (and exits 0), and a
    stray argument (`phonehome serve phonehome.yaml`) is an error instead
    of being ignored.
  - The root `docker-compose.yml` (Pi-hole on the host) asks for
    `PIHOLE_GID`, the group that may read `pihole-FTL.db`, instead of a
    commented-out GID that was wrong for most hosts.
    `packaging/compose/adguard.yml` notes it needs Docker Compose v2.23.1+.

### Reliability and speed
- **A report's memory no longer grows with the number of distinct
  domains.** Launch QA's fixture (3M lookups, 14 clients, 208,000 domains)
  took 3.3 s and 708 MiB for a 7-day report and 4.3 s and 803 MiB for 30
  days, and thrashed for 14 s under `GOMEMLIMIT=128MiB`; it now takes 2.3 s
  and 225 MiB, 3.8 s and 234 MiB, and 4.7 s and 162 MiB under the limit.
  Lookups are streamed from the store into the analysis instead of being
  held as a slice, per-domain counters and classifications are packed and
  shared, the unknown and top-domain lists keep only their top as they go,
  and the full domain lists are kept up to a budget (past it, a device's
  table is listed when its details open). Reports are byte-for-byte
  unchanged. See [docs/performance.md](docs/performance.md).
- **Retention applies on the first read.** Records older than
  `retention_days` are skipped before they are stored (Pi-hole database
  `WHERE timestamp >=`, Pi-hole API `from=`, other sources filtered before
  insert), with cursors moving past them. A 30-day, 3M-row Pi-hole database
  with `retention_days: 2` now ingests in 3.7 s into an 18 MB file; before
  it wrote 3M rows, deleted 2.8M and kept 125 MB. New databases use
  incremental auto-vacuum and give freed space back after a large prune;
  existing ones log how to shrink once (phonehome never runs a full
  `VACUUM`).
- **A first start on a large history catches up in seconds.** A Pi-hole
  database with millions of rows used to be read at 100,000 lookups per
  minute; the next round now starts at once while a source has more, and
  `ingest --once` reads everything instead of the oldest 100,000.
- **A `kill -9`, power cut or shutdown during ingestion no longer stores a
  batch twice.** The batch and its cursor are written in one transaction;
  before, up to 5,000 lookups per source were duplicated on restart.

### Docs and tests
- [docs/testing-at-home.md](docs/testing-at-home.md): a first real
  measurement of your own home: which source, how long to collect, reading
  `phonehome unknown`, suggesting rules and sharing a receipt without MAC
  addresses. A test now keeps the "Suggest a rule" link's prefilled fields
  in step with the issue form.
- docs/alerts.md states exactly how delivery fails: one attempt per target
  per batch, no retry or queue, a missed batch is not resent.
- Rotated `.gz` logs: the dnsmasq, OpenWrt, AdGuard Home and FAQ pages say
  logrotate needs `delaycompress` (phonehome does not read `.gz`).
- Every claim in the README, FAQ, setup guides and docs checked against the
  code: privacy wording names alert targets, the Pi-hole API source's device
  names, current demo and benchmark figures, and the `allowed_hosts` names
  (including `pi.hole`).
- An end-to-end smoke test ([scripts/smoke.sh](scripts/smoke.sh)) builds
  the binary, starts the demo and checks every endpoint, its headers and
  its refusals in CI.

## v0.2.0

### New
- **Since you fixed it.** Every report is compared with the period before it:
  snooping lookups a day before → now, the grade change, and which heartbeats
  stopped or started. It appears in the dashboard, `phonehome report` and a
  new *SINCE LAST WEEK* block on the receipt. See [docs/grading.md](docs/grading.md).
- **Home grade.** The home gets its own A–F grade: as good as its worst device.
- **Help classify.** `phonehome unknown` lists the domains the knowledge base
  can't explain yet, and each device's details have a *Suggest a rule* link
  that opens a prefilled GitHub issue with domain names only.
- **A friendlier first run.** Auto-detection says when it finds Pi-hole's or
  AdGuard Home's files but can't read them, and how to fix it. `serve` keeps
  looking until a source appears. The dashboard shows a setup panel instead of
  an empty page.
- **Setup guides** for Pi-hole (Docker, on the host, over its API), AdGuard
  Home, OpenWrt, Unraid, Synology and Proxmox, with compose files, plus
  Homebrew and AUR templates attached to each release.

### Knowledge base
- 540 → 715 rules and 114 → 138 companies. New coverage includes Windows,
  macOS/iOS, Linux, browsers, Microsoft 365, Slack, Zoom, messaging apps,
  game consoles, web trackers, Roborock US, Dreame and Google Home/Cast.
- 34 → 41 fixes: Panasonic, Sharp, Ecovacs, Blink, Tapo/Kasa (two) and Tuya.
  Six existing fixes were corrected against their sources.

### Reliability and speed
- Fuzz tests for every parser. They found and fixed: an AdGuard reader that
  could loop after a clock jump, a DHCP lease file broken by a device named
  `{`, and a `retention_days` overflow.
- Recovers from a replaced Pi-hole database, restarted ids and unreadable
  cursors. Reads dnsmasq 2.86+ answers that carry an extended DNS error.
- Retention also runs with `ingest --once`.
- Reports over 30 days are about twice as fast and use a third of the memory.
  `GOMEMLIMIT=128MiB` by default in the image and systemd unit. Numbers are in
  [docs/performance.md](docs/performance.md).

## v0.1.0

First release.
