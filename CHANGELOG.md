# Changelog

All notable changes to loafer are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/), and the project uses
[SemVer](https://semver.org/). Release notes are generated from GitHub
releases; this file tracks the highlights.

## [Unreleased]

### Added

- Initial implementation: publish annotated IPs (`loafer.dev/ips`) and an
  optional hostname (`loafer.dev/hostname`) into the status of eligible
  `LoadBalancer` Services via server-side apply.
- Config file with class, annotation prefix, allowed CIDRs, namespace
  selector, leader election, metrics/health addresses, log level.
- `loafer_ip_assignments_total` metric, `IPAssigned` / `IPReleased` /
  `InvalidAnnotation` events.
- Kustomize deploy base, kind e2e, goreleaser release pipeline.
