#!/usr/bin/env bash
# End-to-end smoke test on kind: deploy the kustomize base, apply the example
# Service, assert the annotated IP appears in status, remove the annotation,
# assert it is cleared. Run via `make e2e`.
set -euo pipefail

KIND=${KIND:-kind}
IMG=${IMG:-ghcr.io/spideyfusion/loafer:e2e}
CLUSTER=${CLUSTER:-loafer-e2e}

log() { echo "--- $*"; }

log "building image $IMG"
docker build -t "$IMG" .

log "creating kind cluster $CLUSTER"
"$KIND" create cluster --name "$CLUSTER" --wait 120s
trap '"$KIND" delete cluster --name "$CLUSTER" || true' EXIT

"$KIND" load docker-image "$IMG" --name "$CLUSTER"

log "deploying loafer"
kubectl kustomize deploy/ \
  | sed "s|ghcr.io/spideyfusion/loafer:latest|$IMG|" \
  | kubectl apply -f -
kubectl -n loafer-system rollout status deploy/loafer --timeout=120s

log "applying example service"
kubectl apply -f examples/basic.yaml

log "waiting for the annotated IP to appear in status"
want=203.0.113.10
for _ in $(seq 1 30); do
  got=$(kubectl get svc demo -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || true)
  [[ "$got" == "$want" ]] && break
  sleep 2
done
if [[ "${got:-}" != "$want" ]]; then
  echo "FAIL: expected EXTERNAL-IP $want, got '${got:-}'" >&2
  kubectl -n loafer-system logs deploy/loafer --tail=50 >&2 || true
  exit 1
fi
log "IP assigned: $got"

log "removing the annotation"
kubectl annotate svc demo loafer.dev/ips-

log "waiting for status to clear"
for _ in $(seq 1 30); do
  got=$(kubectl get svc demo -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || true)
  [[ -z "$got" ]] && break
  sleep 2
done
if [[ -n "${got:-}" ]]; then
  echo "FAIL: expected status to clear, still '$got'" >&2
  kubectl -n loafer-system logs deploy/loafer --tail=50 >&2 || true
  exit 1
fi

log "e2e OK"
