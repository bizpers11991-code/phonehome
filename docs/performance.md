# Performance

Numbers from `TestScale` in `internal/ingest/scale_test.go`, which is skipped
unless asked for:

```sh
PHONEHOME_SCALE=1 go test -run TestScale -v -timeout 30m ./internal/ingest
GOARCH=386 PHONEHOME_SCALE=1 go test -run TestScale -v ./internal/ingest   # 32-bit
PHONEHOME_SCALE=1 go test -run TestScaleManyDomains -v ./internal/ingest   # 200k distinct domains
```

## The workload

On this page a "lookup" is one logged DNS query, a row in the store: what
ingest, storage and the report's memory scale with. Reports then count the
queries one device makes for one name within 30 seconds as a single lookup
([grading.md](grading.md#the-terms)), so their totals are lower.

A Pi-hole v6 `pihole-FTL.db` (FTL's `query_storage` + `*_by_id` tables and
`queries` view, in WAL mode) holding **1,455,434 lookups over 30 days from 25
clients** (one of them IPv6-only), about 48,500 a day. The traffic is the
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
`GOMEMLIMIT`; "keeps" is what is still live after a GC, i.e. the finished
report. "Max RSS" is what the OS saw for the whole `phonehome report` process.

## Results

Measured on main for v0.3.0, with the 30-second lookup counting and the
report-memory changes described [below](#reports-with-many-distinct-domains).

| | amd64 | 386 (32-bit) |
|---|---|---|
| First ingest, 1.46M lookups | 4.9 s (297k lookups/s), heap peak 8 MiB | 6.5 s (222k/s), heap peak 6 MiB |
| Incremental ingest, one hour (1,227 lookups) | 10 ms | 14 ms |
| Store on disk (after checkpoint) | 48.7 MiB, 35 B/lookup | same file |
| Report, 1 day (38k lookups) | 52 ms; peak 9 MiB | 86 ms; peak 7 MiB |
| Report, 7 days (276k lookups) | 379 ms; peak 26 MiB | 543 ms; peak 20 MiB |
| Report, 30 days (1.31M lookups) | 1.51 s; peak 72 MiB | 2.38 s; peak 60 MiB |
| Prune 1 week (353k lookups) | 0.70 s | 0.79 s |

The report rows are the whole report (read and analysis in one pass, since
lookups are streamed from the store into the analysis) with the lists a
dashboard does not show left out. They used to be 1.10 s + 0.70 s with a
292 MiB peak (amd64) and 1.74 s + 0.84 s with 188 MiB (386) for 30 days, as
the report held every lookup of the period in memory.

A report holds **one read snapshot** of the store for as long as it runs
(`Store.DNSEach` is a single read transaction, 4 s for 30 days of the
harder fixture below). Ingest and pruning keep writing meanwhile, since WAL
readers do not block the writer, but the WAL cannot be reset past the
snapshot, so it grows until the report finishes. After a large prune the
file is therefore shrunk with a passive checkpoint, which never waits; if a
report was open, the last pages go at a later checkpoint.

Ingest memory stays flat however large the backlog: sources are read in
batches of 5,000 and a round stops after 20 batches per source, with the
next round starting at once while a source has more (`ingest --once` keeps
going until it has everything).

## Reports with many distinct domains

The household above has 20,000 distinct names it does not know. Launch QA
tried a harder one, a Pi-hole v5 database of **3,000,000 lookups over 30
days from 14 clients to about 208,000 distinct domains** (CDN hash hosts,
generated tracker names), where a device looks up tens of thousands of names
of its own. A report's memory does not grow with lookups so much as with
**(device, domain) pairs**: each needs a counter, its burst times for the
heartbeat test, a row in the device's domain list and its classification.
That fixture has 624,000 pairs in 30 days.

`phonehome report --days N` on the same Ryzen laptop (`tm.py`: wall time
and max RSS of the whole process; heap peak sampled inside the process):

| | before | after |
|---|---|---|
| 1 day (98k lookups) | 0.55 s, RSS 153 MiB, heap peak 124 MiB | 0.46 s, RSS 79 MiB, heap peak 56 MiB |
| 7 days (698k) | 3.3 s, RSS 708 MiB, heap peak 638 MiB | 2.3 s, RSS 225 MiB, heap peak 173 MiB |
| 30 days (3.0M) | 4.3 s, RSS 803 MiB, heap peak 747 MiB | 3.8 s, RSS 234 MiB, heap peak 189 MiB |
| 1 day, `GOMEMLIMIT=128MiB` | 0.56 s, RSS 138 MiB, heap peak 107 MiB | 0.45 s, RSS 82 MiB, heap peak 53 MiB |
| 7 days, `GOMEMLIMIT=128MiB` | 10.7 s (115 s of CPU), RSS 552 MiB, heap peak 517 MiB | 3.3 s (15 s of CPU), RSS 174 MiB, heap peak 137 MiB |
| 30 days, `GOMEMLIMIT=128MiB` | 13.7 s (153 s of CPU), RSS 477 MiB, heap peak 440 MiB | 4.7 s (16 s of CPU), RSS 162 MiB, heap peak 115 MiB |

(The 7-day report reads 14 days, to compare with the week before.) Before,
the live data alone was above the 128 MiB limit, so the collector ran
continuously: 10–15 s of wall time and a minute-plus of CPU on a 16-thread
machine, which on a 4-core Pi is the wall time. After, the live data is
about 100 MiB at its largest (just before the report is built), so the limit
holds with a collector that is busy but not thrashing, and RSS stays near
160 MiB: Go's heap limit is soft and does not count SQLite's own memory.
Nothing about the output changed: the dashboard's API answers for 1, 7 and
30 days and every device's domain list were compared byte for byte, before
and after, on this fixture and on the demo household.

`TestScaleManyDomains` makes a smaller version of this without a Pi-hole
database (3M lookups, 14 clients, 200k domains, about 600k pairs, stored
directly) and fails if the 30-day heap peak passes 256 MiB:

| | time | heap peak |
|---|---|---|
| 1 day | 0.26 s | 35 MiB |
| 7 days | 1.4 s | 108 MiB |
| 30 days | 4.6 s | 187 MiB |

What changed, from the profile of the 30-day report (1.37 GB allocated, 673
MiB peak, 187 MiB kept):

- **Lookups are streamed.** `Store.DNSEach` decodes one row at a time into
  the analysis (`analyze.AnalyzeStream`) instead of building a 3M-element
  `[]model.DNSQuery` (104 bytes each, 310 MiB). Its scan variables are also
  declared once; per row they were three heap allocations, a fifth of the
  garbage.
- **A pair costs 64 bytes of counters, not 80 plus a slice.** The accumulator
  no longer points at its domain (the map key does), keeps a name's first
  burst time in a field and later ones in a slice only if there are any, and
  is allocated in chunks per device, so a device's are garbage as soon as its
  report is built.
- **Classifications are shared.** All the unknown domains, or all those under
  one rule, point at one `model.DomainMatch` instead of each carrying a
  152-byte copy of the rule's text, company and evidence; a `DomainStat` is
  now a name, two counts, two times and that pointer.
- **Only the top is kept.** `Unknown` (top 50) and `TopDomains` (top 25) were
  built from a full sort of every domain, and the 50 kept pinned the backing
  array of all of them (52 MiB in the 7-day report); they now trim as they go, and
  a name's registrable-domain grouping is worked out only for the survivors.
- **The previous period keeps no domain lists.** The comparison period is only
  compared with; its devices (`Gone`) used to carry full lists nobody shows.
- **The full domain lists are capped.** `DeviceReport.Domains` is every
  domain a device looked up, shown in the details view's table. A cached
  report keeps these lists for up to 250,000 entries in all (about 90 bytes
  each; a household with 20,000 names has them all several times over). Past
  that a device's list is left out of the report (`DomainCount` says how long
  it is) and opening its details runs one analysis for that device's list
  (`ReportWithDomains`): about the cost of a report (4 s here for 30 days),
  once, instead of keeping hundreds of MB for every cached period. Callers
  asking at the same time share that analysis. The result is cached for 30
  seconds (two at most) only while the list is under the same 250,000
  entries; a longer one is handed to those waiting and then dropped, so
  the cache stays bounded and asking again re-runs the analysis.

`go test -bench Analyze ./internal/analyze` on the laptop, before and after
(the first two are the machine's usual household; `ManyDomains` is eight
devices that each look up 20,000 names, 480k lookups in a week):

| | before | after |
|---|---|---|
| `BenchmarkAnalyze` (1M lookups, one week) | 221 ms, 74 MB, 487k allocs | 188 ms, 62 MB, 328k allocs |
| `BenchmarkAnalyzeCompared` (2M, two weeks) | 439 ms, 146 MB, 949k allocs | 364 ms, 113 MB, 631k allocs |
| `BenchmarkAnalyzeManyDomains` (160k pairs) | 223 ms, 70 MB, 761k allocs | 158 ms, 30 MB, 442k allocs |

`TestManyDomainsMemory` (in `go test`, about half a second, skipped with
`-short`) guards the shape: 120,000 pairs must peak under 450 bytes of heap
each; it was 737 before and is about 215 now.

**What is left.** Memory still grows linearly with pairs, at roughly 160
bytes live (300 with the collector's headroom) per pair in the busiest
period: the counter, its map slot and burst times, the domain's cached
classification, and the name. A household where every device looks up
millions of distinct names would still need hundreds of MB for a 30-day
report, in proportion; there is no cap that would not change what a report
says, so there is none. The details view's table of a device with hundreds
of thousands of domains is a large response (about 10 MB for 52,000 here).

## Before and after the v0.2 store changes

Measured for v0.2 on that release's version of the workload (1.51M lookups),
before and after the store changes from the robustness work (`DNSBetween` no longer joins every row to `domains` and `sources`
and allocates its result once; `InsertDNS` sends 64 rows per statement):

| | before | after |
|---|---|---|
| 30-day read (amd64) | 2.01 s, allocates 1121 MiB, heap peak 454 MiB | 1.09 s, 385 MiB, 299 MiB |
| 30-day read (386) | 2.70 s, allocates 752 MiB, heap peak 234 MiB | 1.72 s, 275 MiB, 195 MiB |
| 7-day read (amd64) | 480 ms, allocates 284 MiB | 253 ms, 92 MiB |
| First ingest of 1.51M (amd64) | 6.1 s | 4.7 s |
| `BenchmarkInsertDNS` | 2399 ns/row | 1830 ns/row |
| `BenchmarkDNSBetween7Days` | 263 ms | 170 ms |

## Comparing with the previous period

Since reports compare each period with the one before it (see
[grading.md](grading.md)), a report reads and analyses **twice the period**
whenever stored data reaches back that far: one `DNSBetween` over both
periods, one pass that classifies each domain once and feeds each lookup to
its period. The timings above are for one period; with the comparison a 7-day
report costs roughly what a 14-day report did before, and a 30-day report
with 60 days stored roughly doubles its time and memory (still within reach
of 512 MB on 32-bit, per the figures above). On the same laptop:

| | ns/op | B/op |
|---|---|---|
| `BenchmarkAnalyze` (1M lookups, one week) | 238 ms | 74 MB |
| `BenchmarkAnalyzeCompared` (2M lookups, two weeks) | 473 ms | 146 MB |

The previous period skips fix lookups, so the overhead is the data itself,
not extra work per lookup.

## What this means for a Pi Zero

- Ingest is cheap: a few MiB of heap, and the steady state (one minute of a
  busy home is ~35 lookups) is negligible even at 40× slower. Catching up on
  a month of history the first time takes minutes, not hours.
- The store needs about 35 bytes per lookup: ~50 MB a month for this
  household, ~150 MB at the default 90-day retention.
- The 30-day report is the memory peak, and it is proportional to the
  distinct (device, domain) pairs, not the lookups, since lookups are
  streamed: 60 MiB on 32-bit for the 1.46M-lookup household above, and 189 MiB
  (115 MiB with the limit) for the 208,000-domain case. That fits in 512 MB
  next to Pi-hole with room to spare. The systemd unit and the Docker image
  set `GOMEMLIMIT=128MiB`, which holds the peak near that limit at the cost of
  slower reports only when a report is that large. The 1- and 7-day reports
  stay under 50 MiB in the household above.
- `GOMEMLIMIT` is a soft limit and only an environment variable, so it is
  easy to change: `systemctl edit phonehome` → `Environment=GOMEMLIMIT=512MiB`,
  or `-e GOMEMLIMIT=512MiB` / `environment:` in Docker; `off` removes it. On a
  64-bit machine the 208,000-domain case keeps ~100 MiB live while a 30-day
  report is built, close to the limit, so the collector runs often (Go caps
  it at about half the CPU); raise the limit there if reports feel slow.
