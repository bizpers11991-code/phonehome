# Contributing to phonehome

Thanks for helping. There are three common ways in:

- **Teach the knowledge base a device or domain.** No Go needed: it is YAML
  in [`kb/`](kb/). Read [kb/README.md](kb/README.md) first.
- **Report what you see.** A [device report](https://github.com/bizpers11991-code/phonehome/issues/new?template=new-device.yml)
  or a [bug report](https://github.com/bizpers11991-code/phonehome/issues/new?template=bug.yml).
- **Code, docs and setup guides.** Start with
  [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md). Improvements to
  [docs/setup/](docs/setup/README.md) from people running a setup we could
  not test are especially welcome.

Security problems go through [SECURITY.md](SECURITY.md), never a public issue.
Everyone taking part follows the [code of conduct](CODE_OF_CONDUCT.md).

## Development setup

You need Go (the version in [`go.mod`](go.mod)) and nothing else: no cgo, no
C compiler, no Node.

```sh
git clone https://github.com/bizpers11991-code/phonehome
cd phonehome
go run ./cmd/phonehome demo          # dashboard with a synthetic household on :8099
go run ./cmd/phonehome report --demo # the same as text
```

To try it on your own data, point a config file at a copy of your Pi-hole
database or AdGuard query log:

```sh
sudo cp /etc/pihole/pihole-FTL.db* /tmp/ && sudo chown "$USER" /tmp/pihole-FTL.db*
printf 'db: /tmp/ph.db\nsources:\n  - type: pihole-db\n    path: /tmp/pihole-FTL.db\n' > dev.yaml
go run ./cmd/phonehome serve --config dev.yaml
```

## Before you open a pull request

CI runs exactly these; run them locally first (`make lint test kb-lint`
does the same):

```sh
gofmt -l .                        # must print nothing
go vet ./...
go test -race ./...
go run ./cmd/phonehome kb lint    # knowledge-base rules
```

If you changed the receipt layout, `go test ./internal/receipt -update`
regenerates the sample images; check them in with the change.

## Ground rules

These keep phonehome trustworthy. Pull requests that break them will not be
merged, however good the rest is.

1. **Read-only.** phonehome never writes to Pi-hole, AdGuard Home, the
   router or any other source, and never scans or probes the network.
2. **Nothing leaves the house.** No telemetry, update checks, CDN fonts or
   scripts, or any outbound connection except to configured sources.
3. **No cgo, few dependencies.** It must cross-compile to a Raspberry Pi
   Zero with `CGO_ENABLED=0`. New third-party modules need a very good reason;
   ask in an issue first.
4. **Honest wording.** DNS shows *who, when and how often*, never *what*.
   User-facing text must not claim more. Demo data is always labelled.
5. **Tests next to the code**, with fixtures in `testdata/` and no network
   access.

## Knowledge-base contributions in brief

The full guide is [kb/README.md](kb/README.md). The essentials:

- **Say only what the evidence shows.** Every rule and fix needs at least one
  `https://` link a reviewer can read. A blocklist that merely lists a domain
  is not evidence of what it does.
- **Confidence** is `high` (peer-reviewed study or vendor documentation
  naming the domain), `medium` (several independent reputable sources) or
  `low` (a single community report). When in doubt, go lower.
- **Anything a device needs to work is `essential`**, even if the same
  company tracks people elsewhere. People copy these lists into blocklists;
  a missed blocking opportunity is cheaper than a bricked doorbell.
- **`purpose`** is one calm, factual sentence (at most 160 characters,
  ending in a period) that is fair to the vendor.
- Never invent domains. If you can't tell what a domain is for, leave it out.

## Pull requests

- Keep them focused: one device family, one fix, one feature.
- Describe what changed and why, and link the issue if there is one.
- The PR template's checklist is the review checklist.

phonehome is MIT-licensed; by contributing you agree your work is released
under the same [license](LICENSE).
