#!/usr/bin/env bash
# Apply all instrumentation patches and roll out services
set -euo pipefail

NAMESPACE="${NAMESPACE:-boutique}"

echo "==> Applying OTel environment variable patches"
for svc in frontend cartservice paymentservice; do
  echo "    Patching ${svc}..."
  kubectl -n "${NAMESPACE}" patch deployment "${svc}" \
    --patch-file "instrumentation/${svc}/k8s-patch.yaml"
done

echo "==> Waiting for rollouts"
for svc in frontend cartservice paymentservice; do
  kubectl -n "${NAMESPACE}" rollout status "deployment/${svc}" --timeout=180s
done

echo "==> Instrumentation applied. Verify in Kibana APM → Services"
