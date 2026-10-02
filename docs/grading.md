# How phonehome grades a device

Every device gets a letter from **A** to **F** for the period you are looking
at. The grade answers one question: *how much does this device talk about you
rather than for you?* It is deliberately simple, so you can check it by hand
from the numbers on the receipt.

## The scale

The first row that matches wins.

| Grade | When |
|---|---|
| **F** | It reports what is on screen on a clock (a content-recognition *heartbeat*), **or** it makes 5,000+ snooping lookups a day. |
| **D** | It contacts content-recognition (ACR) servers at all, **or** it makes 1,500+ snooping lookups a day, **or** it routes DNS around your filter (a high-confidence bypass). |
| **C** | 300+ snooping lookups a day. |
| **B** | 50+ snooping lookups a day. |
| **A** | Fewer than 50 snooping lookups a day. |

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

## The terms

- **Snooping lookups** are DNS lookups of destinations the knowledge base
  classes as *content recognition*, *advertising*, *tracking* or *telemetry*.
  Essential traffic (updates, time sync) and the content you asked for
  (streams, apps) never count against a device, and neither do destinations
  the knowledge base does not know yet.
- **Per day** is snooping lookups divided by the length of the period in days,
  so a 7-day report and a 1-day report are comparable.
- **Blocked lookups still count.** A blocked lookup is an attempt: the device
  tried to reach that server. Your blocklist protects you; it does not make
  the device better behaved.
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
