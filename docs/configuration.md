# Configuration reference

loafer reads a single YAML file from the path given by `--config` (default
`/etc/loafer/config.yaml`). Unknown fields and invalid values are startup
errors.

## Hot-reload

The file is polled every 10 seconds (polling, not inotify, so ConfigMap
symlink swaps are always picked up):

- A **valid** change applies immediately and triggers a resync of all
  watched Services, so eligibility changes take effect without object edits.
- An **invalid** change (unknown field, bad CIDR, bad log level) is logged
  once and ignored; the previous configuration stays active. Fix the file
  and it is picked up on the next poll.
- `logLevel` changes apply live.

Fields fixed at manager startup log a "requires a restart" notice when
changed: `metricsBindAddress`, `healthProbeBindAddress`, `leaderElection`,
`ipAliases`, and any change that *widens* the namespace watch scope (adding
a namespace to a non-empty `namespaces` list, or emptying the list).
Narrowing the namespace selector works live.

Note that the *content* of the IP-aliases ConfigMap is always live — it is
watched, not polled — only its name/namespace are fixed at startup.

All fields are optional. The zero-value (empty) file is valid and equals:

```yaml
loadBalancerClass: loafer.dev/static
claimServicesWithoutClass: false
annotationPrefix: loafer.dev
allowedCIDRs: []
namespaces: []
ipAliases:
  configMapName: loafer-ip-aliases
  namespace: ""
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
| `namespaces` | `[]` | When non-empty, only Services in these namespaces are reconciled (and watched); Services elsewhere are never touched, not even leftover cleanup. Widening this list requires a restart (see hot-reload above). |
| `ipAliases.configMapName` | `loafer-ip-aliases` | ConfigMap with `alias: IP` entries that Services reference via the `ip-names` annotation. Empty disables aliases. Requires a restart when changed. |
| `ipAliases.namespace` | controller namespace | Namespace of that ConfigMap (resolved from `POD_NAMESPACE` or the serviceaccount mount when empty). Requires a restart when changed. |
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
