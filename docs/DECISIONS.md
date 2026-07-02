# Architectural Decision Log

This document records key technical decisions, trade-offs, and rationale for the
SRE assessment observability implementation.

---

## ADR-001: Gateway + Agent Collector Topology

**Status:** Accepted

**Context:** The assessment requires both node-level collection (hostmetrics, local OTLP
receivers) and centralized processing (tail-based sampling, export to Elastic).

**Decision:** Deploy two Helm releases of `opentelemetry-collector`:
- **Agent (DaemonSet):** receives OTLP/Zipkin from local pods via `status.hostIP:4317`,
  collects hostmetrics, enriches with k8sattributes, forwards to gateway.
- **Gateway (Deployment, 2 replicas with HPA):** receives from agents, applies tail-based
  sampling, exports to Elastic APM Server via OTLP/gRPC.

**Alternatives considered:**
- Single Deployment collector: cannot collect per-node hostmetrics or receive from pods
  via hostIP without hostNetwork (security concern).
- Sidecar per pod: excessive resource overhead across 11 microservices.

**Trade-offs:**
- (+) Clean separation of concerns; sampling decisions made with complete trace data
- (+) Agents are stateless forwarders; gateway handles backpressure
- (-) Additional network hop (agent → gateway → APM Server)
- (-) Gateway is a single point of failure mitigated by replicas + PDB

---

## ADR-002: Tail-Based Sampling Policy

**Status:** Accepted

**Context:** 11 microservices generating traces at scale will overwhelm Elasticsearch
storage without sampling. Head-based sampling at the SDK level loses error traces.

**Decision:** Tail-based sampling at the gateway with four policies (priority order):
1. **Errors:** 100% of spans with ERROR status
2. **Latency:** 100% of traces with any span >2000ms
3. **Business-critical:** 100% of checkout/payment transactions
4. **Probabilistic:** 10% of remaining successful traces

`decision_wait: 10s` to accommodate async spans (email send, payment callback).

**Alternatives considered:**
- Head-based `parentbased_traceidratio` at SDK: simple but drops error traces when parent
  is unsampled.
- 100% sampling: cost-prohibitive at production scale.

---

## ADR-003: Service Selection for Instrumentation

**Status:** Accepted

**Context:** Assessment requires 3 services in different languages with auto + manual
instrumentation covering the checkout user journey.

**Decision:**
| Service | Language | Rationale |
|---------|----------|-----------|
| frontend | Go | Entry point for all user traffic; HTTP + gRPC client tracing |
| cartservice | C# | Redis integration; cart business logic metrics |
| paymentservice | Node.js | Error recording; payment validation spans |

**Alternatives considered:**
- recommendationservice (Python): valuable but not on critical checkout path
- currencyservice (C++): manual SDK complexity without adding trace depth to checkout flow

---

## ADR-004: OTLP Export Target — DaemonSet Agent via hostIP

**Status:** Accepted

**Context:** Applications must send telemetry to the local node's OTel agent.

**Decision:** Inject `HOST_IP` via `status.hostIP` downward API and set
`OTEL_EXPORTER_OTLP_ENDPOINT=http://$(HOST_IP):4317`.

**Alternatives considered:**
- ClusterIP service: load-balances across all agents, losing per-node hostmetrics correlation
- Sidecar collector: doubles container count per pod

---

## ADR-005: Elastic APM RUM Agent over OTel Web SDK

**Status:** Accepted

**Context:** Browser-side observability must correlate with backend traces in Kibana APM.

**Decision:** Use `@elastic/apm-rum` rather than `@opentelemetry/sdk-trace-web`.

**Rationale:**
- Native Kibana APM User Experience dashboard integration (Core Web Vitals)
- Built-in `distributedTracingOrigins` for traceparent injection
- `transaction.marks` for page load waterfall breakdown
- OTel Web SDK would require additional translation layer for Kibana UX features

**Trade-offs:**
- (-) Vendor-specific agent vs OTel-native approach
- (+) Zero-config correlation in Kibana APM Traces waterfall
- (+) CORS handled via same-origin `/apm` proxy through NGINX ingress

---

## ADR-006: Fleet-Managed Elastic Agents for Infrastructure

**Status:** Accepted

**Context:** Infrastructure monitoring across VMs, PostgreSQL, Redis, NGINX, and network
flows must reach Elasticsearch.

**Decision:** Fleet-managed Elastic Agents with integration-specific policies over
standalone Metricbeat/Filebeat.

**Rationale:**
- Centralized policy management in Kibana Fleet UI
- Unified agent lifecycle (enroll, upgrade, health)
- Integration-specific dashboards ship with the agent policy
- Assessment environment provides Fleet Server

**Alternatives considered:**
- OTel Collector hostmetrics for VMs: acceptable per assessment spec but lacks
  PostgreSQL/Redis/NGINX integrations
- Standalone Beats: harder to manage across 2 VMs + K8s cluster

---

## ADR-007: NGINX Ingress Monitoring — Dual Signal Approach

**Status:** Accepted

**Context:** Load balancer monitoring requires both metrics (request rate, latency) and
logs (per-request detail, status codes).

**Decision:**
- **Metrics:** Elastic Agent `prometheus/metrics` input scraping `:10254/metrics`
- **Logs:** Filebeat/Filestream with JSON access log format

**Rationale:** Metrics provide aggregated SLIs for alerting; logs enable per-request
debugging and correlation with APM traces via `request_id`.

---

## ADR-008: Alert Connector — Webhook

**Status:** Accepted

**Context:** Alert rules must notify operators. Assessment environment provides a webhook
endpoint for alert delivery.

**Decision:** All 10 alert rules use a `webhook-connector` action with structured JSON
payloads containing alert name, affected resource, and metric value.

**Production recommendation:** Replace with PagerDuty/Opsgenie/Slack connectors with
severity-based routing and deduplication.

---

## ADR-009: Dashboard Version Control via NDJSON Export

**Status:** Accepted

**Context:** Dashboards must be reproducible and version-controlled, not manually created.

**Decision:** Export Kibana Saved Objects as NDJSON files committed to `dashboards/`.
Three dashboards with Kibana Controls for service/environment filtering.

**Panel type selection:**
- Lens XY for time-series (RED metrics, error trends)
- Lens Gauge for Core Web Vitals with color-coded thresholds
- Lens Datatable for percentile breakdowns and error tables
- Lens Metric for single-value business KPIs
- Maps for geographic latency distribution

---

## ADR-010: Authentication to Elastic APM Server

**Status:** Accepted

**Decision:** Secret token via Kubernetes Secret (`elastic-apm-credentials`), injected
into collector gateway and application pods as environment variables.

**TLS:** APM Server uses TLS with `insecure_skip_verify: true` for the assessment
environment. Production deployments should mount the cluster CA certificate.

---

## Open Items / Known Gaps

1. ~~**checkoutservice** not instrumented~~ — **Resolved.** Go OTel instrumentation
   added in `instrumentation/checkoutservice/` (gRPC server + client stats handlers,
   `prepare-order-items` / `charge-and-ship` custom spans, `checkout.orders.placed`
   counter and `checkout.order.value` histogram). This closes the frontend↔payment gap.
2. ~~**frontend** custom metrics never exported~~ — **Resolved.** The `MeterProvider`
   originally had no reader; an OTLP metric exporter with a 15s `PeriodicReader` is now
   attached in `instrumentation/frontend/otel_instrumentation.go`.
3. ~~**SSL certificate expiry alert** for NGINX~~ — **Resolved.** Added an Uptime TLS
   certificate rule (`xpack.uptime.alerts.tlsCertificate`, 14-day threshold) plus an
   SSL handshake error threshold rule in `infrastructure/alerting-rules/all-rules.ndjson`.
   The TLS rule requires a Heartbeat/Synthetics monitor on the ingress endpoint.
4. **emailservice** (Ruby) not instrumented — async email send span still appears only
   as an auto-instrumented gRPC child of `charge-and-ship`. Fix: add
   `opentelemetry-ruby` for custom send-status span events.
5. **Elastic ML anomaly detection** on error rates requires Platinum license — the
   Service Health dashboard uses a threshold reference line (5%) on the error-rate
   trend and threshold-based alerting as the fallback.

## Addendum: Dashboard packaging

Each dashboard NDJSON is exported **self-contained** — it bundles the data-view
(index-pattern) saved objects it references (`traces-apm-default`, `metrics-apm-default`,
`traces-apm-rum-default`, `logs-network-flow`, `logs-nginx-access`) and links every panel
to its visualization via the dashboard `references` array. This makes re-import
idempotent (`?overwrite=true`) with no manual data-view creation. A fourth dashboard,
`infrastructure.ndjson`, covers network-security denied connections (3.3) and NGINX
load-balancer vs backend APM latency correlation (3.4).
