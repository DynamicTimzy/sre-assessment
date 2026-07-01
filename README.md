# SRE Assessment — Observability & Infrastructure Monitoring

Setup instructions for the Online Boutique observability implementation.

## Repository Structure

```
sre-assessment/
├── otel-collector/                # Helm values, collector configs
│   ├── values-agent.yaml
│   ├── values-gateway.yaml
│   └── sampling-policy.yaml
├── instrumentation/               # Per-service instrumentation code/patches
│   ├── frontend/
│   ├── cartservice/
│   └── paymentservice/
├── rum/                           # Browser SDK integration code
├── dashboards/                    # Kibana Saved Objects (NDJSON exports)
│   ├── service-health.ndjson
│   ├── rum-performance.ndjson
│   └── business-transactions.ndjson
├── infrastructure/                # Agent/Beat configs, alert rules
│   ├── elastic-agent-policies/    # Fleet policy exports or agent.yml
│   ├── postgres-integration/
│   ├── redis-integration/
│   ├── nginx-integration/
│   └── alerting-rules/            # Kibana rule exports (NDJSON)
├── docs/
│   └── DECISIONS.md               # Architectural decision log
└── README.md                      # Setup instructions
```

## Prerequisites

- Kubernetes cluster with Online Boutique deployed (`boutique` namespace)
- Elastic Stack 8.12+ (Elasticsearch, Kibana, APM Server, Fleet Server)
- `kubectl`, `helm` CLI tools
- Elastic APM secret token or API key

## Setup Instructions

### 1. Deploy OpenTelemetry Collector

```bash
kubectl create namespace observability
kubectl -n observability create secret generic elastic-apm-credentials \
  --from-literal=secret_token=<YOUR_APM_SECRET_TOKEN>

helm repo add open-telemetry https://open-telemetry.github.io/opentelemetry-helm-charts
helm repo update open-telemetry

kubectl apply -f otel-collector/sampling-policy.yaml

helm upgrade --install otel-gateway open-telemetry/opentelemetry-collector \
  --namespace observability \
  --values otel-collector/values-gateway.yaml \
  --wait --timeout 5m

helm upgrade --install otel-agent open-telemetry/opentelemetry-collector \
  --namespace observability \
  --values otel-collector/values-agent.yaml \
  --wait --timeout 5m
```

### 2. Instrument Microservices

```bash
kubectl -n boutique patch deployment frontend \
  --patch-file instrumentation/frontend/k8s-patch.yaml
kubectl -n boutique patch deployment cartservice \
  --patch-file instrumentation/cartservice/k8s-patch.yaml
kubectl -n boutique patch deployment paymentservice \
  --patch-file instrumentation/paymentservice/k8s-patch.yaml
```

Integrate the instrumentation source files from each service directory, rebuild images, and roll out.

### 3. Enable RUM

1. Apply `rum/apm-server-rum-config.yaml`
2. Integrate `rum/apm-rum.js` into the frontend
3. Add `rum/template-snippet.html` to frontend templates

### 4. Deploy Infrastructure Monitoring

Configure Fleet policies from:

- `infrastructure/elastic-agent-policies/agent.yml`
- `infrastructure/postgres-integration/`
- `infrastructure/redis-integration/`
- `infrastructure/nginx-integration/`

### 5. Import Dashboards and Alerts

```bash
curl -X POST "https://kibana:5601/api/saved_objects/_import" \
  -H "kbn-xsrf: true" -H "Authorization: ApiKey <API_KEY>" \
  --form file=@dashboards/service-health.ndjson

curl -X POST "https://kibana:5601/api/saved_objects/_import" \
  -H "kbn-xsrf: true" -H "Authorization: ApiKey <API_KEY>" \
  --form file=@dashboards/rum-performance.ndjson

curl -X POST "https://kibana:5601/api/saved_objects/_import" \
  -H "kbn-xsrf: true" -H "Authorization: ApiKey <API_KEY>" \
  --form file=@dashboards/business-transactions.ndjson

curl -X POST "https://kibana:5601/api/saved_objects/_import" \
  -H "kbn-xsrf: true" -H "Authorization: ApiKey <API_KEY>" \
  --form file=@infrastructure/alerting-rules/all-rules.ndjson
```

### 6. Validate Distributed Tracing

Generate checkout traffic and verify traces in Kibana APM (Services, Traces, Service Map).

See [docs/DECISIONS.md](docs/DECISIONS.md) for architectural decisions and trade-offs.
