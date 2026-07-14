---
name: Bug report
about: Something doesn't behave as documented
labels: bug
---

**What happened?**

**What did you expect to happen?**

**How to reproduce**

Include the Service manifest (annotations and `loadBalancerClass` matter) and
your `config.yaml` if it differs from the defaults.

```yaml
```

**Environment**

- loafer version (`loafer --version` or image tag):
- Kubernetes version (`kubectl version`):
- Any other LB implementation in the cluster (MetalLB, cloud controller, ...):

**Controller logs / Service events**

```
kubectl -n loafer-system logs deploy/loafer
kubectl describe svc <name>
```
