# SRE Assessment — Observability & Infrastructure Monitoring

Production-grade observability implementation for the Google Online Boutique microservices
architecture, using OpenTelemetry for application instrumentation and the Elastic Stack
(Elasticsearch, Kibana, APM Server, Fleet-managed Elastic Agents) as the observability backend.

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────────┐
│  Browser (RUM Agent)                                                    │
│    └── traceparent headers on fetch/XHR                                 │
├─────────────────────────────────────────────────────────────────────────┤
│  Microservices (frontend, cartservice, paymentservice)                  │
│    └── OTel SDK → OTLP → DaemonSet Agent (hostIP:4317)               │
├─────────────────────────────────────────────────────────────────────────┤
│  OTel Collector Topology                                                │
│    Agent (DaemonSet): OTLP + Zipkin + hostmetrics → Gateway            │
│    Gateway (Deployment): tail_sampling → OTLP → Elastic APM Server     │
├─────────────────────────────────────────────────────────────────────────┤
│  Elastic Stack                                                          │
│    APM Server (:8200 OTLP) → Elasticsearch → Kibana                    │
│    Fleet Agents → VMs, PostgreSQL, Redis, NGINX, Network flows          │
└─────────────────────────────────────────────────────────────────────────┘
```

## Repository Structure

```
sre-assessment/
├── otel-collector/          # Helm values, sampling policy, deploy script
├── instrumentation/         # Per-service OTel code (Go, C#, Node.js)
├── rum/                     # Elastic APM RUM agent integration
├── dashboards/              # Kibana Saved Objects (NDJSON)
├── infrastructure/          # Elastic Agent policies, alerting rules
├── docs/                    # Decision log, trace validation guide
└── scripts/                 # Traffic generation for trace validation
```

## Prerequisites

- Kubernetes cluster with Online Boutique deployed (`boutique` namespace)
- Elastic Stack 8.12+ (ECK or Helm): Elasticsearch, Kibana, APM Server, Fleet Server
- `kubectl`, `helm` CLI tools
- Elastic APM secret token or API key

## Quick Start

### 1. Deploy OpenTelemetry Collector

```bash
# Create APM credentials secret
kubectl create namespace observability
kubectl -n observability create secret generic elastic-apm-credentials \
  --from-literal=secret_token=<YOUR_APM_SECRET_TOKEN>

# Deploy gateway + agent topology
cd otel-collector
chmod +x deploy.sh
./deploy.sh
```

### 2. Instrument Microservices

Apply Kubernetes patches to inject OTel environment variables:

```bash
kubectl -n boutique patch deployment frontend --patch-file instrumentation/frontend/k8s-patch.yaml
kubectl -n boutique patch deployment cartservice --patch-file instrumentation/cartservice/k8s-patch.yaml
kubectl -n boutique patch deployment paymentservice --patch-file instrumentation/paymentservice/k8s-patch.yaml
```

Integrate instrumentation code into each service (see per-service directories), rebuild
container images, and roll out:

```bash
# Example for frontend
cd instrumentation/frontend
docker build -f Dockerfile.otel -t frontend:otel ../../path-to-boutique/src/frontend
kubectl -n boutique set image deployment/frontend server=frontend:otel
```

### 3. Enable RUM

1. Apply APM Server RUM config: `kubectl apply -f rum/apm-server-rum-config.yaml`
2. Copy `rum/apm-rum.js` into frontend static assets
3. Add `rum/template-snippet.html` to frontend HTML templates
4. Add `data-rum-action` attributes to UI buttons (Add to Cart, Checkout)

### 4. Deploy Infrastructure Monitoring

Enroll Elastic Agents on VMs and configure Fleet policies:

```bash
# Import Fleet policies via Kibana UI or API
# Files in infrastructure/elastic-agent-policies/
# Files in infrastructure/postgres-integration/
# Files in infrastructure/redis-integration/
# Files in infrastructure/nginx-integration/
# Files in infrastructure/network-monitoring/
```

### 5. Import Kibana Dashboards and Alerts

```bash
# Dashboards
curl -X POST "https://kibana:5601/api/saved_objects/_import" \
  -H "kbn-xsrf: true" \
  -H "Authorization: ApiKey <API_KEY>" \
  --form file=@dashboards/service-health.ndjson

curl -X POST "https://kibana:5601/api/saved_objects/_import" \
  -H "kbn-xsrf: true" -H "Authorization: ApiKey <API_KEY>" \
  --form file=@dashboards/rum-performance.ndjson

curl -X POST "https://kibana:5601/api/saved_objects/_import" \
  -H "kbn-xsrf: true" -H "Authorization: ApiKey <API_KEY>" \
  --form file=@dashboards/business-transactions.ndjson

# Alert rules
curl -X POST "https://kibana:5601/api/saved_objects/_import" \
  -H "kbn-xsrf: true" -H "Authorization: ApiKey <API_KEY>" \
  --form file=@infrastructure/alerting-rules/all-rules.ndjson
```

### 6. Validate Distributed Tracing

```bash
# Port-forward frontend or use ingress URL
kubectl -n boutique port-forward svc/frontend 8080:80 &

# Run checkout flow
chmod +x scripts/checkout-flow.sh
./scripts/checkout-flow.sh http://localhost:8080
```

Verify in Kibana:
- **APM → Services**: frontend, cartservice, paymentservice appear
- **APM → Traces**: full checkout waterfall with connected spans
- **APM → Service Map**: inter-service dependencies visible
- **Observability → Metrics Explorer**: custom business metrics queryable
- **Observability → User Experience**: Core Web Vitals populated

## Instrumented Services

| Service | Language | Custom Spans | Custom Metrics |
|---------|----------|-------------|----------------|
| frontend | Go | `render-product-page`, `validate-session` | `frontend.page.views` |
| cartservice | C# | `validate-cart-contents`, `calculate-shipping-cost` | `cart.items.added`, `cart.items.removed` |
| paymentservice | Node.js | `validate-payment-details`, `process-charge` | `payment.charges.total`, `payment.charge.amount` |

## Alerting Rules Summary

| Alert | Threshold | Component |
|-------|-----------|-----------|
| High CPU Sustained | >85% for 5 min | VMs |
| Disk Space Critical | <10% free | VMs |
| Memory Pressure | <500MB available | VMs |
| PG Connection Pool | >80% max_connections | PostgreSQL |
| PG Cache Hit Ratio | <95% | PostgreSQL |
| Redis Memory | >85% maxmemory | Redis |
| Redis Eviction Rate | >100 keys/5min | Redis |
| NGINX 5xx Spike | >5% for 2 min | Ingress |
| NGINX Upstream Down | >10 x 502/503 in 2 min | Ingress |
| Unexpected Egress | deny to non-RFC1918 | Network |

## Further Reading

- [Architectural Decisions](docs/DECISIONS.md)
- [Trace Validation Guide](docs/TRACE_VALIDATION.md)
