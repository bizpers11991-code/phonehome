# The phonehome knowledge base

This directory is what turns a hostname like `acr.tv-vendor.example` into
*"TV Vendor: identifies what is on your screen."* It is plain YAML, it ships inside the
binary, and anyone can improve it. Thank you for helping.

```
companies/<group>.yaml   who operates domains
domains/<group>.yaml     what each domain is for
fixes/<id>.yaml          what people can do about it
```

Files that don't end in `.yaml` or `.yml` are ignored.

## The one rule

**Say only what the evidence shows.** phonehome sees DNS lookups and, at
most, connection metadata. That tells us *who* a device talks to, *when* and
*how often*, never *what* it sent. Write every rule so it would survive a
skeptical reader with the source open next to it.

## Categories

Pick what the traffic is *for*, from the owner's point of view.

| category    | means                                                     | example |
|-------------|-----------------------------------------------------------|---------|
| `acr`       | Automatic content recognition: fingerprints what is on screen or playing. | a smart TV's ACR endpoint |
| `ads`       | Serving, targeting or measuring adverts.                  | a TV's home-screen ad server |
| `tracking`  | Building profiles across apps, devices or companies; data brokers. | a cross-device identity graph |
| `telemetry` | The vendor's own analytics, diagnostics and usage logging. | crash reports, "device health" uploads |
| `essential` | Needed for the device to work: firmware updates, time sync, connectivity checks, sign-in, licence checks. | `time.example.com`, update servers |
| `content`   | The thing the person actually asked for: streams, app APIs, CDNs, artwork. | a video CDN |

If a domain serves several purposes, choose the one that best describes
most of its traffic and say so in `purpose`. If you can't tell, don't guess:
leave the domain out. Unknown is an honest answer.

## Essential domains: don't break people's devices

Many readers will copy our lists straight into a blocklist. Anything a device
needs to update, keep time, sign in or stay online **must** be `essential`,
even if the same company also tracks people elsewhere. Say what breaks in the
purpose, e.g. *"Delivers firmware updates; blocking it leaves the TV
unpatched."* When a hostname is shared by an update service and telemetry,
mark it `essential` and explain; a missed blocking opportunity is far cheaper
than a bricked doorbell.

## Confidence

| level    | requires |
|----------|----------|
| `high`   | A peer-reviewed measurement study, or the vendor's own documentation, that names this domain. |
| `medium` | Several independent, reputable sources agree, or a well-maintained blocklist entry that explains *why* it is listed. |
| `low`    | A single community report (forum post, issue, one person's capture). |

When in doubt, go lower. Confidence can be raised later; trust can't be
rebuilt.

## Evidence

Every rule and every fix needs at least one `https://` link that a reviewer
can read. Good sources, best first:

1. Vendor documentation, privacy policies or support pages.
2. Peer-reviewed papers and academic measurement studies.
3. Reputable journalism that did its own testing.
4. Your own packet or DNS capture. Link the PR or issue where you describe
   the device, firmware version, what you did and what you saw.

A blocklist that merely contains the domain is not evidence of what it does.

## Writing a `purpose`

One calm, factual sentence, at most 160 characters, ending in a period. It
appears on receipts that people share, so it must be fair to the vendor and
useful to a non-technical reader.

- Good: *Sends fingerprints of on-screen video so the vendor can identify what you watch.*
- Good: *Checks for and downloads firmware updates.*
- Bad: *Spies on everything you do!!* (hyperbole, and says nothing specific)
- Bad: *Telemetry.* (says nothing the category doesn't)
- Bad: *Uploads your viewing history and voice recordings.* (unless your source says exactly that)

## Patterns

```yaml
pattern: example.com     # example.com and every subdomain, not badexample.com
pattern: =example.com    # exactly example.com, no subdomains
```

The most specific (longest) matching pattern wins, so you can classify
`example.com` as `content` and `ads.example.com` as `ads`. Patterns are
lowercase, have at least two labels, and have no wildcards or trailing dot.
Each pattern may appear only once across all files.

## Worked example

You've found that Acme's smart plugs report usage to `metrics.acme-iot.com`,
and Acme's own privacy policy says so.

1. Make sure the company exists in `companies/`, or add it:

   ```yaml
   companies:
     - id: acme              # lowercase slug, unique across all files
       name: Acme Devices Ltd
       country: GB           # ISO 3166-1 alpha-2 of the headquarters
       url: https://www.acme-iot.com
   ```

2. Add the rule to `domains/acme.yaml` (one file per company or group):

   ```yaml
   company: acme             # default for every rule in this file
   rules:
     - pattern: metrics.acme-iot.com
       category: telemetry
       purpose: Sends usage statistics and diagnostics from the plug to Acme.
       confidence: high      # the vendor's own document names the domain
       kind_hint: plug       # optional: this traffic suggests a smart plug
       evidence:
         - https://www.acme-iot.com/privacy#analytics
   ```

   A rule can set `company:` itself to override the file default.
   `kind_hint` is one of `tv`, `streamer`, `speaker`, `camera`, `vacuum`,
   `plug`, `hub`, `appliance`, `console`, `phone`, `computer`, `network`.

3. If there is a setting that turns it off, add `fixes/acme-plug-analytics.yaml`
   (the file name must match the `id`):

   ```yaml
   id: acme-plug-analytics
   title: Turn off usage analytics in the Acme app
   vendors: [acme]   # matched, case-insensitively, against device vendor, hostname, label and companies seen
   kinds: [plug]     # empty means any kind; vendors and kinds can't both be empty
   steps:
     - Acme app › Profile › Privacy
     - Share usage data → Off
   notes: Older firmware does not have this setting.
   evidence:
     - https://support.acme-iot.com/privacy-settings
   ```

4. Validate, then open a pull request that says how you know.

## Validating

```sh
phonehome kb lint              # every problem, with file and rule
go test ./internal/kb/...      # the embedded data must load and lint clean
```

Lint checks categories, confidence, kinds, evidence URLs, purpose length
and punctuation, pattern syntax, duplicate patterns, company IDs and fix IDs,
and references to companies that don't exist. CI runs the same checks.
