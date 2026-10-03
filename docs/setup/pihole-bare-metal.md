# Pi-hole installed on the host

*The installer and systemd unit are tested on Debian 12 and Alpine (binary
only). Running them on a machine with a real bare-metal Pi-hole is not yet
tested end to end.*

This is for a Pi-hole installed with Pi-hole's own installer on Raspberry Pi
OS, Debian, Ubuntu or Fedora. phonehome runs on the same machine as a
sandboxed systemd service and reads `/etc/pihole/pihole-FTL.db`. It works
with Pi-hole v5 and v6.

## Install

```sh
curl -fsSLO https://raw.githubusercontent.com/bizpers11991-code/phonehome/main/scripts/install.sh
less install.sh             # it's short, read it
sudo sh install.sh --systemd
```

The script:

1. picks the release for your CPU (amd64, arm64, armv7, armv6 for the Pi
   Zero/1) and checks its SHA-256 against the release's `SHA256SUMS`;
2. installs `/usr/local/bin/phonehome`;
3. with `--systemd`, installs `/etc/systemd/system/phonehome.service`, copies
   the example config to `/etc/phonehome/phonehome.yaml` (if none exists) and
   starts the service.

Open `http://<pi-hole-host>:8099`. Lookups appear a minute or two after they
happen, because Pi-hole writes its database about once a minute.

Pin a version with `sudo PHONEHOME_VERSION=v0.1.0 sh install.sh --systemd`,
or install somewhere else with `sudo BINDIR=/opt/bin sh install.sh`.

## How it gets access

The unit runs phonehome as a throwaway user (`DynamicUser=yes`) that can
write only to `/var/lib/phonehome`. Pi-hole keeps its database readable by
the `pihole` group, so the unit adds that group
(`SupplementaryGroups=pihole`) and mounts `/etc/pihole` read-only. If the
machine has no `pihole` group, the installer removes that line, because
systemd refuses to start a unit that names a missing group.

**Installed Pi-hole after phonehome?** Add the group back:

```sh
sudo systemctl edit phonehome
```

```ini
[Service]
SupplementaryGroups=pihole
```

then `sudo systemctl restart phonehome`.

## Checks

```sh
systemctl status phonehome
journalctl -u phonehome -f
# … msg="auto-detected source" type=pihole-db path=/etc/pihole/pihole-FTL.db
```

If phonehome cannot read the database, the log says
`source found but not readable … err="permission denied"` with the group to
add, and the dashboard shows the same. `ls -l /etc/pihole/pihole-FTL.db`
should show group `pihole` with `r`.

## Configure

Edit `/etc/phonehome/phonehome.yaml` and `sudo systemctl restart phonehome`.
Useful settings: `timezone:`, `labels:` for device names, `auth:` for a
password on the dashboard. Pi-hole's own client list already supplies
hostnames and MAC addresses for devices it has seen.

## Update and uninstall

Run the installer again to update. To remove:

```sh
sudo systemctl disable --now phonehome
sudo rm /etc/systemd/system/phonehome.service /usr/local/bin/phonehome
sudo rm -r /etc/phonehome /var/lib/phonehome /var/lib/private/phonehome
sudo systemctl daemon-reload
```

(`DynamicUser=yes` keeps the state in `/var/lib/private/phonehome`, with
`/var/lib/phonehome` as a symlink.)
