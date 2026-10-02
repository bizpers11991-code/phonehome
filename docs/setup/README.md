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

## Configuration in one minute

With no config file, phonehome looks for these files **once, at startup**,
and uses every one it can read:

| File | Source type |
|---|---|
| `/etc/pihole/pihole-FTL.db` | `pihole-db` |
| `/opt/AdGuardHome/data/querylog.json`, `/var/lib/AdGuardHome/data/querylog.json`, `/var/snap/adguard-home/{current,common}/data/querylog.json` | `adguard-querylog` |
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

- **"No sources" right after first start.** Auto-detection runs once, at
  startup. If your DNS server had not created its files yet, restart
  phonehome, or list the source in a config file (a listed file that does
  not exist yet is retried on every poll).
- **A source is not detected although the file exists.** phonehome could not
  read it. Each guide explains the permissions involved; `phonehome ingest
  --once` prints which sources it found.
- **Lookups arrive late.** Pi-hole writes its database about once a minute,
  and AdGuard Home writes its query log in batches. phonehome polls every
  `interval:` (60 s by default).
- **Every lookup comes from one address, like `172.17.0.1`.** Your DNS
  server sees Docker's proxy, not your devices. That is a DNS-server setup
  issue; Pi-hole's and AdGuard's Docker docs explain host networking or
  macvlan. phonehome can only report the addresses your DNS server logged.
