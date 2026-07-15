<div align="center">

<img src="docs/loaf.png" alt="loafer logo" width="260">

# loafer

**A LoadBalancer that doesn't lift a finger.**

[![CI](https://github.com/spideyfusion/loafer/actions/workflows/ci.yaml/badge.svg)](https://github.com/spideyfusion/loafer/actions/workflows/ci.yaml)
[![Release](https://img.shields.io/github/v/release/spideyfusion/loafer)](https://github.com/spideyfusion/loafer/releases/latest)
[![Go version](https://img.shields.io/github/go-mod/go-version/spideyfusion/loafer)](go.mod)
[![License](https://img.shields.io/github/license/spideyfusion/loafer)](LICENSE)
[![Container image](https://img.shields.io/badge/ghcr.io-spideyfusion%2Floafer-2496ED?logo=docker&logoColor=white)](https://github.com/spideyfusion/loafer/pkgs/container/loafer)

</div>

---

loafer is a tiny Kubernetes controller that publishes a user-chosen IP
address into the status of `LoadBalancer` Services, based on an annotation.

It's built for clusters where the actual load balancer lives *outside* the
cluster — an on-prem appliance, a router, a manually managed VIP. No cloud
controller ever populates the Service status there, so `kubectl get svc`
shows `<pending>` forever and everything that consumes the status field
(external-dns, ingress controllers, GitOps health checks) breaks. You
already know which IP the Service is reachable on. Your load balancer does
all the work; **loafer just tells Kubernetes about it.**

## Highlights

- **One annotation** — `loafer.dev/ips: 203.0.113.10` and the IP shows up
  under `EXTERNAL-IP`. IPv4, IPv6, or both.
- **Named IPs** — or reference an alias (`loafer.dev/ip-names: public-lb`)
  resolved through a ConfigMap; edit the ConfigMap once and every Service
  using the alias re-points, live.
- **Plays nice** — claims only Services with its `loadBalancerClass`, writes
  status via server-side apply, and never touches anything another
  implementation owns. Coexists safely with MetalLB or cloud controllers.
- **Hot-reloadable config** — a single YAML file, re-checked every 10
  seconds; changes apply live with a full resync.
- **Typo-proof (optional)** — a CEL admission policy warns right in your
  `kubectl` output when an annotation isn't a valid IP list.
- **Boring on purpose** — no CRDs, no webhooks, no IPAM, no data plane.
  Small enough to read in one sitting.

## Quickstart

Install the controller:

```sh
kubectl apply -k https://github.com/spideyfusion/loafer/deploy
```

Create a `LoadBalancer` Service with the loafer class and your IP:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: demo
  annotations:
    loafer.dev/ips: 203.0.113.10
spec:
  type: LoadBalancer
  loadBalancerClass: loafer.dev/static
  selector:
    app: demo
  ports:
    - port: 80
```

Within seconds:

```
$ kubectl get svc demo
NAME   TYPE           CLUSTER-IP    EXTERNAL-IP    PORT(S)        AGE
demo   LoadBalancer   10.96.12.34   203.0.113.10   80:30000/TCP   5s
```

Remove the annotation and the Service returns to `<pending>`.

## How it works

loafer watches Services and reconciles the ones that meet **all** of these
rules:

1. `spec.type` is `LoadBalancer`.
2. `spec.loadBalancerClass` equals the configured class (default
   `loafer.dev/static`). Services with no class or another class are never
   touched — this is the standard Kubernetes mechanism for coexisting with
   other load-balancer implementations. If your cluster cannot set the
   field, `claimServicesWithoutClass: true` also claims class-less Services,
   at the risk of fighting a cloud controller — leave it off unless you know
   you need it.
3. The namespace matches the configured selector (default: all namespaces).

For eligible Services it parses the annotations:

| Annotation | Meaning |
|---|---|
| `loafer.dev/ips` | Comma-separated IPs to publish, e.g. `203.0.113.10` or `203.0.113.10,2001:db8::10`. IPv4 and IPv6. |
| `loafer.dev/ip-names` | Comma-separated [IP alias names](#ip-aliases) resolved through a ConfigMap. Mutually exclusive with `loafer.dev/ips` — a Service setting both gets a warning and is ignored. |
| `loafer.dev/hostname` (optional) | Also publish a `hostname` ingress entry. Only takes effect alongside a valid `ips` or `ip-names` annotation. |

and server-side-applies `.status.loadBalancer.ingress` with field manager
`loafer`. Nothing else on the Service is ever written.

<details>
<summary><b>Behavior details</b></summary>

- **Valid annotation** → status is set to exactly the declared IPs
  (deduplicated, order preserved), Event `Normal/IPAssigned`.
- **Invalid annotation** (unparseable IP, or outside `allowedCIDRs`) →
  existing status is left untouched, Event `Warning/InvalidAnnotation` with
  the reason. Invalid input is terminal until the object changes; there is
  no hot retry loop.
- **Annotation removed or emptied** → the published entries are cleared,
  Event `Normal/IPReleased`.
- **Service becomes ineligible** (type changed away from `LoadBalancer`, or
  recreated under another class) → entries owned by loafer are cleared once,
  then the Service is left alone. Ownership is checked via `managedFields`,
  so entries written by another implementation are never touched.
- **Two Services declaring the same IP** → allowed; that is your call. The
  controller logs it at `info`.
- **Write conflicts** (another field manager owns the status) → surface as
  errors and retry with backoff, never silently overwrite.

</details>

## IP aliases

Hardcoding the same IP into twenty Services gets old. Put it in a ConfigMap
in the controller's namespace instead — key is the alias, value is the IP
(or a comma-separated list, e.g. for dual-stack):

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: loafer-ip-aliases
  namespace: loafer-system
data:
  public-lb: 203.0.113.10
  dual-stack-lb: 203.0.113.20,2001:db8::20
```

and reference the *name* from Services:

```yaml
metadata:
  annotations:
    loafer.dev/ip-names: public-lb
```

Editing the ConfigMap re-points **every** Service that references the alias,
live — loafer watches it and re-reconciles the users within seconds. No
Service edits, no rollouts.

Rules:

- A Service uses either `loafer.dev/ips` or `loafer.dev/ip-names`, never
  both at once (both set → `Warning/InvalidAnnotation`, nothing written).
- An unknown alias (or a missing ConfigMap) is an invalid annotation:
  warning event, and any already-published status is left untouched — so
  deleting an alias out from under a Service never yanks its published IP.
  The Service heals automatically the moment the alias appears.
- Resolved IPs go through the same pipeline as raw ones: `allowedCIDRs`
  enforcement, dedup, order preserved.
- The ConfigMap name/namespace are configurable via `ipAliases` (defaults:
  `loafer-ip-aliases` in the controller's namespace). See
  `examples/aliases.yaml` for a complete example.

## Configuration

The controller reads a single YAML file (`--config`, default
`/etc/loafer/config.yaml`) and **hot-reloads** it: the file is re-checked
every 10 seconds, a valid change applies immediately (followed by a full
resync), and a broken change is logged and ignored while the previous
configuration stays active. All fields are optional; an empty file is valid:

```yaml
loadBalancerClass: loafer.dev/static   # class this controller claims
claimServicesWithoutClass: false       # also claim services with no class set (risky)
annotationPrefix: loafer.dev           # annotation prefix, for forks/renames
allowedCIDRs: []                       # e.g. ["203.0.113.0/24", "2001:db8::/64"]
namespaces: []                         # empty = all namespaces
ipAliases:
  configMapName: loafer-ip-aliases     # empty disables aliases
  namespace: ""                        # defaults to the controller's namespace
leaderElection:
  enabled: true
  namespace: ""                        # defaults to the pod namespace
metricsBindAddress: ":8080"
healthProbeBindAddress: ":8081"
logLevel: info                         # debug|info|warn|error
```

Unknown fields and invalid values (bad CIDR, bad log level) fail loudly.
The only flags are `--config` and `--version`. Edit `deploy/config.yaml`
before `kubectl apply -k deploy/` to change the installed configuration —
see the [full configuration reference](docs/configuration.md), including
which fields still need a restart.

### Metrics

Standard controller-runtime metrics on `metricsBindAddress`, plus:

- `loafer_ip_assignments_total{result="assigned|released|invalid"}`

### Admission warnings (optional)

On Kubernetes ≥ 1.31 you can additionally get typo feedback right at
`kubectl apply` time, as a warning (never a rejection):

```sh
kubectl apply -f https://raw.githubusercontent.com/spideyfusion/loafer/main/deploy/admission-warnings.yaml
```

```
$ kubectl annotate svc demo loafer.dev/ips=not-an-ip
Warning: loafer.dev/ips contains an entry that is not a valid IP address; ...
service/demo annotated
```

This is a CEL `ValidatingAdmissionPolicy` with `validationActions: [Warn]` —
no webhook, no TLS, no controller involvement. If you changed
`annotationPrefix`, edit the annotation name in the policy to match.

## What loafer is not

- **Not IPAM.** loafer does not allocate IPs from pools; you choose the IP.
  If you want allocation (and an in-cluster data plane), use
  [MetalLB](https://metallb.universe.tf/).
- **Not a data plane.** It does not program routes, ARP, BGP, or firewalls.
  Delivering traffic to the IP is entirely your responsibility — that's the
  point: your load balancer already does it.
- **Not a CRD zoo.** Configuration is a file plus annotations.

## Development

```sh
make help    # list all targets
make test    # unit + envtest integration tests
make e2e     # kind end-to-end smoke test (needs Docker)
```

Contributions are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md) for the
dev setup and PR expectations, and [SECURITY.md](SECURITY.md) for reporting
vulnerabilities.

## License

[Apache-2.0](LICENSE)
