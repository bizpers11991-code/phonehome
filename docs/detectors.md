# What phonehome detects, and what it cannot

phonehome reads logs your network already keeps: DNS lookups from Pi-hole,
AdGuard Home or dnsmasq, and, if you have it, connection tracking (conntrack)
from your router. From those it reports patterns. This page says exactly how
each finding is made and where it stops being reliable.

Two limits apply to everything below:

- **Encrypted traffic.** Almost all of this traffic is encrypted. We see *who*
  a device contacts, *when*, and *how often*, never *what* it sends.
- **DNS caching.** A device that already knows a server's address does not
  ask again until the answer expires (its TTL, often minutes to hours). So one
  lookup can stand for many connections. Every count here is a **lower bound**
  on how often the device actually connects.

## Attribution: which device made the lookup

**What it does.** Each lookup carries the IP address that asked. We match it
to a known device (from your DHCP leases or Pi-hole's network table). If two
devices have held the same address, the one seen most recently gets it.
Addresses we cannot match appear as their own entry, named after the IP.

**What it cannot tell you.**
- **Shared addresses and NAT.** If a device sits behind another router, a
  mesh node in router mode, or a VPN, everything behind it looks like one
  device.
- **Address changes.** If a device's address moved during the period and your
  leases do not record its history, older lookups may land on whichever
  device holds that address now.
- **Randomized MAC addresses.** Phones and laptops often use a different
  private MAC on each network or rotate it. One phone can appear as several
  devices, and its maker cannot be read from the MAC.
- **Resolvers in the middle.** If your devices ask your router, and the router
  asks Pi-hole, Pi-hole sees only the router. Point devices at Pi-hole
  directly (via DHCP) for per-device results.

## Classification: what each destination is for

**What it does.** Every domain is looked up in phonehome's knowledge base,
which says who runs it and what it is for (content recognition, advertising,
tracking, telemetry, essential, content), with a confidence level and links to
evidence. "Snooping" means the first four.

**What it cannot tell you.** The knowledge base describes what a server is
*known* to be used for. A domain with several jobs is classed by its main one.
Domains it has not catalogued are shown as *unknown*, never guessed.

## Device type

**What it does.** We guess what each device is from, in order of trust: the
traffic it makes (a device that mostly talks to TV-only services is a TV, once
we have at least 5 such lookups making up at least half of the hinted ones),
the type your source already recorded, its name (your label, then its network
hostname, such as `Samsung-TV` or `RingDoorbell`), the maker in its MAC
address (only for makers of one kind of product), and finally a weak majority
of hinted traffic.

**What it cannot tell you.** Names are chosen by manufacturers and users and
can mislead. A phone running a TV app may carry a TV hint. The type is a
convenience for picking fixes, not a finding.

## Heartbeats: "it calls home on a clock"

**What it measures.** For each device and destination with at least 20
distinct lookup moments spread over at least 6 hours, we take the gaps between
consecutive lookups. Lookups within 2 seconds of each other count as one
moment (devices ask for IPv4 and IPv6 addresses together, and retry). We drop
the shortest and longest 10% of gaps, so a night unplugged or a burst of
active use does not hide a pattern, then take the median gap and its
variability (coefficient of variation). If the median is an hour or less and
the variability is at most 0.35, we report a heartbeat "every *median*".

**Why it matters.** People use their devices irregularly. A destination
contacted on a steady clock, all day and all night, is contacted whether or
not anyone is using the device.

**What it cannot tell you.**
- The cadence is of **lookups**, shaped by DNS caching. The device may connect
  far more often than it looks things up, never less.
- A steady clock can be harmless (time sync, update checks). We report
  heartbeats to every category and say which category each one is; only
  content-recognition heartbeats affect the grade.
- Devices that keep one connection open for hours make few lookups and will
  not show a heartbeat even if they report constantly over it.

## Quiet hours

**What it measures.** The number of lookups made between 01:00 and 06:00 local
time (configurable; the window may wrap past midnight, e.g. 23:00–06:00).

**Why it matters.** Most households are asleep. Traffic then is mostly the
device acting on its own.

**What it cannot tell you.** Whether anyone was awake. Some homes are busy at
3 a.m.; scheduled updates and backups legitimately run at night.

## DNS bypass: routing around your filter

A DNS filter only sees lookups that are sent to it. These findings mean a
device can, or does, send lookups somewhere else, where your blocklist does
not apply and phonehome cannot see them.

| Finding | Confidence | Evidence |
|---|---|---|
| `doh-lookup` | medium | The device looked up a known encrypted-DNS service (e.g. `dns.google`, `cloudflare-dns.com`, `dns.quad9.net`). |
| `doh-flow` | high | A connection to a major public DNS resolver's address (Google, Cloudflare, Quad9, OpenDNS, AdGuard) on port 443. |
| `dot-flow` | high | A connection to a public address on port 853, the DNS-over-TLS port. |
| `foreign-dns` | high | Plain DNS (port 53) sent straight to a public address instead of your resolver. |

The full list of resolver hostnames and addresses is in
`internal/analyze/bypass.go`. Each finding cites the most frequent domain or
address and port as evidence.

**What it cannot tell you.**
- `doh-lookup` shows the device *can* use encrypted DNS, not that it did. Many
  phones look up `dns.google` routinely to test whether it is available.
- `use-application-dns.net` is never flagged. Firefox looks it up to ask your
  network whether to *disable* encrypted DNS; it is a sign of cooperation.
- The three connection findings need conntrack. In DNS-only mode they cannot
  appear, and their absence proves nothing.
- Encrypted DNS sent to a resolver that is not on our list, or tunnelled
  inside a VPN, looks like any other HTTPS connection and is not detected.
- Your own DNS filter forwards lookups to a public resolver; that is its job,
  not a bypass. Connections from the addresses configured as your resolvers
  are therefore exempt from the three connection findings (they are still
  counted and analysed otherwise). If the filter's address is not configured,
  the machine it runs on will show `foreign-dns`, `dot-flow` or `doh-flow`.
  It can still show `doh-lookup` if it looks up an encrypted-DNS service
  itself; that finding is medium confidence and does not affect the grade.
