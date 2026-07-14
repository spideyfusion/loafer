# loafer — Project Specification & Handover

**A LoadBalancer that doesn't lift a finger.**

A tiny Kubernetes controller that assigns a user-chosen IP address to
`Service` objects of type `LoadBalancer`, based on an annotation. Intended
for clusters where the actual load balancer lives *outside* the cluster (an
on-prem appliance, a router, a manually managed VIP), so no cloud controller
ever populates the Service status and it stays `<pending>` forever.

This document is the single source of truth: the behavioral contract
(sections 1–6), plus the as-built state and handover notes (sections 7–9).
**v1 is fully implemented and verified** — any agent or developer can take
over from here. Where the spec is silent, prefer the simplest solution that
could possibly work (KISS).

> Naming history: the project started life as `static-lb` and was renamed to
> **loafer** before the first commit. If you find `static-lb` anywhere except
> this sentence, it's a leftover — fix it.

---

## 1. Problem statement

When a `Service` of type `LoadBalancer` is created in a cluster with no
cloud provider integration, `.status.loadBalancer.ingress` is never set.
Users who front their cluster with an externally managed load balancer know
exactly which IP the service is reachable on, but Kubernetes has no way to
reflect that. Tools that consume the status field (external-dns, ingress
controllers, `kubectl get svc`, GitOps health checks) all break or show
`<pending>`.

loafer closes that gap: the user annotates the Service with the IP they've
assigned externally, and the controller writes it into the Service status.

## 2. Goals & non-goals

Goals:

1. Watch Services and, for eligible ones, patch
   `.status.loadBalancer.ingress` with the IP(s) declared in an annotation.
2. Remove the IP from status when the annotation is removed or the service
   becomes ineligible.
3. Runtime configuration via a single YAML file.
4. Never fight another load-balancer implementation.
5. Small enough to read in one sitting; trivially easy to contribute to.

Non-goals (also documented in the README's "What this is not"):

- **No IPAM** — the user chooses the IP; want allocation? Use MetalLB.
- **No data plane** — no routes, ARP, BGP, firewalls.
- **No CRDs** — config file + annotations only.
- **No webhooks.** Since v0.2.0 an *optional* CEL
  `ValidatingAdmissionPolicy` (`deploy/admission-warnings.yaml`, K8s ≥ 1.31)
  surfaces invalid-annotation warnings at apply time — warning only, never a
  rejection, and no webhook/TLS machinery.
- **No multi-cluster awareness.**

## 3. Behavioral contract (as implemented)

### 3.1 Identity

| Thing | Value |
|---|---|
| Module path | `github.com/spideyfusion/loafer` |
| GitHub repo | `spideyfusion/loafer` |
| Container image | `ghcr.io/spideyfusion/loafer` |
| Default `loadBalancerClass` | `loafer.dev/static` |
| Default annotation prefix | `loafer.dev` |
| SSA field manager | `loafer` (const `FieldManager` in `internal/controller`) |
| Namespace (deploy base) | `loafer-system` |
| Custom metric | `loafer_ip_assignments_total{result="assigned\|released\|invalid"}` |

### 3.2 Eligibility

A Service is reconciled iff **all** hold (implemented in
`ServiceReconciler.eligible`, unit-tested):

1. `spec.type == LoadBalancer`.
2. `spec.loadBalancerClass` equals the configured class. Nil-class services
   are ignored unless `claimServicesWithoutClass: true` (default `false`;
   risky, documented).
3. Namespace matches the configured selector (default: all). Namespaces are
   also filtered at the cache level in `cmd/loafer/main.go`.
4. Not being deleted. **No finalizer by design**: status dies with the
   object, so there is nothing to clean up on delete.

### 3.3 Annotations

| Annotation | Meaning |
|---|---|
| `loafer.dev/ips` | Comma-separated IPs to publish (IPv4 and/or IPv6). |
| `loafer.dev/hostname` (optional) | Also publish a `hostname` ingress entry, appended after the IP entries. **Implementation decision:** only takes effect alongside a valid, non-empty `ips` annotation. |

Rules (pure logic in `internal/ipparse.Parse`, ~100% covered):

- IPs must parse with `net/netip.ParseAddr`; one invalid entry invalidates
  the whole annotation. IPv4-mapped IPv6 is normalized (`Unmap`) so
  `::ffff:203.0.113.10` equals `203.0.113.10`.
- If `allowedCIDRs` is non-empty, every IP must fall in at least one CIDR.
- Duplicates removed, first-occurrence order preserved.
- Valid annotation → SSA the exact declared set (idempotent: no patch, no
  event when equal), Event `Normal/IPAssigned`, metric `assigned`.
- Invalid annotation → existing status untouched, Event
  `Warning/InvalidAnnotation` with the reason, `error` log, metric
  `invalid`. Terminal until the object changes: Reconcile returns nil (no
  hot retry loop).
- Annotation removed/emptied while eligible → clear our entries, Event
  `Normal/IPReleased`, metric `released`.

### 3.4 Status ownership (the subtle part)

- All status writes are **server-side apply** with field manager `loafer`,
  no force: conflicts surface as errors and requeue with default backoff.
- Only `.status.loadBalancer.ingress` is ever written. Never spec.
- **Release semantics:** clearing applies a status with an empty
  `loadBalancer` (no `ingress` field), which makes SSA remove our entries
  *and drop our field ownership*. After that the Service is never touched
  again until it becomes eligible with a valid annotation.
- **Ineligible cleanup** (type changed away, recreated under another class):
  clear only if `managedFields` shows manager `loafer` owning
  `status.loadBalancer.ingress` on the `status` subresource
  (`ownsIngress`/`fieldsContainIngress` in `internal/controller/ingress.go`).
  This guarantees we never clear entries some other implementation wrote.
  Note the raw fieldsV1 shape is `{"f:status":{"f:loadBalancer":{"f:ingress":{}}}}`
  — the top-level `f:status` wrapper is easy to miss.
- **Idempotency comparison** (`ingressEqual`) checks only IP + Hostname, in
  order. `ipMode` is deliberately ignored — the API server defaults it, and
  comparing it would cause a patch loop.
- Two services declaring the same IP → allowed (user's call). Detected
  cheaply via a cache field index (`ipIndexKey`) and logged at `info`.
- Events use the `events.k8s.io/v1` API (`mgr.GetEventRecorder`, since
  v0.2.0) with action strings `AssignIP` / `ReleaseIP` /
  `ProcessAnnotation`. RBAC needs the `events.k8s.io` group, not core
  events. In tests, list `eventsv1.EventList` and read `.Regarding` /
  `.Series` (not `.InvolvedObject` / `.Count`).

### 3.5 Config file

Read from `--config` (default `/etc/loafer/config.yaml`) and
**hot-reloaded** since v0.2.0: `internal/config.Store` polls the file every
10s (polling, not inotify — ConfigMap symlink swaps), swaps atomically on a
valid change, and triggers a full Service resync through a
`source.Channel` in `SetupWithManager`. An invalid change is logged once
and ignored (previous config stays active); `logLevel` switches live via a
zap `AtomicLevel`. Bind addresses, leader election, and *widening* the
namespace watch scope are fixed at manager startup — `warnNonReloadable` in
`main.go` logs a restart notice for those. Full reference in
`docs/configuration.md`. Defaults:

```yaml
loadBalancerClass: loafer.dev/static
claimServicesWithoutClass: false
annotationPrefix: loafer.dev
allowedCIDRs: []
namespaces: []
leaderElection: {enabled: true, namespace: ""}   # namespace defaults to pod ns
metricsBindAddress: ":8080"
healthProbeBindAddress: ":8081"
logLevel: info
```

- Unknown fields are a startup error (`sigs.k8s.io/yaml` `UnmarshalStrict`).
  Beware: JSON field matching is case-insensitive, so a wrong-case field is
  NOT unknown.
- Validation at startup (bad CIDR, bad log level, empty class, prefix
  containing `/`) exits non-zero with a clear message.
- Flags are only `--config` and `--version`.

## 4. Architecture

- **Go 1.26**, `sigs.k8s.io/controller-runtime` **v0.24** (one manager, one
  reconciler, no CRD machinery). SSA uses the v0.24 typed apply API:
  `r.Status().Apply(ctx, corev1ac.Service(...).WithStatus(...), client.FieldOwner(FieldManager))`.

```
cmd/loafer/main.go            flags, config store, manager wiring only — no logic
internal/config/              schema, strict loading, validation; Store = hot-reload
internal/ipparse/             annotation parsing + CIDR checks, pure funcs (100% covered)
internal/controller/
  service_controller.go       the reconciler (~200 lines incl. comments)
  ingress.go                  pure helpers: desiredIngress, ingressEqual, ownsIngress
  suite_test.go               envtest integration suite
  reload_test.go              live config-reload envtest (second manager)
deploy/                       kustomize base (namespace, RBAC, deployment, configMapGenerator)
deploy/admission-warnings.yaml  optional VAP, not in the kustomize base
examples/                     basic.yaml (annotated Service), config.yaml (local run)
hack/e2e.sh                   kind smoke test (incl. admission-warning check)
docs/configuration.md         full config reference
```

Keep pure logic in functions that take values and return values. If the
reconciler grows past ~200 lines, factor logic out, don't add layers.

RBAC (in `deploy/rbac.yaml`, mirrored by kubebuilder markers in the
reconciler): `services` get/list/watch, `services/status` patch/update,
`events.k8s.io/events` create/patch/update, `coordination.k8s.io/leases`
for leader election — plus core-group `events` create/patch in the
namespaced leader-election Role only: controller-runtime's leader election
still records "became leader" through the legacy core events API (bit us in
v0.2.0; the e2e now greps controller logs for `forbidden`). Nothing more.

## 5. Testing (non-negotiable; all in place and green)

- **Unit**: `internal/config`, `internal/ipparse` (both 100%), plus the pure
  controller helpers. Table-driven.
- **Integration** (`internal/controller/suite_test.go`, envtest, plain `go
  test` style — no ginkgo): assign, update, release, hostname, ineligible
  class never touched, type change clears, invalid annotation emits Warning
  and preserves status, idempotent re-reconcile (no event series), field
  manager verified via managedFields. `reload_test.go` runs a second manager
  (`SkipNameValidation` — controller names are globally unique per process)
  against the same envtest API server and proves a config-file rewrite
  reclassifies a Service with no object edits. Tests skip gracefully when
  `KUBEBUILDER_ASSETS` is unset; `make test` sets it via `setup-envtest`
  (envtest k8s 1.33.0). Controller package sits at ~94%.
- **E2E** (`make e2e`): kind cluster, build+load image, `kubectl apply -k
  deploy/`, apply `examples/basic.yaml`, assert `203.0.113.10` appears in
  status, remove annotation, assert cleared; then apply
  `deploy/admission-warnings.yaml` and assert an invalid annotation produces
  a kubectl warning.
- **Coverage gate**: `make coverage` filters `cmd/` from the profile and
  enforces ≥ **85%** (currently ~96%). CI runs the same target.

Test names in the envtest suite create uniquely named Services in the
`default` namespace and clean up via `t.Cleanup`; the manager is shared
across tests via `TestMain`.

## 6. CI & releases

- `.github/workflows/ci.yaml` (PRs + main): `go vet` + `golangci-lint`
  (version pinned in the Makefile and mirrored in the workflow — keep them
  in sync), `go mod tidy` diff check, `make coverage`, `make e2e`,
  multi-arch image build without push.
- `.github/workflows/release.yaml` (tags `v*`): goreleaser v2 publishes
  binaries (linux/darwin × amd64/arm64), multi-arch images to GHCR
  (`Dockerfile.goreleaser` copies the prebuilt binary; the root `Dockerfile`
  builds from source for dev/CI), SBOMs via syft, GitHub build-provenance
  attestation. SemVer; `--version` prints version/commit/date via ldflags.
- Tool pins live in the Makefile (`golangci-lint v2.12.2`, `setup-envtest
  release-0.21` binaries `1.33.0`, `kind v0.29.0`); tools install to `./bin`.

## 7. Current repo state (handover)

- **Pushed and released.** `main` lives at `git@github.com:spideyfusion/loafer.git`,
  CI is green, and **v0.1.0** is published: GitHub release with binaries
  (linux/darwin × amd64/arm64), SBOMs and checksums, plus multi-arch images
  `ghcr.io/spideyfusion/loafer:0.1.0` and `:latest` (note: image tags have
  no `v` prefix — goreleaser uses `{{ .Version }}`).
- v0.1.0 release quirk: the tag was force-moved once before release (to drop
  a stray file), and two release runs raced. The release *archives* embed
  orphaned-but-content-identical commit `56cadc6` in `--version`; the GHCR
  *images* embed the real tag commit `38e1f5f`. Functionally identical; the
  failed duplicate run on the Actions tab is cosmetic. To make it pristine,
  delete the v0.1.0 release with an authenticated `gh` and re-run the
  release workflow.
- `make help` lists all targets; everything runs from a clean clone with Go
  and Docker only (envtest binaries and tools are fetched into `./bin`,
  which is gitignored along with coverage files).

## 8. Acceptance criteria → verification status

1. `kubectl apply -k deploy/` + `examples/basic.yaml` shows the annotated IP
   under `EXTERNAL-IP` within seconds — **verified** (kind e2e, twice).
2. Removing the annotation returns the service to `<pending>` — **verified**
   (kind e2e + envtest).
3. A service without the configured class is never touched — **verified**
   (envtest `TestIneligibleClassNeverTouched` + unit tests).
4. Invalid IP / out-of-CIDR annotation → Warning Event, no status change —
   **verified** (envtest `TestInvalidAnnotation`; CIDR paths unit-tested).
5. `make test` and `make e2e` pass from a clean clone; coverage gate green —
   **verified locally and in GitHub Actions** (all five CI jobs green).
6. README quickstart works verbatim — **live**: repo is public and
   `ghcr.io/spideyfusion/loafer:latest` is pullable (verified by running the
   released image and binary).

## 9. Sensible next steps

- Optionally clean up the v0.1.0 release-run race (see §7).
- The former "v2 ideas" (config hot-reload, `events.k8s.io` recorder,
  admission warnings) shipped in v0.2.0. Remaining ideas, none committed:
  a Helm chart, per-Service `ipMode` control via annotation, richer
  duplicate-IP reporting (metric or Event instead of a log line).

Lesson from the first release: golangci-lint release binaries must be new
enough for the module's `go` directive — a `go install`-built copy can pass
locally while the same pinned version fails in CI (this bit v2.1.6 with Go
1.26; now pinned to v2.12.2 in both the Makefile and `ci.yaml`).
