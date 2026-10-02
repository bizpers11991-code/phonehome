#!/bin/sh
# Render the Homebrew formula and AUR PKGBUILD for one release.
#
#   sh packaging/render.sh v0.1.0 dist/SHA256SUMS dist/
#
# Writes <outdir>/phonehome.rb and <outdir>/PKGBUILD. Fails if any tarball
# the templates need is missing from SHA256SUMS.
set -eu

[ $# -eq 3 ] || {
	echo "usage: $0 <tag> <SHA256SUMS> <outdir>" >&2
	exit 2
}
tag=$1
sums=$2
out=$3
here=$(cd "$(dirname "$0")" && pwd)

case "$tag" in
v[0-9]*) version=${tag#v} ;;
*)
	echo "render.sh: tag must look like v1.2.3, got '$tag'" >&2
	exit 2
	;;
esac
# AUR pkgver may not contain '-'; keep pre-releases out of these manifests.
case "$version" in
*-*)
	echo "render.sh: '$tag' is a pre-release; not rendering" >&2
	exit 2
	;;
esac

script="s|@VERSION@|$version|g"
for p in darwin-arm64 darwin-amd64 linux-amd64 linux-arm64 linux-armv7 linux-armv6; do
	f="phonehome_${tag}_${p}.tar.gz"
	sum=$(awk -v f="$f" '$2 == f || $2 == "*" f { print $1 }' "$sums")
	case "$sum" in
	[0-9a-f][0-9a-f]*) ;;
	*)
		echo "render.sh: $f is not listed in $sums" >&2
		exit 1
		;;
	esac
	script="$script;s|@SHA256_${p}@|$sum|g"
done

mkdir -p "$out"
sed "$script" "$here/homebrew/phonehome.rb.in" >"$out/phonehome.rb"
sed "$script" "$here/aur/PKGBUILD.in" >"$out/PKGBUILD"
if grep -n '@[A-Za-z0-9_-]*@' "$out/phonehome.rb" "$out/PKGBUILD"; then
	echo "render.sh: unrendered placeholders left" >&2
	exit 1
fi
echo "wrote $out/phonehome.rb and $out/PKGBUILD for $tag"
