# Contributing to loafer

Thanks for helping out! loafer ("a LoadBalancer that doesn't lift a finger")
is deliberately small — the whole codebase is readable in one sitting, and
we'd like to keep it that way.

## Dev setup

You need Go (see `go.mod` for the version) and Docker (for the e2e test).
Everything else is installed into `./bin` by the Makefile, pinned to known
versions:

```sh
make help        # list all targets
make build       # build ./bin/loafer
make test        # unit + envtest integration tests (downloads envtest binaries)
make coverage    # same, with the 85% coverage gate CI enforces
make lint        # golangci-lint
make e2e         # kind end-to-end smoke test
```

To run the controller locally against your current kubeconfig context:

```sh
make run
```

## Layout

```
cmd/loafer/        flag parsing, config load, manager wiring — no logic
internal/config/      config schema, loading, validation
internal/controller/  the reconciler (+ envtest suite)
internal/ipparse/     annotation parsing, CIDR checks — pure functions
deploy/               kustomize base
examples/             example manifests
hack/                 e2e script
```

Keep pure logic (parsing, validation, computing desired state) in functions
that take values and return values — no client, no context. That's where most
tests live. If the reconciler grows past ~200 lines, factor logic out rather
than adding abstraction layers.

## Pull requests

- Every change lands with its tests. Unit tests for pure logic, envtest for
  reconciler behavior.
- CI runs `go vet`, `golangci-lint`, the test suite with an 85% coverage
  gate, a `go mod tidy` diff check, the kind e2e, and a multi-arch image
  build. `make lint coverage` locally covers most of it.
- Dependencies: `go.mod` is the single source of truth. Don't add a
  dependency a stdlib function could replace.
- Keep commits focused; a PR should be one logical change.

## Reporting issues

Use the issue templates. For security issues see [SECURITY.md](SECURITY.md) —
please do not open public issues for vulnerabilities.
