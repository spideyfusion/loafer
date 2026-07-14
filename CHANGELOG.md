# Changelog

All notable changes to loafer are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/), and the project uses
[SemVer](https://semver.org/). Release notes are generated from GitHub
releases; this file tracks the highlights.

## [Unreleased]

## [0.2.1] - 2026-07-14

### Fixed

- Leader-election messages from client-go (klog) now go through the zap
  logger, so all log output is uniform JSON.

- v0.2.0 dropped core-group `events` from RBAC, but controller-runtime's
  leader election still records its "became leader" event through the
  legacy core events API, producing a `forbidden` error at startup and a
  lost event. The leader-election Role now grants core `events`
  create/patch again (namespaced, `loafer-system` only). Re-run
  `kubectl apply -k deploy/` to pick it up; no restart needed.

## [0.2.0] - 2026-07-14

### Added

- **Config hot-reload**: the config file is polled every 10 seconds; valid
  changes apply immediately (including `logLevel`) and trigger a full
  resync, invalid changes are logged and ignored. Bind addresses, leader
  election, and widening the namespace watch scope still require a restart
  and log a notice when changed.
- **Admission warnings** (optional, Kubernetes ≥ 1.31):
  `deploy/admission-warnings.yaml` installs a CEL ValidatingAdmissionPolicy
  that warns — never rejects — at apply time when `loafer.dev/ips` is not a
  valid IP list.

### Changed

- Events are now written through the `events.k8s.io/v1` API. **RBAC
  change**: the controller needs `events` create/patch/update in the
  `events.k8s.io` group instead of the core group; `kubectl apply -k
  deploy/` updates the shipped ClusterRole.

## [0.1.0] - 2026-07-14

### Added

- Initial implementation: publish annotated IPs (`loafer.dev/ips`) and an
  optional hostname (`loafer.dev/hostname`) into the status of eligible
  `LoadBalancer` Services via server-side apply.
- Config file with class, annotation prefix, allowed CIDRs, namespace
  selector, leader election, metrics/health addresses, log level.
- `loafer_ip_assignments_total` metric, `IPAssigned` / `IPReleased` /
  `InvalidAnnotation` events.
- Kustomize deploy base, kind e2e, goreleaser release pipeline.
