# OpenWrt and plain dnsmasq

*Partly tested: the `uci` settings below were checked on an OpenWrt snapshot
(x86-64 rootfs in Docker) to produce `log-queries=extra` and
`log-facility=/tmp/dnsmasq.log`, and phonehome read the resulting log. Running
phonehome on a physical router, the procd service and remote syslog are
untested.*

OpenWrt's DNS server is dnsmasq. With query logging on, every lookup becomes
a log line, which phonehome reads with the `dnsmasq-log` source. The same
works for any machine running dnsmasq.

There are two ways to do it:

- **A. phonehome on the router.** Simplest, and the only way to also read
  connection data (conntrack) to catch devices that bypass your DNS. Needs
  an **arm64 or armv7** router with storage to spare.
- **B. phonehome on another machine** (a Pi, a NAS, a server) that receives
  the router's log over syslog. Works with any router, including MIPS ones.

phonehome has no MIPS build: its pure-Go SQLite library does not support
MIPS, and many budget routers are MIPS. Check with `uname -m` on the router
(`aarch64` = arm64, `armv7l` = armv7). Those routers use option B.

## Turn on query logging

On the router, over SSH:

```sh
uci set dhcp.@dnsmasq[0].logqueries='1'
uci set dhcp.@dnsmasq[0].logfacility='/tmp/dnsmasq.log'   # option A only
uci commit dhcp
/etc/init.d/dnsmasq restart
```

`logqueries` makes OpenWrt start dnsmasq with `--log-queries=extra`, which
numbers each query so phonehome can pair it with its answer. `logfacility`
writes the log to a file instead of the system log.

`/tmp` is RAM. dnsmasq never trims the file, so keep it small. A busy home
logs a few MB a day. A cron job (**System › Scheduled Tasks**) can empty it
when it grows; phonehome notices the truncation and starts again from the top,
losing at most the lines written since its last poll:

```cron
*/30 * * * * [ "$(wc -c < /tmp/dnsmasq.log)" -gt 8000000 ] && : > /tmp/dnsmasq.log
```

(Renaming the file and sending `SIGUSR2` is the usual rotation, but OpenWrt
runs dnsmasq in a jail with that file bind-mounted, so truncating is safer.)

## A. phonehome on the router

1. Download the `linux-arm64` or `linux-armv7` tarball from the
   [releases page](https://github.com/bizpers11991-code/phonehome/releases)
   on a computer, check it against `SHA256SUMS`, and copy the binary to
   storage with ~15 MB free: a USB stick or extroot, not the internal flash
   of a small router. Below, that is `/mnt/usb`.
2. Write `/etc/phonehome.yaml`:

   ```yaml
   db: /mnt/usb/phonehome/phonehome.db     # persistent storage, not /tmp
   retention_days: 30
   sources:
     - type: dnsmasq-log
       path: /tmp/dnsmasq.log
     - type: leases
       path: /tmp/dhcp.leases               # device names from DHCP
     - type: conntrack                      # /proc/net/nf_conntrack
   ```

   Listing `sources:` turns auto-detection off, so list all three.
3. Try it: `/mnt/usb/phonehome serve --config /etc/phonehome.yaml`, then
   open `http://192.168.1.1:8099`.
4. Run it as a service with `/etc/init.d/phonehome` (procd):

   ```sh
   #!/bin/sh /etc/rc.common
   START=99
   USE_PROCD=1

   start_service() {
       procd_open_instance
       procd_set_param command /mnt/usb/phonehome serve --config /etc/phonehome.yaml
       procd_set_param respawn
       procd_set_param stdout 1
       procd_set_param stderr 1
       procd_close_instance
   }
   ```

   `chmod +x /etc/init.d/phonehome && /etc/init.d/phonehome enable && /etc/init.d/phonehome start`

Because phonehome runs on the resolver itself, the router's own upstream DNS
traffic is not reported as a bypass. For byte counts on connections, enable
conntrack accounting: `sysctl -w net.netfilter.nf_conntrack_acct=1`.

Mind the router's RAM: phonehome loads the lookups for the period you are
viewing into memory, so a 7-day view on a busy network may need tens of MB.

## B. phonehome on another machine (remote syslog)

Send the router's system log, which includes dnsmasq's query lines, to the
machine running phonehome. Skip the `logfacility` line above.

On the router:

```sh
uci set system.@system[0].log_ip='192.168.1.10'   # the phonehome machine
uci set system.@system[0].log_proto='udp'
uci set system.@system[0].log_port='514'
uci commit system
/etc/init.d/log restart
```

On the receiving machine, with rsyslog, write the router's messages to their
own file (`/etc/rsyslog.d/30-openwrt.conf`):

```
module(load="imudp")
input(type="imudp" port="514")
if $fromhost-ip == '192.168.1.1' then {
    action(type="omfile" file="/var/log/openwrt.log" template="RSYSLOG_FileFormat")
    stop
}
```

Restart rsyslog, and point phonehome at the file:

```yaml
sources:
  - type: dnsmasq-log
    path: /var/log/openwrt.log
```

`RSYSLOG_FileFormat` writes RFC 3339 timestamps with a time zone. With the
classic format (`Oct  2 14:01:00`) phonehome assumes the local zone of the
machine it runs on (`TZ` in Docker), which must then match the router's.

Lines from other programs in the file are ignored. phonehome follows the file
across rotation if your logrotate renames it to `openwrt.log.1` (the default
without `dateext` and without compressing the newest rotation, i.e. use
`delaycompress`). The file must be readable by phonehome: with the systemd
unit, add the file's group with `sudo systemctl edit phonehome` →
`SupplementaryGroups=adm` (or whichever group owns it).

Device names: the router's `/tmp/dhcp.leases` is not on this machine. Name
devices with `labels:` in the config, or copy the leases file over and add a
`leases` source.

## Plain dnsmasq (Debian, Raspberry Pi OS, …)

Add to `/etc/dnsmasq.conf` (or a file in `/etc/dnsmasq.d/`):

```
log-queries=extra
log-facility=/var/log/dnsmasq.log
```

and configure

```yaml
sources:
  - type: dnsmasq-log
    path: /var/log/dnsmasq.log
  - type: leases
    path: /var/lib/misc/dnsmasq.leases
```

Rotate the log with logrotate (`postrotate` sends `SIGUSR2` so dnsmasq
reopens it). Pi-hole users don't need this: the `pihole-db` source is better.
