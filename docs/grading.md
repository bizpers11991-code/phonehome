# How phonehome grades a device

Every device gets a letter from **A** to **F** for the period you are looking
at. The grade answers one question: *how much does this device talk about you
rather than for you?* It is deliberately simple, so you can check it by hand
from the numbers on the receipt.

## The scale

The first row that matches wins.

| Grade | When |
|---|---|
| **F** | It reports what is on screen on a clock (a content-recognition *heartbeat*)\*, **or** it makes 5,000+ snooping lookups a day. |
| **D** | It contacts content-recognition (ACR) servers at all\*, **or** it makes 1,500+ snooping lookups a day, **or** it routes DNS around your filter (a high-confidence bypass). |
| **C** | 300+ snooping lookups a day. |
| **B** | 50+ snooping lookups a day. |
| **A** | Fewer than 50 snooping lookups a day. |

\* Only for TVs, streaming players and devices of unknown kind; see
[which devices the ACR rules apply to](#which-devices-the-acr-rules-apply-to).

## Why a device got its grade

Every grade names the rule that decided it, with the numbers behind it, so
you never have to work backwards from a letter. The report prints it as
`why:`, the dashboard as **Why D:** under the grade, the receipt as `WHY:`
under the stamp, and `/api/report` as each device's `reason`. There are four
rules, matching the table above:

| Rule (`reason.rule`) | Example |
|---|---|
| `volume` | 1,282 snooping lookups a day (C is 300–1,499) |
| `acr-heartbeat` | Content recognition (ACR) on a clock, every 15s: always F |
| `acr` | Contacts content-recognition (ACR) servers (120 lookups): at least D |
| `bypass` | Bypasses your DNS via 8.8.8.8:443: at least D (its 520 snooping lookups a day alone would be C) |

"At least D" is literal: a device with an ACR lookup or a bypass that also
makes 5,000+ snooping lookups a day is an F, and its reason is then the
volume. The rate in a reason is rounded but never across a band's edge, so
49.6 a day reads "49 (A is under 50)", not 50.

### Which devices the ACR rules apply to

The two content-recognition rules (F for an ACR heartbeat, D for any ACR
lookup) apply only to **TVs, streaming players and devices whose kind
phonehome could not tell**. ACR fingerprints what is on a screen, and only
those devices have one to fingerprint (or might). A laptop or phone that
looks up an ACR company's host, say because someone opened its website or an
app embeds its analytics, is not watching what you watch. For every other
kind those lookups still count as snooping, like advertising or telemetry,
so they still raise the device's lookups per day; they just do not trigger
the ACR penalty on their own.

The kind is shown on every device card. It comes from the hosts the device
looks up, its name and its vendor (`internal/analyze/kind.go`); if phonehome
has one wrong, please open an issue.

For a TV or streaming player that looked up no known ACR server, the report,
the dashboard's details, the receipt and the API (`acrUnseen`) add a note: "No known content-recognition server seen; not every TV
brand's servers are known." The knowledge base has ACR servers for Samsung,
Vizio, LG (Alphonso) and Samba TV (used by Sony, TCL, Philips and others),
but not seeing one is not proof a TV has no ACR.

### Provisional grades

A rate per day needs enough days behind it. A grade based on **less than 20
hours of data** for the device is marked **provisional**: "Provisional:
based on 3 hours of data". The data counted runs from the first lookup in
the period (or from when the device was first seen, if that is later) to the
end of the period. That covers a fresh install, and a device that joined an
established home an hour ago, whose few lookups would otherwise be spread
over the whole week and look quieter than they are. The threshold is 20
hours rather than 24 so that a 24-hour view whose first lookup came a few
minutes in is not flagged.

A provisional grade is still computed by the same rules and still shown,
and it still counts toward the home grade. Alerts ignore it: a provisional
grade never sends a "grade got worse" alert, a new device is announced
without one, and the MQTT grade sensor keeps its last real value (a new
device's sensor appears once it has one). Heartbeats and bypasses are
findings rather than rates, so they are alerted however little data there
is.

### Coverage: how much of a device phonehome recognises

Only lookups the knowledge base recognises can count against a device;
unknown destinations never do. So every device also shows the **share of
its lookups the knowledge base classified**: "recognised: 81% of lookups" in
the report, "81% recognised" beside Lookups in the dashboard's details,
`coverage` (0 to 1) and `coveragePercent` in `/api/report`. The percentage is
rounded down, so 49.6% reads 49%.

When fewer than half of a device's lookups are recognised, the grade says
so: "Graded on the 40% of lookups phonehome recognises". Such a grade errs
on the kind side: a rule for one of its unknown domains can only add
snooping, never take it away. The device's details list those unknown
domains, with a button to suggest rules for them.

## The home grade

Your whole home gets a letter too, shown on the home receipt, at the top of
`phonehome report` and as `grade` in `/api/report`. The rule is the simplest
honest one: **a home is as good as its worst device.** The home grade is the
worst grade among the devices seen in the period (F is worst, then D, C, B,
A). One loud TV is enough to make a home loud, however quiet the light bulbs
are, and averaging would hide exactly the device you most need to look at.

A period with no devices has no home grade (the receipt shows `?` and the
API returns an empty string). The rule lives in `HomeGrade` in
`internal/analyze/grade.go`.

## Compared with the previous period

When you fix something, the next report should show it. So each report is
also compared with the period of the same length just before it: the last 7
days with the 7 days before those, the last 24 hours with the day before. The
dashboard, the receipt ("SINCE LAST WEEK") and `phonehome report` show, for
the home and for each device:

- **snooping lookups per day**, before → now, and the change in percent;
- **the grade**, before → now;
- **heartbeats that stopped** (and new ones): snooping destinations contacted
  on a clock in the previous period but not in this one. Clocks for time sync
  or firmware checks are left out, since they say nothing about you;
- per category, the change in lookups per day;
- devices seen before but not now. phonehome cannot say why: unplugged,
  switched off, or a new address it cannot tie to the old device.

Both periods are graded by exactly the rules above, so "F → D" means the same
thing as two separate reports would.

**When no comparison is shown.** phonehome compares only against data it
actually has. There is no comparison when:

- stored data starts inside the current period (a fresh install, or
  retention shorter than twice the period: a 30-day view with 30 days of
  data has nothing before it);
- less than half of the previous period has data;
- the previous period has no lookups at all.

**Partial previous periods.** If data starts partway through the previous
period (but covers at least half of it), the comparison is shown and marked:
"only 4.2 of those 7 days have data". Its rates are per day of data, so a
short baseline is not mistaken for a quiet one.

**Percentages.** The change is (now − before) ÷ before. When a device had no
snooping before and has some now there is no meaningful percentage, so it
says "up from none"; a device not seen before is "new". Neither is ever shown
as a division by zero.

**What it does not prove.** A drop shows *that* a device asks less often, not
*why*. If you changed a setting, a drop right after is good evidence it
worked, but a TV that was simply switched off for a week would drop too:
check the hour-by-hour chart before you credit the fix. The comparison lives
in `internal/analyze/compare.go`.

## The terms

- **Snooping lookups** are DNS lookups of destinations the knowledge base
  classes as *content recognition*, *advertising*, *tracking* or *telemetry*.
  Essential traffic (updates, time sync) and the content you asked for
  (streams, apps) never count against a device, and neither do destinations
  the knowledge base does not know yet.
- **Per day** is snooping lookups divided by the length of the period in days,
  so a 7-day report and a 1-day report are comparable.
- **A lookup** is one device asking for one name, counted at most twice a
  minute. DNS logs record *queries*, and one lookup often makes several:
  iPhones, Macs and Chrome ask for the A, AAAA and HTTPS records of a name
  together, and Pi-hole answers blocked names with a 2-second TTL, so a
  device retrying a blocked tracker asks again every few seconds. Counting
  queries made Apple devices look up to three times worse than others doing
  the same thing, and made blocking a tracker worsen a device's grade. Now
  queries for the same name by the same device within 30 seconds of a
  counted lookup are part of it, whatever their record type and whether or
  not they were blocked; the lookup counts as blocked if any of them was.
  The 30 seconds run from the counted lookup, so a name asked for non-stop
  counts twice a minute, while a server contacted once a minute (Samsung's
  content recognition, for one) still counts every beat. Every figure built from lookups uses this count: totals,
  categories, per day, the hour-by-hour chart, quiet hours, the domain
  table, blocked counts, comparisons, receipts, `/metrics`, alerts and
  exports. Heartbeat cadences are measured from the queries themselves and
  are not affected.
- **Blocked lookups still count**, once each as above. A blocked lookup is an
  attempt: the device tried to reach that server. Your blocklist protects
  you; it does not make the device better behaved.
- **Content recognition** is singled out because it is the most intimate
  thing a TV can send: a fingerprint of whatever is on the screen, including
  your own videos and other devices plugged into it.
- **A high-confidence bypass** means we saw the device connect to a public DNS
  server directly (plain, over TLS on port 853, or over HTTPS to a known
  resolver address). This needs connection logs (conntrack). A device merely
  *looking up* an encrypted-DNS service is medium confidence and is reported
  but does not change the grade.

## What a grade is not

A grade measures *how often* a device reaches out to collect or report data.
It cannot measure *what* is sent: that traffic is encrypted. Lookups also
undercount real connections, because devices cache DNS answers. See
[detectors.md](detectors.md) for the limits of each finding.

The thresholds live in `internal/analyze/grade.go`. If you think they are
unfair, open an issue with the numbers from your own network.
