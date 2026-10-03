# Pi-hole v6 on another machine (API)

*Tested in Docker against `pihole/pihole:latest` (v6) with an app password.*

Use this when phonehome cannot see Pi-hole's files: Pi-hole runs on a
different box, or you would rather not share its directory. phonehome reads
Pi-hole's query log over the REST API with an **app password**.

This needs Pi-hole **v6**; v5 has no such API. On a v5 host, run phonehome
on that host instead ([bare metal](pihole-bare-metal.md) or
[Docker](pihole-docker.md)).

## 1. Create an app password

An app password works without two-factor codes and can be revoked without
changing your main password.

1. Open the Pi-hole web interface and go to **Settings › Web interface / API**.
2. Switch the settings mode from *Basic* to **Expert** (top right).
3. Click **Configure app password**. Pi-hole shows a new password once.
   Copy it, then click **Enable new app password**.

Treat it like your admin password: it grants the same API access. Each
Pi-hole has a single app password; generating a new one replaces the old one
and logs out everything using it.

Store it in a file that only root can read:

```sh
sudo install -d -m 0755 /etc/phonehome
printf '%s\n' 'PASTE-THE-APP-PASSWORD' | sudo tee /etc/phonehome/pihole-app-password >/dev/null
sudo chmod 0600 /etc/phonehome/pihole-app-password
```

The systemd unit runs phonehome as a throwaway user that cannot read a
root-only file, so hand it over as a systemd credential
(`sudo systemctl edit phonehome`):

```ini
[Service]
LoadCredential=pihole-app-password:/etc/phonehome/pihole-app-password
```

systemd then exposes it to phonehome alone at
`/run/credentials/phonehome.service/pihole-app-password`. (Needs systemd 247
or later: Debian 11, Ubuntu 22.04, Raspberry Pi OS bullseye or newer. *Not
yet tested on real hardware;* our container test environment cannot apply
credential permissions. If it fails, `journalctl -u phonehome` shows
`reading password file: … permission denied`.)

## 2. Configure phonehome

```yaml
# /etc/phonehome/phonehome.yaml
sources:
  - type: pihole-api
    name: upstairs-pihole          # optional; keep it stable once set
    url: http://192.168.1.2        # Pi-hole's address, no /admin or /api
    password_file: /run/credentials/phonehome.service/pihole-app-password
resolvers: ["192.168.1.2"]         # only matters with conntrack
```

| Field | Notes |
|---|---|
| `url` | `http://` or `https://` plus host and optional port. phonehome adds `/api/...` itself. |
| `password_file` | Preferred. Trailing whitespace and newlines are stripped. |
| `password` | Inline alternative. Set one or the other, not both. Leave both out only if your Pi-hole has no password at all. |
| `name` | Defaults to `pihole-api`. It also keys how far phonehome has read, so do not rename it later. |
| `path` | Not allowed for this type. |

Then restart phonehome (`sudo systemctl restart phonehome`).

phonehome also reads the devices Pi-hole knows (`/api/network/devices`:
MAC address, maker, addresses and host names), so devices get their names
rather than bare IP addresses. In the dashboard's footer this appears as a
second source, `<name>-devices`.

Lookups are read once they are 30 seconds old, so they reach the dashboard
about half a minute late. Until then Pi-hole may still mark a lookup as
blocked, for example when the reply's CNAME chain hits your blocklist.

### In Docker

```yaml
  phonehome:
    image: ghcr.io/bizpers11991-code/phonehome:latest
    ports: ["8099:8099"]
    volumes:
      - phonehome-data:/data
      - ./phonehome.yaml:/config/phonehome.yaml:ro
      - ./pihole-app-password:/config/pihole-app-password:ro
```

with `password_file: /config/pihole-app-password` in `phonehome.yaml`. The
container runs as UID 65532, so give the file to that user:
`sudo chown 65532 pihole-app-password && sudo chmod 0400 pihole-app-password`.
(We tested the mount; the ownership line is standard Unix permissions.)

## What you get, and what you don't

- **DNS lookups**, with the client IP Pi-hole logged and whether Pi-hole
  blocked them.
- **No device names or MAC addresses.** The API source reads lookups only.
  Name devices with `labels:` (by IP), or add a `leases` source if phonehome
  can read your DHCP server's lease file.

## Security notes

- Over `http://` the app password and the query log cross your LAN
  unencrypted. If that matters, use `https://` with a certificate phonehome
  trusts. There is no option to skip certificate checks, so Pi-hole's
  self-signed default certificate will be rejected.
- phonehome logs in, keeps the session while it runs, and logs out when it
  stops. It only reads; it never changes Pi-hole's settings.
- If Pi-hole refuses the login, phonehome's status says
  `login refused: check the app password`.

## Without the web interface

The same thing over the API, with your main password (for scripts or a
headless Pi-hole):

```sh
PH=http://192.168.1.2
SID=$(curl -s -X POST "$PH/api/auth" -d '{"password":"YOUR-MAIN-PASSWORD"}' | jq -r .session.sid)
curl -s -H "X-FTL-SID: $SID" "$PH/api/auth/app" > app.json   # new password + hash
jq -r .app.password app.json                                  # save this one
curl -s -X PATCH -H "X-FTL-SID: $SID" "$PH/api/config" \
  -d "$(jq '{config:{webserver:{api:{app_pwhash:.app.hash}}}}' app.json)" >/dev/null
curl -s -X DELETE -H "X-FTL-SID: $SID" "$PH/api/auth"
```
