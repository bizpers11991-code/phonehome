#!/bin/sh
# End-to-end smoke test: build phonehome, start the demo, and check every
# endpoint over real HTTP: status codes, JSON shape, security headers,
# 404s and CSRF rejections. Needs curl and jq. CI runs it; so can you:
#
#	sh scripts/smoke.sh
set -eu

addr=127.0.0.1:${SMOKE_PORT:-18199}
base=http://$addr
work=$(mktemp -d)
pid=
cleanup() {
	[ -n "$pid" ] && kill "$pid" 2>/dev/null || true
	rm -rf "$work"
}
trap cleanup EXIT INT TERM

fails=0
ok() { printf 'ok   %s\n' "$1"; }
fail() { printf 'FAIL %s\n' "$1"; fails=$((fails + 1)); }

echo "building"
CGO_ENABLED=0 go build -o "$work/phonehome" ./cmd/phonehome
"$work/phonehome" version >/dev/null

# --metrics is accepted only by builds that have the metrics endpoint.
metrics=
if "$work/phonehome" demo -h 2>&1 | grep -q -- -metrics; then metrics=--metrics; fi
"$work/phonehome" demo --listen "$addr" --days 14 $metrics >"$work/demo.log" 2>&1 &
pid=$!
i=0
until curl -fsS -o /dev/null "$base/healthz" 2>/dev/null; do
	i=$((i + 1))
	if [ "$i" -gt 120 ]; then
		cat "$work/demo.log"
		echo "demo did not start"
		exit 1
	fi
	sleep 0.5
done

# get PATH writes the body to $work/body and headers to $work/head, and
# prints the status code.
get() {
	curl -sS -o "$work/body" -D "$work/head" -w '%{http_code}' "$@"
}
header() { grep -i "^$1:" "$work/head" | head -n 1 | cut -d: -f2- | tr -d '\r' | sed 's/^ *//'; }

# expect NAME CODE PATH...: the status code, then the security headers every
# response must carry.
expect() {
	name=$1 want=$2
	shift 2
	got=$(get "$@")
	if [ "$got" != "$want" ]; then
		fail "$name: status $got, want $want"
		return 1
	fi
	csp=$(header content-security-policy)
	case $csp in
	*"default-src 'self'"*"script-src 'self'"*"frame-ancestors 'none'"*) ;;
	*) fail "$name: Content-Security-Policy is \"$csp\"" && return 1 ;;
	esac
	[ "$(header x-content-type-options)" = nosniff ] || { fail "$name: X-Content-Type-Options missing" && return 1; }
	[ "$(header referrer-policy)" = no-referrer ] || { fail "$name: Referrer-Policy missing" && return 1; }
	ok "$name"
}

# jqcheck NAME FILTER: FILTER must be true for the last body.
jqcheck() {
	if jq -e "$2" "$work/body" >/dev/null 2>&1; then ok "$1"; else fail "$1: $2"; fi
}

expect "dashboard" 200 "$base/"
grep -q '<script src="assets/app.js"' "$work/body" && ok "dashboard loads app.js" || fail "dashboard has no app.js"
for a in app.js app.css icons.svg favicon.svg; do
	expect "asset $a" 200 "$base/assets/$a"
done
expect "missing asset" 404 "$base/assets/nope.js"
expect "healthz" 200 "$base/healthz"

for days in 1 7 30; do
	expect "report days=$days" 200 "$base/api/report?days=$days"
	jqcheck "report days=$days shape" \
		'.demo == true and (.total|type) == "number" and (.devices|length) > 0 and (.grade|test("^[A-F]$"))
		 and all(.devices[]; (.id|type) == "string" and (.grade|test("^[A-F]$")) and (.topDomains|type) == "array")
		 and (.categories|length) > 0'
done
expect "report days=2" 400 "$base/api/report?days=2"
jqcheck "error body" '(.error|type) == "string"'

expect "status" 200 "$base/api/status"
jqcheck "status shape" '.demo == true and (.devices|type) == "number" and (.sources|type) == "array"'

get "$base/api/report?days=7" >/dev/null
dev=$(jq -r '.devices[0].id' "$work/body")
devq=$(jq -rn --arg d "$dev" '$d|@uri')

expect "home receipt svg" 200 "$base/receipt/home.svg?days=7"
case $(header content-type) in image/svg+xml*) ok "svg content type" ;; *) fail "svg content type $(header content-type)" ;; esac
grep -q "DEMO" "$work/body" && ok "receipt is labelled demo" || fail "receipt not labelled demo"
expect "home receipt png" 200 "$base/receipt/home.png?days=7"
[ "$(head -c 8 "$work/body" | od -An -tx1 | tr -d ' \n')" = 89504e470d0a1a0a ] && ok "png signature" || fail "not a PNG"
expect "device receipt" 200 "$base/receipt/$devq.svg?days=1"
expect "receipt of unknown device" 404 "$base/receipt/mac%3A00%3A00%3A00%3A00%3A00%3A01.svg"
expect "receipt bad format" 404 "$base/receipt/home.gif"
expect "unknown route" 404 "$base/api/nope"

# Renaming: allowed same-origin with JSON, refused cross-site or as a form.
label() { get -X POST "$base/api/devices/$devq/label" "$@"; }
code=$(label -H 'Content-Type: application/json' -H 'Sec-Fetch-Site: same-origin' --data '{"label":"Smoke test"}')
[ "$code" = 204 ] && ok "rename same-origin" || fail "rename same-origin: $code"
get "$base/api/report?days=7" >/dev/null
jq -e --arg d "$dev" '.devices[] | select(.id == $d) | .name == "Smoke test"' "$work/body" >/dev/null && ok "rename visible in report" || fail "rename not visible"
code=$(label -H 'Content-Type: application/json' -H 'Sec-Fetch-Site: cross-site' --data '{"label":"x"}')
[ "$code" = 403 ] && ok "CSRF: cross-site refused" || fail "CSRF: cross-site got $code"
code=$(label -H 'Content-Type: application/json' -H 'Origin: http://evil.example' --data '{"label":"x"}')
[ "$code" = 403 ] && ok "CSRF: foreign Origin refused" || fail "CSRF: foreign Origin got $code"
code=$(label -H 'Content-Type: application/x-www-form-urlencoded' --data 'label=x')
[ "$code" = 403 ] && ok "CSRF: form post refused" || fail "CSRF: form post got $code"
code=$(label -H 'Content-Type: application/json' -H 'Sec-Fetch-Site: same-origin' --data '{"label":"a\u0007b"}')
[ "$code" = 400 ] && ok "control characters refused" || fail "control characters got $code"
code=$(get -X POST "$base/api/devices/mac%3A00%3A00%3A00%3A00%3A00%3A01/label" -H 'Content-Type: application/json' -H 'Sec-Fetch-Site: same-origin' --data '{"label":"x"}')
[ "$code" = 404 ] && ok "rename unknown device: 404" || fail "rename unknown device: $code"
code=$(get -X GET "$base/api/devices/$devq/label")
[ "$code" = 405 ] && ok "label needs POST" || fail "GET label: $code"

# Endpoints newer builds have.
code=$(get "$base/api/devices/$devq/domains?days=7")
if [ "$code" != 404 ]; then
	[ "$code" = 200 ] && ok "device domains" || fail "device domains: $code"
	jqcheck "domains shape" '(.domains|length) > 0 and all(.domains[]; (.domain|type) == "string" and (.evidence|type) == "array")'
	expect "domains of unknown device" 404 "$base/api/devices/mac%3A00%3A00%3A00%3A00%3A00%3A01/domains"
fi
if [ -n "$metrics" ]; then
	expect "metrics" 200 "$base/metrics"
	grep -q '^phonehome_demo 1$' "$work/body" && ok "metrics labelled demo" || fail "metrics not labelled demo"
	grep -q '^phonehome_device_grade{' "$work/body" && ok "metrics device grades" || fail "metrics without device grades"
fi

if [ "$fails" -gt 0 ]; then
	echo "$fails check(s) failed"
	exit 1
fi
echo "all checks passed"
