# Changelog

## Unreleased

### Knowledge base
- 784 → 802 rules and 146 → 147 companies. Tesla's Fleet API, sign-in
  hosts (from the client library Home Assistant's Tesla integrations use)
  and Ecovacs' regional REST, MQTT and XMPP hosts (from the Deebot
  libraries), each linked to the line of code that names it.

### Fixed
- In `pihole.log` without serials, when a client asked A and AAAA for the
  same name, A got a CNAME reply and AAAA was then blocked upstream, the
  block was counted for A instead of AAAA. A blocking verdict now goes to
  the query of the type its address is for (0.0.0.0 for A, :: for AAAA), so
  an upstream block of a CNAME chain still counts for the query it ends.
- The device panel's domain table fits a 1280px screen: the panel is wider,
  dates and confidence wrap, and the evidence column shows "Evidence 1/2"
  links instead of raw URLs (a list of links was being turned into text).
- LG ThinQ regional API rules cite the full list of countries each region
  serves, and their purposes now name every region that list covers.

### Changed in v0.3.0, now noted
- Each `pihole-api` source also shows a second `<name>-devices` entry in
  the source health list: it reads device names from Pi-hole's
  `/api/network/devices`.

### Fixed
- **Receipts no longer print names that identify your home.** Heartbeat and
  "heartbeats stopped" lines could show local names (`annas-iphone.lan`),
  reverse lookups that contain a home address, and vendor names carrying the
  device's MAC or serial number. Receipts now redact names the way the
  Suggest a rule link does (ID-like labels become `*`, names mentioning the
  device are left out), and heartbeats on local names and reverse lookups
  are no longer reported anywhere.

### Docs
- [docs/testing-at-home.md](docs/testing-at-home.md): a first real
  measurement of your own home: which source, how long to collect, reading
  `phonehome unknown`, suggesting rules and sharing a receipt without MAC
  addresses. A test now keeps the "Suggest a rule" link's prefilled fields
  in step with the issue form.

## v0.3.0

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
  restarts. MQTT also keeps a Home Assistant grade sensor per device via MQTT
  discovery. Exactly what is sent: [docs/alerts.md](docs/alerts.md).
- **Every grade says why.** The report, dashboard, receipt and
  `/api/report` (`reason`) name the rule that decided each grade, with its
  numbers: "1,282 snooping lookups a day (C is 300–1,499)", "Bypasses your
  DNS via 8.8.8.8:443: at least D (its 520 snooping lookups a day alone
  would be C)", or content recognition. The receipt's verdict follows the
  rule too, so a D for a DNS bypass reads "GOES AROUND YOUR DNS FILTER"
  rather than "TALKS ABOUT YOU A LOT". [docs/grading.md](docs/grading.md#why-a-device-got-its-grade)
- **Provisional grades.** A grade based on less than 20 hours of data for
  the device (a fresh install, or a device that joined an hour ago) is marked
  "Provisional: based on 3 hours of data". Alerts and the MQTT grade sensor
  ignore provisional grades; `/metrics` has
  `phonehome_device_grade_provisional`.
- **Coverage.** Each device shows the share of its lookups the knowledge base
  recognised, and a grade resting on less than half of them says so
  ("Graded on the 40% of lookups phonehome recognises"). TVs and streaming
  players with no known content-recognition server seen get a note that not
  every brand's servers are known. `/metrics` has
  `phonehome_device_classified_ratio`.

### Security
- **The dashboard answers only to local names.** Requests addressed to a
  host name other than an IP address, `localhost`, a single-label name or a
  name under `.local`, `.lan`, `.home.arpa` or `.internal` get 421, so a web
  page that re-points its own domain at phonehome (DNS rebinding) can no
  longer read reports or rename devices through a browser on your LAN. List
  other names you use, such as a reverse proxy's, in the new
  `allowed_hosts:` setting. `/healthz` is unaffected.
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
- **`/metrics` stays valid** when a DHCP hostname is not UTF-8.
- **`install.sh --systemd` installs the config readable only by root and
  the service** (0640, group `phonehome`), since it may hold the dashboard
  password.

### Dashboard
- **Five languages.** English, German, French, Spanish and Dutch, chosen from
  the browser's languages or the new selector. The non-English texts are
  machine-translated; corrections welcome. Knowledge-base texts and the
  receipt stay English.
- **Every domain, sortable and filterable.** A device's details list every
  domain it looked up, with company, category, lookups, blocks, first and
  last seen, and the rule's confidence and evidence links.
- **Export.** The report (CSV, one row per device, or JSON) and a device's
  domain table, generated in the browser.
- **Accessibility.** Arrow keys move between device cards; dialogs keep focus
  inside and return it on close; light-mode colours now meet WCAG AA
  contrast, checked by a test for light and dark.
- The headline no longer shows the word "null" when there is no earlier
  period to compare with.
- **Why each grade.** Device cards and details show "Why D:" with the rule
  behind the grade, any provisional or low-coverage note, and in the details
  the share of lookups recognised, in all five languages.

### Knowledge base
- **Fewer false alarms and less overclaiming, checked against sources.**
  The bare `lgtvcommon.com` and `samba.tv` rules are gone: the evidence
  that could be checked lists only subdomains, or states no purpose, and
  the evidenced subdomains keep their rules. A laptop visiting Samba TV's
  site is no longer flagged for content recognition;
  `samsungelectronics.com`, whose function is undocumented, is dropped;
  OneSignal push notifications count as content; `graph.oculus.com` is
  medium confidence. New, from Android, Chromium and client source:
  `time.android.com` (essential), Chrome's variations and Optimization
  Guide downloads, Nest's `home.nest.com` API, Apple's Wi-Fi location
  service `gs-loc.apple.com`, and LaunchDarkly's and Optimizely's
  configuration (content) and event (telemetry) hosts. 802 → 810 rules.
- **Shared hosts no longer marked as pure snooping.** `xp.apple.com` (Apple
  lists it for software updates) and `play.googleapis.com` (Play app
  downloads) are essential, as the knowledge base's policy requires for a
  host shared by updates and telemetry; their purposes keep the measured
  telemetry. `ls.apple.com`, which serves Apple Maps, is content.
- 715 → 784 rules and 138 → 146 companies, mined from the open-source
  clients that talk to each vendor's cloud (Home Assistant integrations and
  the libraries behind them). Every new rule links to the exact line of
  code that names the host. New: August/Yale (ASSA ABLOY), LG ThinQ,
  Home Connect (Bosch/Siemens), Miele, SolarEdge, Enphase, SwitchBot, Nuki
  and LIFX. More hosts for Ring, ecobee, Tuya, Xiaomi, Wyze, tado,
  SmartThings, eufy, Govee, Meross, Tapo, Arlo and Netatmo.

### Fixed
- **Lookup counts and grades change: a lookup is now counted once, not once
  per query.** A device asking for the same name again within 30 seconds of
  a counted lookup (A, AAAA and HTTPS records together, retries, re-queries of
  a blocked name answered with Pi-hole's 2-second TTL) makes one lookup,
  blocked if any of its queries was. Before, iPhones, Macs and Chrome looked
  up to three times worse than other devices doing the same thing, and
  blocking a tracker could make a device's grade worse. Totals, categories,
  snooping per day, hourly and quiet-hours counts, the domain table, blocked
  counts, comparisons, receipts, `/metrics`, alerts and exports all drop
  accordingly, and some grades improve; heartbeat detection is unchanged.
  See [docs/grading.md](docs/grading.md#the-terms).
- **Content-recognition rules apply only to screens.** The ACR rules (F for
  an ACR heartbeat, at least D for any ACR lookup) now apply only to TVs,
  streaming players and devices of unknown kind. A laptop or phone opening an
  ACR company's site was graded D; its ACR lookups now count as snooping
  like any other. [Why](docs/grading.md#which-devices-the-acr-rules-apply-to)
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
  - dnsmasq answers ending in `(DNSSEC signed)` were not recognised, and
    DNSSEC `validation` lines were taken for answers, settling a query
    before Pi-hole had decided whether to block it.
  - The Pi-hole API reader could record a lookup before Pi-hole had
    decided to block it (CNAME inspection, a blocking upstream). It now
    waits until a lookup is 30 seconds old, as FTL does before saving.
  - AdGuard Home `$dnsrewrite` rules that answer `REFUSED` or `NXDOMAIN`
    now count as blocks.
- **Device names over the Pi-hole API.** `pihole-api` sources now also read
  the devices Pi-hole knows (MAC, maker, addresses and host names) from
  `/api/network/devices`, so they are no longer bare IP addresses.
  Each `pihole-api` source therefore shows a second entry, `<name>-devices`,
  in the source health list.
- Renaming a device the dashboard doesn't know now answers 404 instead of
  creating it.
- An end-to-end smoke test ([scripts/smoke.sh](scripts/smoke.sh)) builds the
  binary, starts the demo and checks every endpoint, its headers and its
  refusals in CI.

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
