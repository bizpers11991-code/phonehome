# Changelog

## Unreleased

### Knowledge base
- 715 → 784 rules and 138 → 146 companies, mined from the open-source
  clients that talk to each vendor's cloud (Home Assistant integrations and
  the libraries behind them). Every new rule links to the exact line of
  code that names the host. New: August/Yale (ASSA ABLOY), LG ThinQ,
  Home Connect (Bosch/Siemens), Miele, SolarEdge, Enphase, SwitchBot, Nuki
  and LIFX. More hosts for Ring, ecobee, Tuya, Xiaomi, Wyze, tado,
  SmartThings, eufy, Govee, Meross, Tapo, Arlo and Netatmo.

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
