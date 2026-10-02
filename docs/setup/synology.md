# Synology DSM

*Untested on a Synology. The compose files are the ones tested in
[pihole-docker.md](pihole-docker.md) and [adguard-home.md](adguard-home.md);
the DSM steps follow Container Manager (DSM 7.2).*

phonehome runs in **Container Manager** next to your Pi-hole or AdGuard Home
container. Models with an x86-64 or arm64 CPU can run the image (it is
published for amd64, arm64 and armv7).

## Pi-hole in Container Manager

1. Find the shared folder Pi-hole uses for `/etc/pihole`, for example
   `/volume1/docker/pihole/etc-pihole`.
2. **Container Manager › Project › Create**. Name it `phonehome`, choose a
   folder such as `/volume1/docker/phonehome`, select *Create
   docker-compose.yml* and paste:

   ```yaml
   services:
     phonehome:
       image: ghcr.io/bizpers11991-code/phonehome:latest
       container_name: phonehome
       restart: unless-stopped
       ports:
         - "8099:8099"
       environment:
         TZ: Europe/Berlin
       group_add: ["1000"]   # GID that owns pihole-FTL.db
       volumes:
         - phonehome-data:/data
         - /volume1/docker/pihole/etc-pihole:/etc/pihole:ro

   volumes:
     phonehome-data:
   ```

3. Build and start the project, then open `http://<nas>:8099`.

The named volume `phonehome-data` keeps phonehome's database. It avoids the
permission trouble of a bind-mounted folder, which DSM creates for your own
user rather than for the container's user (65532).

`group_add` must match the group that owns `pihole-FTL.db`. Pi-hole's image
uses 1000 unless you set `PIHOLE_GID`. Over SSH:
`stat -c %g /volume1/docker/pihole/etc-pihole/pihole-FTL.db`.

## AdGuard Home in Container Manager

Use [packaging/compose/adguard.yml](../../packaging/compose/adguard.yml) as the
project's compose file, keeping only the `phonehome` service and the
`configs:` block, and replace `./adguard/work/data` with the shared folder
mapped to AdGuard's `/opt/adguardhome/work` plus `/data`. If Container
Manager rejects the inline `configs:` block (older Compose), mount a
`phonehome.yaml` file at `/config/phonehome.yaml` with the same content
instead.

## Port conflicts

DSM itself uses ports 80 and 443, which is why Pi-hole on a Synology is
usually run with a macvlan network or different ports. phonehome only needs
8099; change the left side (`"8100:8099"`) if that is taken.
