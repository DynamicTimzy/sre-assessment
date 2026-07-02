# Distributed Trace Validation (Section 1.3)

This document describes how to validate end-to-end distributed tracing and where to
capture the required evidence (screenshots) in Kibana APM.

## 1. Generate a checkout user journey

```bash
FRONTEND_URL=http://boutique.assessment.local \
  ./scripts/generate-checkout-traffic.sh 30
```

The script exercises **browse → set currency → view product → add to cart → view cart
→ checkout** and intentionally submits an invalid card on ~20% of runs to produce
error spans (`ERROR_RATIO` is configurable).

## 2. Locate the full trace waterfall

`Kibana → Observability → APM → Traces` → open a `POST /cart/checkout` transaction.

Expected span hierarchy (root → leaves):

```
frontend: POST /cart/checkout                 (HTTP server span, otelhttp)
└─ checkoutservice: PlaceOrder                 (gRPC server span)
   ├─ prepare-order-items                      (custom business span)
   │  ├─ cartservice: GetCart                  (gRPC → Redis exit spans)
   │  ├─ productcatalogservice: GetProduct
   │  ├─ currencyservice: Convert
   │  └─ shippingservice: GetQuote
   └─ charge-and-ship                          (custom business span)
      ├─ paymentservice: Charge                (Node.js gRPC server span)
      │  ├─ validate-payment-details           (custom span)
      │  └─ process-charge                     (custom span)
      ├─ shippingservice: ShipOrder
      └─ emailservice: SendOrderConfirmation
```

**Screenshot to capture:** the trace waterfall showing all service spans. Save as
`docs/screenshots/trace-waterfall.png`.

## 3. Verify W3C context propagation across ≥3 hops

Context is propagated via the W3C `traceparent` header. It is validated across at
least three hops: **frontend → checkoutservice → paymentservice**.

- All instrumented services set `OTEL_PROPAGATORS=tracecontext,baggage` (see each
  `k8s-patch.yaml`) and use gRPC OTel stats handlers on both client and server side.
- Confirm a single `trace.id` spans frontend, checkoutservice, and paymentservice
  documents in `Discover` (index `traces-apm*`, filter by the trace id).

**Screenshot to capture:** `Kibana → Observability → APM → Service Map` showing the
connected call chain. Save as `docs/screenshots/service-map.png`.

## 4. Known gaps and fixes (status)

| Gap | Status | Fix |
|-----|--------|-----|
| `checkoutservice` uninstrumented → frontend↔payment gap | **Fixed** | `instrumentation/checkoutservice/` added (gRPC server + client handlers, custom spans/metrics) |
| `frontend` custom metrics never exported | **Fixed** | OTLP metric reader attached in `otel_instrumentation.go` |
| `emailservice` (Ruby) async span missing | Open | Add `opentelemetry-ruby` auto-instrumentation; async send appears as a child of `charge-and-ship` |
| `shippingservice` spans | Auto only | Covered by gRPC context propagation from checkoutservice; add custom spans if deeper detail needed |

## 5. Demonstrate error spans

Runs that used the invalid card (`0000-0000-0000-0000`) fail in
`paymentservice.validate-payment-details`, which calls `span.recordException()` and
sets `SpanStatusCode.ERROR`.

1. `Kibana → Observability → APM → Services → paymentservice → Transactions`.
2. Filter `event.outcome: failure` (or open the red/failed transaction).
3. Open the **Errors** tab → select the error → view the **exception stack trace**
   (`error.exception.message = "Invalid card number"`, with type and stacktrace).

**Screenshot to capture:** the error detail with stack trace. Save as
`docs/screenshots/error-span.png`.

> Tail-based sampling guarantees these are retained: the `errors-policy`
> (status_code = ERROR) samples 100% of error traces regardless of the 10%
> probabilistic policy.
