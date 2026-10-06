# Prometheus, Grafana and Home Assistant

phonehome can hand its figures to the monitoring you already run. Nothing
on this page is pushed anywhere: Prometheus and Home Assistant fetch the
numbers from phonehome, with the same password as the dashboard. (To have
phonehome push instead, to an MQTT broker with Home Assistant discovery or
to a webhook, see [alerts.md](alerts.md).)

## Prometheus

Turn the endpoint on in your config file (it is off by default) and
restart phonehome:

```yaml
metrics: true
```

`/metrics` then serves the
[Prometheus text format](https://prometheus.io/docs/instrumenting/exposition_formats/).
It sits behind the same basic auth as the dashboard (`auth:` in the config),
so set a password if phonehome is reachable from more than your own LAN, and
behind the same host-name check: scrape it by IP address or a local name
(`pihole.lan`), or add the name you use to `allowed_hosts:`.

A scrape job:

```yaml
# prometheus.yml
scrape_configs:
  - job_name: phonehome
    scrape_interval: 1m          # under 5m: Prometheus looks back only 5m for a sample
    metrics_path: /metrics
    params:
      days: ["7"]                # 1, 7 (default) or 30: the period the figures cover
    basic_auth:                  # only if auth: is set in phonehome's config
      username: admin
      password_file: /etc/prometheus/phonehome-password
    static_configs:
      - targets: ["pihole.lan:8099"]
```

The figures describe a period, the last `days` days, exactly like the
dashboard, so they are gauges rather than counters: "snooping lookups per
day over the last 7 days" can go down. phonehome computes them at most once
a minute per period and serves the same page to scrapes in between.

### What is exported

Per device (labels `device`, `name`, `kind`):

| Metric | Meaning |
|---|---|
| `phonehome_device_grade` | The device's grade in grade points: A=4, B=3, C=2, D=1, F=0. See [grading.md](grading.md). |
| `phonehome_device_snooping_lookups_per_day` | Snooping lookups (content recognition, ads, tracking, telemetry) per day. |
| `phonehome_device_lookups{category=…}` | Lookups in the period, one series per category: `acr`, `ads`, `tracking`, `telemetry`, `essential`, `content`, `unknown`. |
| `phonehome_device_blocked_lookups` | Lookups your DNS filter blocked. |
| `phonehome_device_quiet_hours_lookups` | Lookups during the quiet-hours window. |
| `phonehome_device_heartbeats` | Destinations contacted on a regular clock. |
| `phonehome_device_bypass_findings{confidence=…}` | DNS-bypass findings, by confidence `high`, `medium`, `low`. See [detectors.md](detectors.md). |

For the whole home:

| Metric | Meaning |
|---|---|
| `phonehome_home_grade` | The home's grade in grade points (the worst device's). Absent when no device was seen. |
| `phonehome_home_devices` | Devices seen in the period. |
| `phonehome_home_lookups{category=…}` | Lookups in the period, per category. |
| `phonehome_home_snooping_lookups_per_day` | Snooping lookups per day. |

Per source (labels `source`, `kind`):

| Metric | Meaning |
|---|---|
| `phonehome_source_up` | 1 if the last ingestion run succeeded, 0 if it failed. |
| `phonehome_source_records` | Records ingested since phonehome's database was created. For a device source (leases), the number of devices it listed last time, so this is a gauge, not a counter. |
| `phonehome_source_last_run_timestamp_seconds` | When the source was last read. |
| `phonehome_source_last_success_timestamp_seconds` | When it was last read without error. Absent if never. |

And `phonehome_info{version}`, `phonehome_demo` (1 for the synthetic demo
household), `phonehome_window_days` and
`phonehome_report_generated_timestamp_seconds`.

**Cardinality.** Labels never contain domains, addresses or anything
unbounded. Each device adds 15 series, each source 4, and the rest add 14:
a home with 30 devices and 3 sources has 14 + 30 × 15 + 3 × 4 = 476
series. `name` is the device's display name, so renaming a device in the
dashboard starts new series for it; `device` (the MAC- or IP-based id)
stays the same, so join on that.

**What it reveals.** The device names you see in the dashboard, their
types and the numbers above. No domains, no IP addresses, no MAC beyond
the device id the dashboard uses.

Try it without Prometheus:

```sh
phonehome demo --metrics &
curl -s http://127.0.0.1:8099/metrics | grep device_grade
```

## Grafana

Add Prometheus as a data source and graph, for example:

- grade per device: `phonehome_device_grade` with the legend `{{name}}`,
  and value mappings 4→A, 3→B, 2→C, 1→D, 0→F;
- snooping per device: `phonehome_device_snooping_lookups_per_day`;
- what a device's traffic is for:
  `sum by (category) (phonehome_device_lookups{name="Living room TV"})`;
- a stale source: `time() - phonehome_source_last_success_timestamp_seconds > 3600`.

## Home Assistant

Home Assistant's own Prometheus integration only *exports* Home Assistant's
state; it cannot read other exporters. Use the built-in
[RESTful integration](https://www.home-assistant.io/integrations/rest/)
against phonehome's JSON API instead. It reads `/api/report` once and
creates as many sensors as you like from it. Add to `configuration.yaml`
(and restart Home Assistant):

```yaml
rest:
  - resource: http://pihole.lan:8099/api/report
    params:
      days: "1"                  # 1, 7 or 30
    authentication: basic        # leave out these three lines without auth:
    username: admin
    password: !secret phonehome_password
    scan_interval: 300
    sensor:
      - name: "phonehome home grade"
        unique_id: phonehome_home_grade
        value_template: "{{ value_json.grade }}"
      - name: "phonehome snooping lookups"
        unique_id: phonehome_snooping_lookups
        value_template: "{{ value_json.snooping }}"
        unit_of_measurement: "lookups"
        state_class: measurement
      - name: "Living room TV grade"
        unique_id: phonehome_living_room_tv_grade
        # The device's id as shown in the dashboard's details (or /api/report).
        value_template: >-
          {% set d = value_json.devices | selectattr('id', 'eq', 'mac:aa:bb:cc:dd:ee:ff') | list %}
          {{ d[0].grade if d else 'unknown' }}
      - name: "Living room TV snooping per day"
        unique_id: phonehome_living_room_tv_snooping
        value_template: >-
          {% set d = value_json.devices | selectattr('id', 'eq', 'mac:aa:bb:cc:dd:ee:ff') | list %}
          {{ d[0].perDay | round(0) if d else 0 }}
        unit_of_measurement: "lookups/day"
        state_class: measurement
```

The keys are those of Home Assistant's `rest` schema
(`homeassistant/components/rest/schema.py`): `resource`, `params`,
`authentication`, `username`, `password`, `scan_interval`, and per sensor
`name`, `unique_id`, `value_template`, `unit_of_measurement` and
`state_class`. In `/api/report`, `grade` is the home's grade, `snooping`
its snooping lookups in the period, and `devices` lists each device with
its `id`, `name`, `grade` and `perDay` (snooping lookups per day).

A state is at most 255 characters, so pick single values as above rather
than the whole report.

An automation that tells you when the TV gets worse:

```yaml
automation:
  - alias: "TV grade dropped"
    triggers:
      - trigger: state
        entity_id: sensor.living_room_tv_grade
    conditions:
      - condition: template
        value_template: >-
          {{ trigger.to_state.state in ['D', 'F'] and trigger.from_state.state in ['A', 'B', 'C'] }}
    actions:
      - action: notify.notify
        data:
          message: "Living room TV is now graded {{ trigger.to_state.state }}."
```
