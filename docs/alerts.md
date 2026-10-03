# Alerts

phonehome can tell you when something changes, through services **you**
run or choose: a webhook, [ntfy](https://ntfy.sh), [Gotify](https://gotify.net)
or an MQTT broker. Alerts are off until you configure a target, and
phonehome contacts nothing but the targets you list.

## What triggers an alert

| Event | `events:` name | When |
|---|---|---|
| A new device appears | `new_device` | A device phonehome has never seen before shows up. |
| A grade gets worse | `grade_worse` | A device's grade drops, e.g. B → D. See [grading.md](grading.md). |
| A new snooping heartbeat | `heartbeat` | A device starts contacting a content-recognition, advertising, tracking or telemetry destination on a clock. See [detectors.md](detectors.md#heartbeats-it-calls-home-on-a-clock). |
| DNS bypass | `bypass` | A device gets a new DNS-bypass finding (it looked up an encrypted-DNS service, or connected to a public resolver). |

Every check (every 5 minutes by default) looks at the last 7 days, the
dashboard's default view, so an alert's grade is the grade the dashboard
shows. The very first check only records what is there, including every
device phonehome already knows but that was quiet that week: turning
alerts on does not announce every device you own.

**No repeats.** The same alert (same device and the same grade, domain or
finding) is not sent again within 24 hours, however often it flaps. A
device that drops out of the week and comes back is not "new".

**Rate limit.** All events found in one check go out together as one
notification. At most `max_per_hour` notifications (default 6) are sent per
hour; events held back by the limit are sent once the hour allows.

**Delivery** is at most once. If a target is down, that notification is
logged as failed and not retried, so a broken target cannot flood you
later. What phonehome remembers between checks lives in its own database,
so a restart neither repeats nor loses alerts.

## Configuration

```yaml
alerts:
  # events: [new_device, grade_worse, heartbeat, bypass]   # default: all
  # interval: 5m                                            # how often to check
  # max_per_hour: 6                                         # notifications per hour

  webhook:
    url: https://automation.lan/hooks/phonehome
    headers:                    # optional
      Authorization: "Bearer my-secret"

  ntfy:
    url: https://ntfy.sh/a-long-random-topic-name   # the topic's full address
    token_file: /etc/phonehome/ntfy-token           # or token:, or username: + password(_file):
    # priority: high                                # ntfy's 1–5 or min/low/default/high/urgent

  gotify:
    url: https://gotify.lan
    token_file: /etc/phonehome/gotify-app-token     # an application token
    # priority: 5

  mqtt:
    broker: mqtt://192.168.1.2:1883                 # or mqtts://host:8883 for TLS
    username: phonehome                             # optional
    password_file: /etc/phonehome/mqtt-password
    # client_id: phonehome
    # topic: phonehome                              # prefix for phonehome's topics
    # discovery: true                               # Home Assistant discovery
    # discovery_prefix: homeassistant
```

Set only the targets you want; each one gets every notification. A public
ntfy topic can be read by anyone who guesses its name: use a long random
name, or your own ntfy server with a token.

## Exactly what is sent

An alert never contains more than the dashboard shows you, and only about
the devices it is about: no lookups, no other devices. The only addresses
in it are the ones the dashboard shows too: the device id of a device
phonehome knows only by IP (`ip:192.168.1.9`), and, in a DNS-bypass
finding, the public resolver the device connected to (`8.8.8.8:443`).

**ntfy and Gotify** receive a title and one line per event:

```
Title: phonehome: 3 changes

New device: Living room TV (tv), grade F.
Kitchen plug started contacting metrics.example.com every 60s (Telemetry).
Kitchen plug: Looked up dns.google, an encrypted-DNS service it can use to skip your DNS filter.
```

ntfy gets them as an HTTP POST to the topic URL (body: the lines; headers:
`Title`, `Tags: phonehome`, and `Priority` if set). Gotify gets
`POST /message` with the `X-Gotify-Key` header and a JSON body
`{"title", "message"}`, plus `"priority"` if you set one (otherwise the
Gotify application's default applies).

**The webhook** and the MQTT topic `<topic>/alert` receive this JSON:

```json
{
  "source": "phonehome",
  "time": "2026-10-02T12:00:00Z",
  "demo": false,
  "title": "phonehome: 3 changes",
  "text": "New device: Living room TV (tv), grade F.\n…",
  "events": [
    {"type": "new_device", "device": {"id": "mac:aa:bb:cc:00:11:22", "name": "Living room TV", "kind": "tv", "vendor": "Samsung"}, "grade": "F"},
    {"type": "grade_worse", "device": {…}, "grade": "D", "previousGrade": "B"},
    {"type": "heartbeat", "device": {…}, "domain": "metrics.example.com", "category": "telemetry", "everySeconds": 60},
    {"type": "bypass", "device": {…}, "finding": {"kind": "doh-lookup", "detail": "Looked up dns.google, …", "evidence": "dns.google", "confidence": "medium"}}
  ]
}
```

| Field | Meaning |
|---|---|
| `device.id` | The device id the dashboard uses: `mac:<address>`, or `ip:<address>` for devices phonehome only knows by IP. |
| `device.name`, `kind`, `vendor` | As shown in the dashboard (your label if you set one). |
| `grade`, `previousGrade` | A–F, see [grading.md](grading.md). |
| `domain`, `category`, `everySeconds` | The heartbeat destination, what it is for, and how often it is contacted. |
| `finding` | The DNS-bypass finding: its kind, the dashboard's sentence, the domain or `address:port` that shows it, and its confidence. |
| `demo` | `true` only for the synthetic demo household. |

The webhook request is a POST with `Content-Type: application/json`, plus
any `headers:` you configured.

**MQTT** also keeps a grade sensor per device: each device's current grade
(A–F) is published, retained, to
`<topic>/device/<id>/grade`, where `<id>` is the device id with every
character other than letters, digits, `_` and `-` replaced by `_`
(`mac:aa:bb:…` becomes `mac_aa_bb_…`). With `discovery: true` (the default)
it also publishes, retained, a Home Assistant
[MQTT discovery](https://www.home-assistant.io/integrations/mqtt/#mqtt-discovery)
config to `<discovery_prefix>/sensor/phonehome/phonehome_<id>_grade/config`:

```json
{"name": "Privacy grade", "unique_id": "phonehome_mac_aa_bb_cc_00_11_22_grade",
 "state_topic": "phonehome/device/mac_aa_bb_cc_00_11_22/grade", "icon": "mdi:shield-search",
 "device": {"identifiers": ["phonehome_mac_aa_bb_cc_00_11_22"], "name": "Living room TV", "manufacturer": "Samsung"},
 "origin": {"name": "phonehome", "sw_version": "v0.3.0", "support_url": "https://github.com/bizpers11991-code/phonehome"}}
```

Home Assistant then shows one device per phonehome device, each with a
"Privacy grade" sensor. A check publishes only what changed since the last
one (plus everything once an hour, in case the broker lost its retained
messages), and connects to the broker only when there is something to send.
When a device drops out of the week, its retained grade is cleared, so the
sensor reads "unknown" rather than an old grade. Retained messages stay on your broker until you
clear them; to remove a device's sensor, publish an empty retained message
to its config topic.

phonehome speaks MQTT 3.1.1 itself (no library): it connects, publishes at
QoS 0 and disconnects, at most once per check for grades and once per
alert. It does not subscribe to anything.
