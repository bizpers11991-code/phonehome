# Setup guides

phonehome only needs to **read** one thing: the DNS log your filter or router
already keeps. Pick the guide that matches where that log lives.

| You run | Guide | Tested |
|---|---|---|
| Pi-hole v6 in Docker | [pihole-docker.md](pihole-docker.md) | yes |
| Pi-hole installed on the host (Raspberry Pi, Debian, Ubuntu) | [pihole-bare-metal.md](pihole-bare-metal.md) | partly |
| Pi-hole v6 on another machine | [pihole-api.md](pihole-api.md) | yes |
| AdGuard Home (Docker, bare metal or snap) | [adguard-home.md](adguard-home.md) | yes |
| OpenWrt, or plain dnsmasq | [openwrt.md](openwrt.md) | partly |
| Unraid | [unraid.md](unraid.md) | no |
| Synology DSM | [synology.md](synology.md) | no |
| Proxmox LXC | [proxmox-lxc.md](proxmox-lxc.md) | no |

"Tested" means we ran these exact steps against the real software in
containers before publishing them. Where a guide says *untested*, the steps
follow the vendor's documentation and phonehome's code, but nobody has run
them end to end yet. If you do, please
[tell us how it went](https://github.com/bizpers11991-code/phonehome/issues/new?template=bug.yml)
or send a pull request fixing the guide.

Packages for Homebrew and the AUR are described in
[packaging/README.md](../../packaging/README.md).

## Supported versions

Each reader is checked against the source code of the software it reads,
with test fixtures built from what that code writes.

| Source | Supported | Checked against |
|---|---|---|
| `pihole-db` | Pi-hole v5 and v6: FTL database versions 9 to 22 (tested: 9, 12, 21, 22) | FTL's own schema tests at v5.25.2, v6.0 and master (October 2026) |
| `pihole-api` | Pi-hole v6 (FTL v6.0 and later) | FTL's `/api/queries` code at v6.0 and master |
| `adguard-querylog` | AdGuard Home v0.107 query logs, including the older single-rule result format | AdGuard Home's own log writer (master after v0.107.79) |
| `dnsmasq-log` | `log-queries`, `log-queries=extra` and `log-queries=proto`; Pi-hole v5 and v6 `pihole.log` | dnsmasq 2.93's `log_query()` and the copy inside Pi-hole FTL |

What counts as **blocked**, per source:

- **Pi-hole** (database and API): every status FTL itself calls blocked:
  gravity, regex and exact denylist matches (also when found in a CNAME
  chain), answers an upstream blocked, special domains such as
  `use-application-dns.net`, and queries refused while gravity was busy.
  The API reader waits until a query is 30 seconds old, because FTL can
  still change a query to blocked when its reply arrives (CNAME inspection,
  a blocking upstream). FTL waits just as long before saving to its database.
- **AdGuard Home**: blocklists, blocked services, safe browsing, parental
  control and invalid requests, plus `$dnsrewrite` rules that answer with
  an error code such as `REFUSED` or `NXDOMAIN` (AdGuard Home's own
  dashboard counts those as rewritten, so phonehome's blocked figure can be
  a little higher). Safe search, rewrites and allowlisted queries are
  answered, so they are not blocks.
- **dnsmasq**: names dnsmasq answers from its own configuration with
  `0.0.0.0`, `::`, `NXDOMAIN` or `NODATA` (the `address=/name/` lines
  blocklists generate). In Pi-hole's `pihole.log`, every verdict Pi-hole
  logs as blocked, including whole CNAME chains.

**Rate limiting.** When Pi-hole rate-limits a client, FTL refuses the
query without recording it, so it is missing from the database and the
API. `pihole.log` does log it, and the `dnsmasq-log` reader counts it as a
blocked lookup. The same home can therefore show more lookups for a
flooding device when read from `pihole.log`.

## Configuration in one minute

With no config file, phonehome looks for these files at startup and uses
every one it can read. Under `phonehome serve` it keeps looking, once a
minute, until it has found a DNS source (Pi-hole, AdGuard Home or dnsmasq),
so it does not matter which container starts first:

| File | Source type |
|---|---|
| `/etc/pihole/pihole-FTL.db` | `pihole-db` |
| `/opt/AdGuardHome/data/querylog.json`, `/var/lib/AdGuardHome/data/querylog.json`, `/var/snap/adguard-home/{current,common}/data/querylog.json` | `adguard-querylog` |
| `/var/log/dnsmasq.log` (only when no Pi-hole or AdGuard Home file is found) | `dnsmasq-log` |
| `/etc/pihole/dhcp.leases`, `/var/lib/misc/dnsmasq.leases`, `/tmp/dhcp.leases` | `leases` |
| `/proc/net/nf_conntrack` | `conntrack` |

Anything else needs a config file. phonehome reads `--config FILE`, then
`$PHONEHOME_CONFIG`, then `./phonehome.yaml`, then
`/etc/phonehome/phonehome.yaml`. The Docker image looks at
`/config/phonehome.yaml`. Every setting is described in
[packaging/phonehome.example.yaml](../../packaging/phonehome.example.yaml);
unknown keys are rejected, so a typo fails loudly instead of being ignored.

The settings that matter most:

```yaml
sources:                        # empty or absent = auto-detect
  - type: pihole-db             # pihole-db | pihole-api | adguard-querylog |
    path: /etc/pihole/pihole-FTL.db  #   dnsmasq-log | leases | conntrack
timezone: Europe/Berlin         # so "overnight" means your night
resolvers: ["192.168.1.2"]      # LAN IPs of your DNS servers (see below)
labels:
  "64:1c:ae:11:22:33": Living room TV   # MAC or IP → name
```

`resolvers:` matters only if you also read conntrack: your DNS server's own
upstream traffic is then not reported as "bypassing your DNS". When phonehome
runs on the DNS server itself and reads its files, it adds that machine's own
addresses automatically.

## When the dashboard stays empty

- **"phonehome hasn't found a DNS log yet".** None of the files above
  exists where phonehome runs. In Docker, check the volume mounts; if your
  DNS server simply has not created its files yet, wait: `serve` looks again
  every minute and the dashboard updates on its own. Files elsewhere need a
  config file (a listed file that does not exist yet is retried on every
  poll).
- **"Found … but permission denied".** The file is there but phonehome may
  not read it. The dashboard, the log and `phonehome report` say which file
  and what to do, for example:

  ```
  found /etc/pihole/pihole-FTL.db but permission denied: Pi-hole v6 lets only
  its group read the database; run phonehome with the file's group (GID 1000),
  e.g. Docker group_add: ["1000"] or systemd SupplementaryGroups=pihole
  ```

  Each guide explains the permissions involved. Once fixed, `serve` picks
  the file up within a minute; no restart needed.
- **"Found … but it is a directory or device, not a file".** Docker created
  an empty directory because a bind-mounted file did not exist yet. Mount
  the folder that contains the file instead.
- **"Waiting for the first lookups".** phonehome reads the file but nothing
  has arrived yet; the dashboard shows when each source was last checked,
  and the error if one is failing.
- **Lookups arrive late.** Pi-hole writes its database about once a minute,
  and AdGuard Home writes its query log in batches. phonehome polls every
  `interval:` (60 s by default).
- **Every lookup comes from one address, like `172.17.0.1`.** Your DNS
  server sees Docker's proxy, not your devices. That is a DNS-server setup
  issue; Pi-hole's and AdGuard's Docker docs explain host networking or
  macvlan. phonehome can only report the addresses your DNS server logged.
