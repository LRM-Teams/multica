# Tech research playbook

Use after `general.md` for architecture, product capability, implementation,
operations, security, or technology-selection questions.

- Pin version, platform, workload, configuration, date, and operating boundary.
  Official docs, RFCs, standards, canonical repositories, release notes, issue
  trackers, benchmarks, incidents, and direct experiments establish different
  kinds of Claims.
- Vendor documentation can establish supported behavior; comparative
  performance, reliability, security impact, and operational cost require
  reproducible measurements or independent operational evidence.
- Preserve commands, configuration, data definitions, and failure conditions
  needed to interpret a benchmark or experiment. Dead-end probes remain useful
  negative evidence.

## Counterevidence adaptation

- Test version skew, unsupported environments, failure recovery, security
  boundaries, resource ceilings, migration cost, and cases where the preferred
  option loses under a realistic workload.
- Separate documented capability, observed behavior, inference, and
  recommendation in the Claim ledger and report.
