# Security policy

phonehome reads sensitive data: a record of every site the devices in a home
look up. We take reports about it seriously.

## Reporting a vulnerability

Please report privately through GitHub:
**[Report a vulnerability](https://github.com/bizpers11991-code/phonehome/security/advisories/new)**
(the *Security* tab › *Report a vulnerability*).

Do not open a public issue, pull request or discussion for a security
problem. Include what you found, how to reproduce it, the version
(`phonehome version`) and how you run it (Docker, systemd, other).

You can expect an acknowledgement within a week. We will keep you updated,
agree on a disclosure date with you, and credit you in the advisory unless
you prefer otherwise.

## Supported versions

Only the latest release gets security fixes.

## In scope

For example:

- reading or changing data through the dashboard or API without the
  configured `auth:`;
- phonehome writing to, or changing, a source it should only read
  (Pi-hole, AdGuard Home, dnsmasq, leases);
- any outbound connection other than to the sources and alert targets you
  configured;
- the receipt or a report revealing more than its view is meant to;
- the install script, release artifacts or container image not matching
  what the source and the published checksums say.

The dashboard has no authentication unless you set `auth:`, and it serves
plain HTTP. Exposing it to the internet without a reverse proxy that adds
TLS and authentication is a configuration choice, not a vulnerability.
