# loafer

**A LoadBalancer that doesn't lift a finger.**

A tiny Kubernetes controller that publishes a user-chosen IP address into the
status of `LoadBalancer` Services, based on an annotation. Your external load
balancer does all the work; loafer just tells Kubernetes about it.

Built for clusters where the actual load balancer lives *outside* the cluster
— an on-prem appliance, a router, a manually managed VIP. No cloud controller
ever populates the Service status there, so `kubectl get svc` shows
`<pending>` forever and everything that consumes the status field
(external-dns, ingress controllers, GitOps health checks) breaks. You already
know which IP the Service is reachable on; loafer writes it where
Kubernetes expects it.

## Quickstart (60 seconds)

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
   other load-balancer implementations. If your cluster cannot set the field,
   `claimServicesWithoutClass: true` also claims class-less Services, at the
   risk of fighting a cloud controller — leave it off unless you know you
   need it.
3. The namespace matches the configured selector (default: all namespaces).

For eligible Services it parses the annotations:

| Annotation | Meaning |
|---|---|
| `loafer.dev/ips` | Comma-separated IPs to publish, e.g. `203.0.113.10` or `203.0.113.10,2001:db8::10`. IPv4 and IPv6. |
| `loafer.dev/hostname` (optional) | Also publish a `hostname` ingress entry. Only takes effect alongside a valid `ips` annotation. |

and server-side-applies `.status.loadBalancer.ingress` with field manager
`loafer`. Nothing else on the Service is ever written.

Behavior details:

- **Valid annotation** → status is set to exactly the declared IPs
  (deduplicated, order preserved), Event `Normal/IPAssigned`.
- **Invalid annotation** (unparseable IP, or outside `allowedCIDRs`) →
  existing status is left untouched, Event `Warning/InvalidAnnotation` with
  the reason. Invalid input is terminal until the object changes; there is no
  hot retry loop.
- **Annotation removed or emptied** → the published entries are cleared,
  Event `Normal/IPReleased`.
- **Service becomes ineligible** (type changed away from `LoadBalancer`, or
  recreated under another class) → entries owned by loafer are cleared
  once, then the Service is left alone. Ownership is checked via
  `managedFields`, so entries written by another implementation are never
  touched.
- **Two Services declaring the same IP** → allowed; that is your call. The
  controller logs it at `info`.
- **Write conflicts** (another field manager owns the status) → surface as
  errors and retry with backoff, never silently overwrite.

## Configuration

The controller reads a single YAML file at startup (`--config`, default
`/etc/loafer/config.yaml`). There is no hot-reload: restart the pod to
apply changes. All fields are optional; an empty file is valid:

```yaml
loadBalancerClass: loafer.dev/static   # class this controller claims
claimServicesWithoutClass: false          # also claim services with no class set (risky)
annotationPrefix: loafer.dev           # annotation prefix, for forks/renames
allowedCIDRs: []                          # e.g. ["203.0.113.0/24", "2001:db8::/64"]
namespaces: []                            # empty = all namespaces
leaderElection:
  enabled: true
  namespace: ""                           # defaults to the pod namespace
metricsBindAddress: ":8080"
healthProbeBindAddress: ":8081"
logLevel: info                            # debug|info|warn|error
```

Unknown fields and invalid values (bad CIDR, bad log level) are startup
errors, so typos fail loudly. The only flags are `--config` and `--version`.

Edit `deploy/config.yaml` before `kubectl apply -k deploy/` to change the
installed configuration.

### Metrics

Standard controller-runtime metrics on `metricsBindAddress`, plus:

- `loafer_ip_assignments_total{result="assigned|released|invalid"}`

## What this is not

- **No IPAM.** loafer does not allocate IPs from pools; you choose the IP.
  If you want allocation (and an in-cluster data plane), use
  [MetalLB](https://metallb.universe.tf/).
- **No data plane.** It does not program routes, ARP, BGP, or firewalls.
  Delivering traffic to the IP is entirely your responsibility — that's the
  point: your load balancer already does it.
- **No CRDs, no webhooks.** Configuration is a file plus annotations.

## Development

```sh
make help    # list all targets
make test    # unit + envtest integration tests
make e2e     # kind end-to-end smoke test (needs Docker)
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for the full dev guide.

## License

[Apache-2.0](LICENSE)
