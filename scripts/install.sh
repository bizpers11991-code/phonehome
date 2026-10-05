#!/bin/sh
# Install the latest phonehome release.
#
#   sh install.sh                 # install binary to /usr/local/bin
#   sh install.sh --systemd       # also install and enable the systemd unit
#   PHONEHOME_VERSION=v0.1.0 sh install.sh
#
# Downloads from GitHub Releases and refuses to install anything whose
# SHA-256 does not match the release's SHA256SUMS. Read it before running it.
set -eu

REPO="bizpers11991-code/phonehome"
BINDIR="${BINDIR:-/usr/local/bin}"
VERSION="${PHONEHOME_VERSION:-}"
WITH_SYSTEMD=0

for arg in "$@"; do
	case "$arg" in
	--systemd) WITH_SYSTEMD=1 ;;
	-h | --help)
		sed -n '2,9p' "$0"
		exit 0
		;;
	*)
		echo "unknown option: $arg" >&2
		exit 2
		;;
	esac
done

die() {
	echo "install.sh: $*" >&2
	exit 1
}

need() {
	command -v "$1" >/dev/null 2>&1 || die "needs '$1' but it is not installed"
}

fetch() { # url dest
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --proto '=https' --tlsv1.2 -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$2" "$1"
	else
		die "needs curl or wget"
	fi
}

sha256() { # file
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	else
		die "needs sha256sum or shasum to verify the download"
	fi
}

as_root() {
	if [ "$(id -u)" -eq 0 ]; then
		"$@"
	elif command -v sudo >/dev/null 2>&1; then
		sudo "$@"
	else
		die "needs root to run: $*"
	fi
}

os="$(uname -s)"
case "$os" in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) die "unsupported OS: $os" ;;
esac

arch="$(uname -m)"
case "$arch" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
armv7l | armv7*) arch=armv7 ;;
armv6l | armv6*) arch=armv6 ;; # Raspberry Pi Zero / 1
*) die "unsupported CPU architecture: $arch" ;;
esac

need tar
need uname

if [ -z "$VERSION" ]; then
	tmpjson="$(mktemp)"
	fetch "https://api.github.com/repos/$REPO/releases/latest" "$tmpjson"
	VERSION="$(sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' "$tmpjson" | head -n 1)"
	rm -f "$tmpjson"
	[ -n "$VERSION" ] || die "could not determine the latest release; set PHONEHOME_VERSION"
fi
case "$VERSION" in
v[0-9]*) ;;
*) die "unexpected version '$VERSION'" ;;
esac

name="phonehome_${VERSION}_${os}-${arch}"
base="https://github.com/$REPO/releases/download/$VERSION"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT INT TERM

echo "Downloading phonehome $VERSION for $os/$arch"
fetch "$base/$name.tar.gz" "$work/$name.tar.gz"
fetch "$base/SHA256SUMS" "$work/SHA256SUMS"

want="$(awk -v f="$name.tar.gz" '$2 == f || $2 == "*" f { print $1 }' "$work/SHA256SUMS")"
[ -n "$want" ] || die "$name.tar.gz is not listed in SHA256SUMS; refusing to install"
got="$(sha256 "$work/$name.tar.gz")"
[ "$want" = "$got" ] || die "checksum mismatch for $name.tar.gz (want $want, got $got); refusing to install"
echo "Checksum OK"

tar -xzf "$work/$name.tar.gz" -C "$work"
[ -f "$work/$name/phonehome" ] || die "archive does not contain phonehome"
as_root install -d "$BINDIR"
as_root install -m 0755 "$work/$name/phonehome" "$BINDIR/phonehome"
echo "Installed $BINDIR/phonehome"

if [ "$WITH_SYSTEMD" -eq 1 ]; then
	[ "$os" = linux ] || die "--systemd is only for Linux"
	need systemctl
	unit="$work/$name/systemd/phonehome.service"
	[ -f "$unit" ] || die "archive does not contain the systemd unit"
	if ! getent group pihole >/dev/null 2>&1; then
		echo "No 'pihole' group here: removing SupplementaryGroups=pihole from the unit"
		sed '/^SupplementaryGroups=pihole$/d' "$unit" >"$unit.new"
		mv "$unit.new" "$unit"
	fi
	if [ "$BINDIR" != /usr/local/bin ]; then
		sed "s|/usr/local/bin/phonehome|$BINDIR/phonehome|" "$unit" >"$unit.new"
		mv "$unit.new" "$unit"
	fi
	# The config can hold the dashboard password, so only root and the
	# service may read it. The service runs as a dynamic user; a static
	# "phonehome" group, added to its unit, lets it read the file.
	if ! getent group phonehome >/dev/null 2>&1; then
		if command -v groupadd >/dev/null 2>&1; then
			as_root groupadd --system phonehome
		else
			as_root addgroup -S phonehome
		fi
	fi
	sed '/^DynamicUser=yes$/a\
SupplementaryGroups=phonehome' "$unit" >"$unit.new"
	mv "$unit.new" "$unit"
	as_root install -m 0644 "$unit" /etc/systemd/system/phonehome.service
	as_root install -d -m 0755 /etc/phonehome
	if [ -f "$work/$name/phonehome.example.yaml" ] && [ ! -e /etc/phonehome/phonehome.yaml ]; then
		as_root install -m 0640 -g phonehome "$work/$name/phonehome.example.yaml" /etc/phonehome/phonehome.yaml
	fi
	as_root systemctl daemon-reload
	as_root systemctl enable --now phonehome.service
	echo "Started phonehome.service"
fi

cat <<EOF

Next steps:
  phonehome demo                    try it with a synthetic household
EOF
if [ "$WITH_SYSTEMD" -eq 1 ]; then
	cat <<EOF
  open http://$(hostname 2>/dev/null || echo localhost):8099
  edit /etc/phonehome/phonehome.yaml, then: sudo systemctl restart phonehome
  logs: journalctl -u phonehome -f
EOF
else
	cat <<EOF
  phonehome serve                   auto-detects Pi-hole / AdGuard Home and serves http://localhost:8099
  sh install.sh --systemd           run it as a service
EOF
fi
