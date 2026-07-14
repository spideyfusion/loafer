# Security Policy

## Reporting a vulnerability

Please report suspected vulnerabilities privately via
[GitHub Security Advisories](https://github.com/spideyfusion/loafer/security/advisories/new)
("Report a vulnerability"). Do not open a public issue.

You can expect an acknowledgement within a few days. Please include enough
detail to reproduce the issue.

## Scope notes

loafer only ever writes `.status.loadBalancer.ingress` on Services and
holds RBAC for exactly that (plus events and leader-election leases). It has
no data plane: it cannot route, expose, or intercept traffic. Reports about
traffic delivery belong to whatever actually operates the external load
balancer.
