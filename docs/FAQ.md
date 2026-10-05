# FAQ

### I already run Pi-hole with good blocklists. Why do I need this?

You don't *need* it, and it doesn't replace them. A blocklist answers "should
this lookup be allowed?". phonehome answers "what is each of my devices
trying to do, and how much of it is about me?". It reads the log your
blocker already keeps and turns it into a per-device story: *this TV looked
up its content-recognition server 1,440 times a day, all night, every
minute*.

That is useful even when everything is blocked. A blocked lookup is still an
attempt, so phonehome counts it ([why](grading.md#the-terms)). It also shows
which devices try to get around your blocker, and which fixes in the
device's own settings turn the behaviour off at the source, which survives
the device being moved to another network.

### Does phonehome itself phone home?

No. It makes no network connections except to the sources you configure
and, if you turn alerts on, the alert targets you configure (your webhook,
ntfy, Gotify or MQTT broker). Of the sources, only the Pi-hole API one talks
over the network at all. No telemetry, no update checks, no accounts, no
fonts or scripts from a CDN; the dashboard works offline. The knowledge base
ships inside the binary.

You can check: the only outgoing clients are the Pi-hole API source in
[`internal/source/pihole/api.go`](../internal/source/pihole/api.go) and the
alert senders in [`internal/alert/`](../internal/alert/), which do nothing
until you configure a target ([docs/alerts.md](alerts.md) shows exactly what
they send). The release binaries are built by a public
[workflow](../.github/workflows/release.yml) from tagged source.

### What about DNS-over-HTTPS? Devices can just skip my Pi-hole.

Yes, and that is a real blind spot. A lookup sent over DoH to a resolver
other than yours never reaches your Pi-hole, so phonehome never sees it.

What phonehome can do:

- **From DNS alone**, flag devices that look up known encrypted-DNS services
  (`dns.google`, `cloudflare-dns.com`, …). That shows a device *can* bypass
  you, not that it did (medium confidence).
- **With connection data (conntrack)**, on the router, flag actual
  connections to well-known public resolvers on port 443, DNS-over-TLS on
  853, and plain DNS sent straight to the internet (high confidence).

What it can't: encrypted DNS to a resolver not on its list, or anything
inside a VPN, looks like any other HTTPS connection. See
[detectors.md](detectors.md#dns-bypass-routing-around-your-filter).

### Almost everything is encrypted. What can it actually see?

Who and when, never what. phonehome knows your TV looked up
`acr-us-prd.samsungcloud.tv` at 03:12 and 03:13 and 03:14. It cannot see
what was sent. Everything it says is phrased that way, and the knowledge
base cites a source (a paper, the vendor's own documentation) for what each
destination is known to be for, with a confidence level.

DNS lookups are also a lower bound: devices cache answers, so the real
number of connections is usually higher.

### Can't a domain name be misleading?

It can. That's why every knowledge-base rule links to its evidence and
carries a confidence level, why a domain we can't explain stays *unknown*
rather than guessed, and why lots of traffic is labelled *essential*
(firmware updates, time sync, the cloud a camera needs) so nobody bricks a
device by blocking it. If you think a rule is wrong,
[open an issue](https://github.com/bizpers11991-code/phonehome/issues/new?template=new-device.yml)
with your evidence.

### How are grades worked out?

From one number: *snooping* lookups (content recognition, advertising,
tracking, telemetry) per day, plus two automatic downgrades: content
recognition, and a device bypassing your DNS. Under 50 a day is an A; 5,000
a day or an ACR heartbeat is an F. The rules are short enough to check by
hand: [grading.md](grading.md).

### Will it run on a Raspberry Pi Zero?

There is an armv6 build for the Pi Zero and Pi 1, and the binary is about
14 MB with no dependencies. We have not measured it on a Zero yet. The thing
to watch is RAM: a report loads the lookups for the period you view into
memory. For scale, analysing one million lookups (a busy week for a large
home) takes about 0.2 s and 56 MB of allocations on a desktop CPU in our
benchmark; a Zero will be many times slower. Shorter retention and shorter
report periods keep it light. The systemd unit and Docker image set
`GOMEMLIMIT=128MiB`, which holds a 30-day report for a busy home near 128 MiB
on 32-bit instead of ~200 MiB ([measurements](performance.md)); change it
with the environment variable. Numbers from real Zeros are very welcome.

### How much does it store, and for how long?

It copies lookups into its own SQLite database (by default
`/var/lib/phonehome/phonehome.db`, `/data` in Docker) and deletes anything
older than `retention_days` (default 90; `0` keeps everything). Nothing is
stored anywhere else. Uninstalling and deleting that file removes it all.
It never modifies your Pi-hole or AdGuard data.

### Is the receipt safe to share?

It's designed to be shared, but look before you post. A receipt shows the
device's name, the period, lookup counts by category, the companies it talks
to most, heartbeat domains, overnight activity and bypass findings. A
whole-home receipt lists your devices by name. Names come from your labels
or your DHCP server, so they may include a person's name ("Anna's iPad");
rename devices with `labels:` first if that matters. Counts and timing also
hint at a household's routine.

### Does it need root, or change anything on my network?

No and no. It reads files and, optionally, the Pi-hole API. It never writes
to your DNS server, never scans or ARP-spoofs, and the systemd unit runs it
sandboxed as a throwaway user. AdGuard Home is the exception on permissions:
its query log is readable by root only, so the AdGuard guides run phonehome
as root with every capability dropped ([why](setup/adguard-home.md)).

### The dashboard says it found my DNS log but can't read it

phonehome looks for Pi-hole, AdGuard Home and dnsmasq files in their usual
places. When one exists but cannot be opened, the dashboard, the log
(`source found but not readable`) and `phonehome report` say which file and
how to fix it. Usually:

- **Pi-hole v6**: `pihole-FTL.db` is readable by the `pihole` group only.
  Docker: `group_add: ["1000"]` (the GID in the message); systemd:
  `SupplementaryGroups=pihole`.
- **AdGuard Home**: `querylog.json` is readable by root only; run phonehome
  as root with every capability dropped, see
  [the guide](setup/adguard-home.md).
- **dnsmasq logs**: run phonehome with the log file's group.

You do not need to restart: `phonehome serve` looks again every minute until
it finds a DNS source. More in
[setup/README.md](setup/README.md#when-the-dashboard-stays-empty).

### Why does a device show up as just an IP address?

DNS logs record IPs, not devices. phonehome names devices from Pi-hole's
network table or DHCP leases when it can read them. Otherwise, name them
yourself with `labels:` (by MAC or IP) or in the dashboard. Phones and
laptops that use random MAC addresses per network may appear under a
changing identity.

### Which setups are supported?

Pi-hole v5/v6 (database or v6 API), AdGuard Home, dnsmasq (OpenWrt or
plain), DHCP leases and Linux conntrack. Step-by-step guides, including
Docker, Unraid, Synology and Proxmox, are in [setup/](setup/README.md).
