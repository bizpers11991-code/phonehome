# Proxmox VE (LXC)

*Untested on Proxmox. The installer and systemd unit were tested in a
Debian 12 systemd container; LXC-specific behaviour below is from Proxmox's
documentation.*

Pi-hole and AdGuard Home are often run in their own LXC container on Proxmox.
There are two good layouts.

## Same container as Pi-hole or AdGuard Home (simplest)

Open the container's console (or `pct enter <ctid>` on the host) and follow
the bare-metal guide for your DNS server:

- Pi-hole: [pihole-bare-metal.md](pihole-bare-metal.md), i.e.
  `sudo sh install.sh --systemd`.
- AdGuard Home: [adguard-home.md › Bare metal](adguard-home.md#bare-metal-systemd).

phonehome reads the files locally and adds the container's own addresses to
`resolvers:` automatically.

**If the service fails with `status=226/NAMESPACE`**, systemd could not set
up the sandbox the unit asks for. Unprivileged containers need the
**nesting** feature for that: on the Proxmox host, *Container › Options ›
Features › Nesting* (or `pct set <ctid> --features nesting=1`), then restart
the container. Recent Proxmox versions tick it by default when you create an
unprivileged container in the web UI.

Memory: phonehome holds the lookups for the period you view in RAM while it
builds a report, so a busy network needs more. We have not measured it on
small containers yet; if yours is tight, watch `systemctl status phonehome`.

## Separate container, reading Pi-hole over its API

Keep the DNS container untouched and run phonehome in its own small Debian
LXC with the [Pi-hole API source](pihole-api.md). This needs Pi-hole v6.
DNS lookups work the same; device MAC addresses and hostnames from Pi-hole's
network table do not come over the API, so name devices with `labels:`.

Sharing Pi-hole's files between containers with a bind mount (`mp0`) also
works in principle, but the UIDs differ between unprivileged containers, so
the group permissions phonehome relies on rarely line up. The API is simpler.

## conntrack

Inside an LXC, `/proc/net/nf_conntrack` shows only that container's own
connections, not your network's. Bypass detection from connection data needs
phonehome on the router itself (see [openwrt.md](openwrt.md)).
