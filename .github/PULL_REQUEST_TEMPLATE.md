## What does this change?

<!-- One or two sentences. Link the issue if there is one. -->

## Checklist

- [ ] `make lint test` passes
- [ ] Knowledge-base changes (`kb/`): every new or changed rule has `evidence:` URLs that
      actually support the claim, a `confidence:` that matches how strong that evidence is,
      and a plain-English `purpose:` that says *who, when and how often*, never more than
      we can know. `go run ./cmd/phonehome kb lint` passes.
- [ ] User-facing strings stay honest: encrypted traffic shows that a device talked to
      someone, not what it said.
- [ ] No new third-party modules and no network calls home.
