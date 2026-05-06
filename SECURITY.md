# Security policy

## Supported versions

`core` is pre-1.0. Security fixes are applied to the `main` branch only;
there are no long-term-support branches yet.

| Version | Supported |
|---------|-----------|
| main    | Yes       |

Once a stable release ships, this table will list every actively-
maintained release branch.

## Reporting a vulnerability

**Do not open a public issue.** Security vulnerabilities must be
reported privately.

### Preferred channel

Email **security@thesmos.sh** with:

1. A description of the vulnerability and its impact.
2. Steps to reproduce or a minimal proof of concept.
3. The affected package (`clock`, `rand`, `reporter`, …).
4. Your assessment of severity (Critical / High / Medium / Low).

Encrypt sensitive reports with the PGP key published at
<https://thesmos.sh/.well-known/security.txt>.

### What to expect

| Step | SLA |
|------|-----|
| Acknowledgement of your report | 2 business days |
| Initial triage and severity assessment | 5 business days |
| Fix developed and verified internally | Severity-dependent (see below) |
| Coordinated disclosure | Mutually agreed date |
| Public advisory published | Same day as fix release |

### Severity response targets

| Severity | Fix target | Disclosure window |
|----------|-----------|-------------------|
| Critical | 7 calendar days | 14 days after fix |
| High | 14 calendar days | 30 days after fix |
| Medium | 30 calendar days | 60 days after fix |
| Low | Next minor release | With release notes |

### Scope

In scope: every Go package in this module.

Out of scope: documentation typos, CI configuration, developer tooling.

### Safe harbour

We will not pursue legal action against researchers who:

- Act in good faith and follow this disclosure policy.
- Avoid accessing or modifying data belonging to others.
- Do not degrade service availability.
- Provide sufficient detail for us to reproduce and fix the issue.
