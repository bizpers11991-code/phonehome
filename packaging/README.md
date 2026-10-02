# Packaging

| Path | What |
|---|---|
| `phonehome.example.yaml` | Every config setting, with defaults. Shipped in the release tarballs. |
| `systemd/phonehome.service` | Sandboxed unit used by `scripts/install.sh --systemd`. Shipped in the tarballs. |
| `compose/pihole.yml`, `compose/adguard.yml` | Docker Compose files for phonehome next to Pi-hole or AdGuard Home. See [docs/setup](../docs/setup/README.md). |
| `homebrew/phonehome.rb.in` | Homebrew formula template (prebuilt binaries, macOS and Linux). |
| `aur/PKGBUILD.in` | AUR `phonehome-bin` template (prebuilt binaries, x86_64/aarch64/armv7h/armv6h). |
| `render.sh` | Fills both templates from a release's `SHA256SUMS`. |

## Homebrew and AUR

Publishing to a Homebrew tap or the AUR needs credentials for another
repository, which the release workflow deliberately does not hold. Instead,
each release renders the formula and PKGBUILD and attaches them to the GitHub
release as `phonehome.rb` and `PKGBUILD`. CI renders them on every pull
request with dummy checksums so the templates cannot rot.

To render by hand:

```sh
curl -fsSLO https://github.com/bizpers11991-code/phonehome/releases/download/v0.1.0/SHA256SUMS
sh packaging/render.sh v0.1.0 SHA256SUMS out/
```

### Publishing the formula (tap maintainer)

```sh
git clone https://github.com/<owner>/homebrew-phonehome   # a repo named homebrew-*
mkdir -p homebrew-phonehome/Formula
cp out/phonehome.rb homebrew-phonehome/Formula/
cd homebrew-phonehome && git commit -am "phonehome 0.1.0" && git push
```

Users then run `brew install <owner>/phonehome/phonehome`. The formula
installs the binary, `etc/phonehome/phonehome.yaml` and a `brew services`
definition. On macOS there is no Pi-hole database to read locally, so the
useful source is [`pihole-api`](../docs/setup/pihole-api.md).

Tested: `brew install`, `brew test` and `brew style` pass on Linux
(`homebrew/brew` image) for v0.1.0. Not yet tested on macOS.

### Publishing to the AUR (package maintainer)

```sh
git clone ssh://aur@aur.archlinux.org/phonehome-bin.git
cp out/PKGBUILD phonehome-bin/
cd phonehome-bin && makepkg --printsrcinfo > .SRCINFO
git add PKGBUILD .SRCINFO && git commit -m "0.1.0-1" && git push
```

The package installs `/usr/bin/phonehome`, the systemd unit and
`/etc/phonehome/phonehome.yaml` (kept on upgrade). The unit ships without
`SupplementaryGroups=pihole`, because systemd refuses to start a unit naming
a group that does not exist; Pi-hole users add it back as described in
[pihole-bare-metal.md](../docs/setup/pihole-bare-metal.md#how-it-gets-access).

Tested: `makepkg` builds the x86_64 package for v0.1.0. The ARM variants are
not tested.
