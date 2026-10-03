# Performance

Numbers from `TestScale` in `internal/ingest/scale_test.go`, which is skipped
unless asked for:

```sh
PHONEHOME_SCALE=1 go test -run TestScale -v -timeout 30m ./internal/ingest
GOARCH=386 PHONEHOME_SCALE=1 go test -run TestScale -v ./internal/ingest   # 32-bit
```

## The workload

A Pi-hole v6 `pihole-FTL.db` (FTL's `query_storage` + `*_by_id` tables and
`queries` view, in WAL mode) holding **1,510,292 lookups over 30 days from 25
clients** (one of them IPv6-only), about 50,000 a day. The traffic is the
`demo` household's devices, with their heartbeats and daily rhythms, cloned
onto 25 addresses; a quarter of the lookups go to a long tail of 20,000
domains the knowledge base does not know. phonehome reads it through the real
`pihole.DB` source and `ingest.Runner`, into a file store, then builds
reports the way `phonehome serve` does (`Devices` + `DNSBetween` +
`FlowsBetween` + `analyze.Analyze` with the embedded KB).

## The machine

An AMD Ryzen 7 8845HS laptop (16 threads, NVMe SSD, 25 GiB RAM), Linux 6.19,
Go 1.27. **Not a Raspberry Pi.** A Pi Zero (one 1 GHz ARMv6 core, SD card) is
easily 20–40× slower; treat the timings below as relative. The 32-bit rows
are the same x86 machine running a `GOARCH=386` build, which shows the memory
a 32-bit Pi would need (pointers and strings are half the size) but not its
speed. No ARM hardware or emulator was available; the `linux/arm` (GOARM=6
and 7) builds were only compiled and vetted.

"Heap peak" is the highest `HeapAlloc` sampled every 5 ms. It includes
garbage the collector has not reclaimed yet, so it moves with `GOGC` and
`GOMEMLIMIT`; "keeps" is what is still live after a GC, i.e. the lookups the
report holds.

## Results (after this change)

| | amd64 | 386 (32-bit) |
|---|---|---|
| First ingest, 1.51M lookups | 4.7 s (320k lookups/s), heap peak 7 MiB | 6.3 s (241k/s), heap peak 6 MiB |
| Incremental ingest, one hour (1,408 lookups) | 7 ms | 9 ms |
| Store on disk (after checkpoint) | 50.5 MiB, 35 B/lookup | same file |
| Report, 1 day (48.6k lookups) | 39 ms read + 16 ms analyze; peak 15 MiB | 64 + 22 ms; peak 11 MiB |
| Report, 7 days (352k lookups) | 253 + 82 ms; peak 76 MiB | 432 + 135 ms; peak 49 MiB |
| Report, 30 days (1.51M lookups) | 1.09 s + 0.40 s; keeps 150 MiB, peak 299 MiB | 1.72 s + 0.49 s; keeps 99 MiB, peak 195 MiB |
| Prune 1 week (353k lookups) | 0.63 s | 0.76 s |

With `GOMEMLIMIT=128MiB` the 32-bit 30-day report peaks at 128 MiB (analysis
takes 0.9 s instead of 0.5 s, as the GC works harder).

Ingest memory stays flat however large the backlog: sources are read in
batches of 5,000 and a run stops after 20 batches.

## Before and after

Same workload and machine, before and after the store changes from the
robustness work (`DNSBetween` no longer joins every row to `domains` and `sources`
and allocates its result once; `InsertDNS` sends 64 rows per statement):

| | before | after |
|---|---|---|
| 30-day read (amd64) | 2.01 s, allocates 1121 MiB, heap peak 454 MiB | 1.09 s, 385 MiB, 299 MiB |
| 30-day read (386) | 2.70 s, allocates 752 MiB, heap peak 234 MiB | 1.72 s, 275 MiB, 195 MiB |
| 7-day read (amd64) | 480 ms, allocates 284 MiB | 253 ms, 92 MiB |
| First ingest of 1.51M (amd64) | 6.1 s | 4.7 s |
| `BenchmarkInsertDNS` | 2399 ns/row | 1830 ns/row |
| `BenchmarkDNSBetween7Days` | 263 ms | 170 ms |

## What this means for a Pi Zero

- Ingest is cheap: a few MiB of heap, and the steady state (one minute of a
  busy home is ~35 lookups) is negligible even at 40× slower. Catching up on
  a month of history the first time takes minutes, not hours.
- The store needs about 35 bytes per lookup: ~50 MB a month for this
  household, ~150 MB at the default 90-day retention.
- The 30-day report is the memory peak. It holds every lookup of the period
  in memory (~99 MiB live on 32-bit for 1.5M lookups) and peaks near 200 MiB
  with default GC settings. That fits in 512 MB next to Pi-hole, but not with
  much to spare, so the systemd unit and the Docker image set
  `GOMEMLIMIT=128MiB`, which keeps the peak near that limit at the cost of
  slower reports. The 1- and 7-day reports stay under 50 MiB.
- `GOMEMLIMIT` is a soft limit and only an environment variable, so it is
  easy to change: `systemctl edit phonehome` → `Environment=GOMEMLIMIT=512MiB`,
  or `-e GOMEMLIMIT=512MiB` / `environment:` in Docker; `off` removes it. On a
  64-bit machine a household this busy keeps ~150 MiB live during a 30-day
  report, above the limit, so the collector runs more often (Go caps it at
  about half the CPU); raise the limit there if reports feel slow.
- Most of the remaining report memory is the `[]model.DNSQuery` itself
  (~68 bytes per lookup on 32-bit, ~104 on 64-bit). Shrinking it further
  would mean streaming lookups into `analyze` instead of materialising them,
  which is a change to the `analyze` / `model` contract and was left out.
