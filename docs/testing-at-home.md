# Testing phonehome at home

A short guide to a first real measurement of your own household, and to
sharing what you find without giving away more than you mean to.

## 1. Pick one source

Use the DNS server your devices already ask. If you run more than one, start
with the one that sees the most devices.

| You run | Read | Why |
|---|---|---|
| Pi-hole v6 on the same machine | its database, `pihole-FTL.db` | Complete, and keeps history ([Docker](setup/pihole-docker.md), [bare metal](setup/pihole-bare-metal.md)) |
| Pi-hole v6 on another machine | its API ([guide](setup/pihole-api.md)) | Nothing to mount |
| AdGuard Home | `querylog.json` ([guide](setup/adguard-home.md)) | |
| A router with dnsmasq (OpenWrt) | its query log ([guide](setup/openwrt.md)) | Holds only what was logged since the last rotation |

Devices are told apart by MAC address where the source knows it, and by IP
otherwise. If your router hands out addresses but Pi-hole or AdGuard Home
only sees the router, every device looks like one: point your devices at the
DNS server directly (usually a DHCP setting) before you start.

## 2. Let it collect for a week, better two

phonehome reads what the log already holds when it first starts. A Pi-hole
database keeps 91 days by default, so the dashboard may fill straight away;
a dnsmasq log usually starts empty.

- **After a day** you see which devices talk, and to whom.
- **After a week** the 7-day view is a full week. Heartbeats (a device
  calling the same place on a clock) and quiet-hours lookups only show up
  once there are enough nights to see them.
- **From about day 11** the 7-day view also compares with the week before
  ("↓ 25% vs the previous 7 days"). phonehome only compares once at least
  half of the earlier week has data, and marks the comparison partial until
  there are two full weeks.

Change one thing at a time (turn off a TV's viewing-information setting, say),
note the date, and look at the comparison a week later.

## 3. Read the unknown list

```
phonehome unknown                 # every device, last 7 days
phonehome unknown --device "Living room TV" --limit 0
```

It lists, per device, the domains the knowledge base cannot explain yet,
grouped by registrable domain, with how often and when they were looked up.
Local names and reverse lookups are left out. A device's details in the
dashboard show the same list under "Unclassified".

Not every unknown domain is interesting. One looked up thousands of times,
around the clock, or only at night is worth a closer look; one looked up
twice is usually not.

## 4. Suggest a rule

If you know, or can find out, what an unknown domain is for, open a
**New device / domain report** issue:

- In the dashboard, a device's "Unclassified" list has a **Suggest a rule**
  link. It opens the GitHub form prefilled with the device's make and kind
  and up to 20 of its unknown domain names. Parts of names that look like
  serial numbers or account IDs become `*`, and names that start with the
  device's hostname or name, or contain its MAC or IP address, are left out. Nothing else goes in: no addresses,
  MACs, hostnames or labels. The link is off for demo data.
- From the command line, `phonehome unknown` prints the form's address.

Issues are public. Fill in what you observed (when, how often, whether it
continues when the device is idle or a setting is off) and your evidence:
vendor documentation, a paper, or a description of your own capture. Say
what you know and what you guess. A rule needs a source that names the host
and what it is for; "it looks like telemetry" is a starting point, not
evidence.

## 5. Share a receipt without your MAC addresses

A receipt names each device the way the dashboard does: your label if you
gave one, otherwise its hostname, otherwise "*maker* device", otherwise its
IP address, and its MAC address only if phonehome knows nothing else about
it. Hostnames can be personal too ("annas-iphone").

Before you share:

1. Rename every device that will appear, in the dashboard (the pencil next
   to its name) or in your config's `labels:`. "Living room TV" is enough.
2. Make the receipt for one device, or the whole home:

   ```
   phonehome receipt -o tv.png --device "Living room TV"
   phonehome receipt -o home.png
   ```

   or use **Home receipt**, or **Receipt** on a device's card, in the dashboard.
3. Look at the image before posting it, for any name, address or MAC you
   did not mean to share. phonehome leaves local names and reverse lookups
   off receipts and masks parts of domain names that look like an ID, but
   check the domain names too: a vendor can put a serial number in a name
   in a form that isn't recognised.

A receipt is made on your machine; nothing is uploaded to make it.
