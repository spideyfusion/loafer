# Configuration reference

loafer reads a single YAML file at startup, from the path given by
`--config` (default `/etc/loafer/config.yaml`). There is no hot-reload —
restart the pod to apply changes. Unknown fields and invalid values are
startup errors.

All fields are optional. The zero-value (empty) file is valid and equals:

```yaml
loadBalancerClass: loafer.dev/static
claimServicesWithoutClass: false
annotationPrefix: loafer.dev
allowedCIDRs: []
namespaces: []
leaderElection:
  enabled: true
  namespace: ""
metricsBindAddress: ":8080"
healthProbeBindAddress: ":8081"
logLevel: info
```

| Field | Default | Meaning |
|---|---|---|
| `loadBalancerClass` | `loafer.dev/static` | The `spec.loadBalancerClass` value this controller claims. Services with a different class are never touched. |
| `claimServicesWithoutClass` | `false` | Also claim Services with **no** `loadBalancerClass`. Only enable this if nothing else (cloud controller, MetalLB) could claim them — two implementations writing the same status will fight. |
| `annotationPrefix` | `loafer.dev` | Prefix for the `<prefix>/ips` and `<prefix>/hostname` annotations. Lets forks rename without code changes. Must not contain `/`. |
| `allowedCIDRs` | `[]` | When non-empty, every annotated IP must fall within at least one CIDR, otherwise the annotation is rejected (`Warning/InvalidAnnotation`). |
| `namespaces` | `[]` | When non-empty, only Services in these namespaces are reconciled (and watched). |
| `leaderElection.enabled` | `true` | Leader election for running multiple replicas. |
| `leaderElection.namespace` | pod namespace | Namespace holding the election Lease. |
| `metricsBindAddress` | `:8080` | Prometheus metrics endpoint. `0` disables. |
| `healthProbeBindAddress` | `:8081` | `/healthz` and `/readyz` endpoint. |
| `logLevel` | `info` | One of `debug`, `info`, `warn`, `error`. |

## Flags

| Flag | Meaning |
|---|---|
| `--config` | Path to the config file. |
| `--version` | Print version/commit/build date and exit. |

Everything else lives in the file.
