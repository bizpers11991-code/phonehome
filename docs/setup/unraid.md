# Unraid

*Untested on Unraid. The container settings are the same ones tested in
[pihole-docker.md](pihole-docker.md) and [adguard-home.md](adguard-home.md);
the Unraid paths and IDs follow Unraid's defaults.*

There is no Community Applications template yet, so add the container by
hand: **Docker › Add Container**, toggle **Advanced View**, and fill in:

| Field | Value |
|---|---|
| Name | `phonehome` |
| Repository | `ghcr.io/bizpers11991-code/phonehome:latest` |
| Network Type | `bridge` |
| Extra Parameters | see below |
| Port | container `8099` → host `8099` |
| Path | container `/data` → host `/mnt/user/appdata/phonehome` (read/write) |
| Path | your DNS server's files, read-only (see below) |
| Variable | `TZ` = your zone, e.g. `Europe/Berlin` |

## Pi-hole (v6)

Add a path: container `/etc/pihole` → the host folder your Pi-hole
container maps to `/etc/pihole` (often `/mnt/user/appdata/pihole/pihole`),
access mode **Read Only**.

**Extra Parameters:**

```
--user 99:100 --group-add 1000
```

- `--user 99:100` (Unraid's `nobody:users`) lets phonehome write to its
  appdata folder, which Unraid creates as `nobody:users`. The image's default
  user (65532) cannot.
- `--group-add 1000` lets it read `pihole-FTL.db`, which Pi-hole v6 keeps
  readable by its `pihole` group. Check the number with
  `stat -c %g /mnt/user/appdata/pihole/pihole/pihole-FTL.db` in the Unraid
  terminal and use that.

Start Pi-hole before phonehome (phonehome looks for the database once, at
startup). In the logs you should see `auto-detected source type=pihole-db`.

## AdGuard Home

AdGuard keeps `querylog.json` readable by root only, so phonehome needs to
run as root, with no capabilities:

- Path: container `/opt/AdGuardHome/data` → the host folder that holds
  AdGuard's `work/data` (e.g. `/mnt/user/appdata/adguardhome/work/data`),
  **Read Only**. That container path is one phonehome auto-detects.
- **Extra Parameters:** `--user 0:0 --cap-drop ALL --security-opt no-new-privileges`

Restart phonehome once after AdGuard has written its first query log, or use
a config file as described in [adguard-home.md](adguard-home.md#docker).

## Pi-hole on another machine

Skip the DNS-file path and use the [API source](pihole-api.md): put
`phonehome.yaml` and the app-password file in
`/mnt/user/appdata/phonehome-config`, add a path container `/config` → that
folder (**Read Only**), use `password_file: /config/pihole-app-password` in
the config, and set Extra Parameters to `--user 99:100`. The image always
reads `/config/phonehome.yaml`.

## Docker Compose Manager

With the Compose Manager plugin, the files in
[packaging/compose/](../../packaging/compose/) work as they are; adjust the
host paths to `/mnt/user/appdata/…`.
