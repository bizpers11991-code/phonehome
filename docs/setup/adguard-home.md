# AdGuard Home

*Docker: tested with `adguard/adguardhome:latest`. Bare metal: the systemd
override below was tested against a query log with AdGuard's ownership and
permissions, not against a running bare-metal AdGuard Home.*

phonehome reads AdGuard Home's query log, `querylog.json` (one JSON object
per line), and the rotated `querylog.json.1` next to it. It never writes to
either.

## Before you start: the query log must be on disk

In AdGuard Home, **Settings › General settings › Logs configuration** must
have the query log enabled. phonehome only sees what is kept, so a retention
of at least a week makes the reports meaningful.

AdGuard can also keep the log in memory only. phonehome needs it in the file,
so check `AdGuardHome.yaml`:

```yaml
querylog:
  enabled: true
  file_enabled: true     # must be true (the default)
  interval: 90d          # retention; the UI sets this
  size_memory: 1000      # entries held in memory before being written
```

AdGuard writes entries to the file in batches of `size_memory`, and when it
stops. In a quiet home it can take a while for lookups to show up in
phonehome. You may lower `size_memory` (stop AdGuard Home before editing the
file), at the cost of more frequent disk writes, which matters on SD cards.

## Where `querylog.json` lives

It sits in the `data/` folder of AdGuard's working directory:

| Install | Path | Auto-detected |
|---|---|---|
| Official installer / tarball | `/opt/AdGuardHome/data/querylog.json` | yes |
| Some distro packages | `/var/lib/AdGuardHome/data/querylog.json` | yes |
| Snap | `/var/snap/adguard-home/current/data/querylog.json` (or `common`) | yes |
| Docker (`adguard/adguardhome`) | `/opt/adguardhome/work/data/querylog.json` inside the container, i.e. wherever you mounted `/opt/adguardhome/work` | mount it at a detected path, or configure it |

If `querylog.dir_path` is set in `AdGuardHome.yaml`, the log is there instead.

## Docker

[`packaging/compose/adguard.yml`](../../packaging/compose/adguard.yml) runs
AdGuard Home and phonehome together. It needs Docker Compose v2.23.1 or later
(`docker compose version`) for its inline `configs:` block:

```sh
mkdir adguard && cd adguard
curl -fsSLo compose.yml https://raw.githubusercontent.com/bizpers11991-code/phonehome/main/packaging/compose/adguard.yml
docker compose up -d
```

Finish AdGuard's setup wizard on `http://<host>:3000`, then open phonehome on
`http://<host>:8099`.

For an existing AdGuard container, add the `phonehome` service and the
top-level `configs:` block from that file, and change `./adguard/work/data`
to the host folder you mount as `/opt/adguardhome/work` plus `/data`.

Two things in that file are deliberate:

- **phonehome runs as root, with every capability dropped.** AdGuard Home
  runs as root and creates `querylog.json` with mode `0600`, so only root can
  read it. Dropping all capabilities means root in the phonehome container
  can read files root owns but cannot override permissions on anything else.
  AdGuard's folder is mounted read-only.
- **The source is configured explicitly** (via the inline `configs:` block,
  which needs Docker Compose 2.23 or newer). AdGuard only creates the file
  after its first batch of lookups, and only at the path phonehome is told
  about: auto-detection (which keeps looking every minute until it finds a
  DNS source) only knows the bare-metal and snap paths. An explicitly
  configured file is retried every poll. Until
  it exists, the dashboard shows `no query log at … (is querylog.file_enabled
  on?)`.

## Bare metal (systemd)

Install phonehome with the [installer](pihole-bare-metal.md#install):

```sh
curl -fsSLO https://raw.githubusercontent.com/bizpers11991-code/phonehome/main/scripts/install.sh
sudo sh install.sh --systemd
```

The stock unit runs phonehome as an unprivileged throwaway user, which
cannot read AdGuard's root-only `querylog.json`. Override that:

```sh
sudo systemctl edit phonehome
```

```ini
[Service]
# AdGuard Home writes querylog.json as root with mode 0600. Run as root,
# still with an empty capability set and a read-only view of the system.
DynamicUser=no
User=root
```

```sh
sudo systemctl restart phonehome
journalctl -u phonehome -n 20
# … msg="auto-detected source" type=adguard-querylog path=/opt/AdGuardHome/data/querylog.json
```

The unit's other hardening stays in place: `CapabilityBoundingSet=` is empty,
the filesystem is read-only apart from `/var/lib/phonehome`, and
`/opt/AdGuardHome` and `/var/lib/AdGuardHome` are mounted read-only.
`ProtectHome=yes` hides `/home`; if your AdGuard lives there, move it or
relax that setting in the same override.

## Device names

AdGuard's query log records client IPs only. phonehome shows devices by IP
unless you name them with `labels:` or add a `leases` source pointing at your
DHCP server's lease file (dnsmasq, odhcpd and ISC dhcpd formats).
AdGuard Home's own DHCP server stores leases in a JSON format that phonehome
does not read yet.
