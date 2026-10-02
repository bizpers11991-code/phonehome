# Pi-hole v6 in Docker

*Tested with `pihole/pihole:latest` (v6) and Docker Compose v2.*

phonehome runs as a second container that mounts Pi-hole's `/etc/pihole`
directory **read-only** and reads `pihole-FTL.db` from it. It does not need
Pi-hole's password, its network, or any change to Pi-hole.

## New setup: Pi-hole and phonehome together

[`packaging/compose/pihole.yml`](../../packaging/compose/pihole.yml) starts
both:

```sh
mkdir pihole && cd pihole
curl -fsSLo compose.yml https://raw.githubusercontent.com/bizpers11991-code/phonehome/main/packaging/compose/pihole.yml
# edit TZ and FTLCONF_webserver_api_password
docker compose up -d
```

Pi-hole is at `http://<host>/admin`, phonehome at `http://<host>:8099`.
The Pi-hole service is a trimmed copy of
[Pi-hole's own example](https://github.com/pi-hole/docker-pi-hole#quick-start);
add its DHCP settings (`NET_ADMIN`, port 67) there if you use Pi-hole for DHCP.

## Existing Pi-hole: add phonehome next to it

Add this service to the compose file that runs Pi-hole. Change
`./etc-pihole` to whatever host directory your Pi-hole service mounts at
`/etc/pihole`:

```yaml
  phonehome:
    image: ghcr.io/bizpers11991-code/phonehome:latest
    restart: unless-stopped
    depends_on:
      pihole:
        condition: service_healthy
    ports:
      - "8099:8099"
    environment:
      TZ: Europe/Berlin
    group_add: ["1000"]
    volumes:
      - phonehome-data:/data
      - ./etc-pihole:/etc/pihole:ro
```

and declare the volume at the top level:

```yaml
volumes:
  phonehome-data:
```

Then `docker compose up -d phonehome`.

Running Pi-hole with plain `docker run` instead:

```sh
docker run -d --name phonehome --restart unless-stopped \
  -p 8099:8099 -e TZ=Europe/Berlin --group-add 1000 \
  -v phonehome-data:/data -v /path/to/etc-pihole:/etc/pihole:ro \
  ghcr.io/bizpers11991-code/phonehome:latest
```

## Why `group_add: ["1000"]`

Pi-hole v6 creates `pihole-FTL.db` with mode `0640`, owned by the `pihole`
user and group. In the official image that is UID/GID **1000** (you can
change it with Pi-hole's `PIHOLE_UID`/`PIHOLE_GID`). phonehome's image runs
as an unprivileged user (65532), so it needs to be in that group to read the
file. Check the number on your host:

```sh
stat -c %g ./etc-pihole/pihole-FTL.db
```

Without the right group, phonehome cannot read the database, skips it during
auto-detection and the dashboard says there is no source.

## Why `condition: service_healthy`

phonehome looks for `pihole-FTL.db` once, at startup. On the very first
start the file does not exist until Pi-hole is up. Waiting for Pi-hole's
built-in health check avoids that race. If your compose file cannot use it,
restart phonehome once after Pi-hole's first start.

## Checks

```sh
docker logs phonehome
# … msg="auto-detected source" type=pihole-db path=/etc/pihole/pihole-FTL.db
curl -s localhost:8099/api/status
```

Pi-hole writes lookups to its database about once a minute, so the first
ones appear one to two minutes after they happen.

## Optional

- **A config file.** Mount one at `/config/phonehome.yaml` (see
  [the example](../../packaging/phonehome.example.yaml)), e.g. to add
  `labels:` or a `timezone:`.
- **Basic auth.** Set `auth.username` and `auth.password_file` in the config
  file and mount the password file read-only. The file must be readable by
  UID 65532 or the group you added.
- **Connection data (conntrack)** only exists in the network namespace of the
  machine doing NAT. On a typical Pi-hole host that is not your router, so it
  adds nothing. See [docs/detectors.md](../detectors.md#dns-bypass-routing-around-your-filter).
