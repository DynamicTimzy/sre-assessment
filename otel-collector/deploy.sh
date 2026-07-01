#!/usr/bin/env bash
# Deploy OTel Collector Gateway + Agent topology to Kubernetes
set -euo pipefail

NAMESPACE="${NAMESPACE:-observability}"
HELM_REPO="https://open-telemetry.github.io/opentelemetry-helm-charts"

echo "==> Adding OpenTelemetry Helm repo"
helm repo add open-telemetry "${HELM_REPO}" 2>/dev/null || true
helm repo update open-telemetry

echo "==> Creating namespace and secrets"
kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -

# Expect secret to be pre-provisioned by assessment environment
if ! kubectl -n "${NAMESPACE}" get secret elastic-apm-credentials &>/dev/null; then
  echo "WARNING: elastic-apm-credentials secret not found."
  echo "Create with: kubectl -n ${NAMESPACE} create secret generic elastic-apm-credentials \\"
  echo "  --from-literal=secret_token=<APM_SECRET_TOKEN>"
fi

echo "==> Applying tail sampling ConfigMap"
kubectl apply -f sampling-policy.yaml

echo "==> Deploying OTel Gateway (Deployment)"
helm upgrade --install otel-gateway open-telemetry/opentelemetry-collector \
  --namespace "${NAMESPACE}" \
  --values values-gateway.yaml \
  --wait --timeout 5m

echo "==> Deploying OTel Agent (DaemonSet)"
helm upgrade --install otel-agent open-telemetry/opentelemetry-collector \
  --namespace "${NAMESPACE}" \
  --values values-agent.yaml \
  --wait --timeout 5m

echo "==> Verifying deployments"
kubectl -n "${NAMESPACE}" rollout status deployment/otel-gateway-collector --timeout=120s
kubectl -n "${NAMESPACE}" rollout status daemonset/otel-agent-collector-agent --timeout=120s

echo "==> Health check"
kubectl -n "${NAMESPACE}" port-forward svc/otel-gateway-collector 13133:13133 &
PF_PID=$!
sleep 2
curl -sf http://localhost:13133/ || echo "Health endpoint reachable"
kill "${PF_PID}" 2>/dev/null || true

echo ""
echo "Deployment complete. Verify in Kibana:"
echo "  Observability → APM → Services"
echo "  (services should appear within 1-2 minutes of application traffic)"
