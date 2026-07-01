# Distributed Trace Validation Report

## Test Procedure

1. Deploy instrumented services (frontend, cartservice, paymentservice)
2. Deploy OTel Collector gateway + agent topology
3. Run `scripts/checkout-flow.sh https://<ingress-host>`
4. Wait 30–60 seconds for trace ingestion

## Expected Trace Waterfall

```
[browser/rum] page-load or click "Checkout"
  └── frontend          POST /cart/checkout          (~50ms)
        └── cartservice   GetCart / AddItem          (~15ms)
              └── checkoutservice  PlaceOrder        (~200ms)
                    ├── shippingservice  GetQuote     (~30ms)
                    ├── currencyservice  Convert      (~10ms)
                    ├── emailservice     SendOrder    (~40ms)
                    └── paymentservice   Charge       (~80ms)
                          ├── validate-payment-details  (custom span)
                          └── process-charge            (custom span)
```

## W3C Trace Context Propagation

Trace context propagates via the `traceparent` header across all gRPC and HTTP boundaries.
The OTel SDKs and auto-instrumentation libraries inject/extract `traceparent` and `tracestate`
headers automatically when `OTEL_PROPAGATORS=tracecontext,baggage` is set.

Verify propagation with:
```bash
curl -v https://<frontend>/product/OLJCESPC7Z 2>&1 | grep -i traceparent
# Expected: traceparent: 00-<trace-id>-<span-id>-01
```

## Known Gaps and Fixes

| Gap | Cause | Fix |
|-----|-------|-----|
| checkoutservice missing custom spans | Not instrumented in this assessment | Add OTel Go instrumentation to checkoutservice |
| emailservice not in trace | Ruby service not instrumented | Add elastic-apm-agent or OTel Ruby SDK |
| Redis operations invisible | StackExchange.Redis instrumentation requires explicit connection registration | Call `services.AddRedisInstrumentation()` with connection multiplexer |
| Async email send breaks parent span | Fire-and-forget goroutine loses context | Pass `ctx` to async operations or use `trace.ContextWithSpan` |

## Error Span Validation

The checkout-flow script step 6 submits an expired card to trigger a payment failure.

In Kibana APM:
1. Navigate to **Observability → APM → Traces**
2. Filter: `service.name: paymentservice AND transaction.result: failure`
3. Open the trace → select the `validate-payment-details` or `process-charge` span
4. **Error tab** should show:
   - Exception message: `Card expired` or `Invalid card number`
   - Stack trace with file/line from `telemetry.js`
   - Span status: `ERROR`

## Service Map Verification

Navigate to **APM → Services → frontend → Service Map**.

Expected connections:
- frontend → cartservice
- frontend → checkoutservice (via checkout handler)
- checkoutservice → paymentservice
- checkoutservice → shippingservice

## Custom Attributes Verification

In any checkout trace, verify these metadata labels appear on spans:

| Service | Attribute | Example Value |
|---------|-----------|---------------|
| frontend | `user.session_id` | `test-session-1234` |
| frontend | `page.route` | `/product/OLJCESPC7Z` |
| cartservice | `user.id` | session user ID |
| cartservice | `cart.total_items` | `1` |
| paymentservice | `order.total` | `15.99` |
| paymentservice | `payment.card.last4` | `0008` |

## Custom Metrics Verification

In **Kibana → Observability → Metrics Explorer**:

| Metric | Service | Query |
|--------|---------|-------|
| `frontend.page.views` | frontend | `metric.name: "frontend.page.views"` |
| `cart.items.added` | cartservice | `metric.name: "cart.items.added"` |
| `payment.charges.total` | paymentservice | `metric.name: "payment.charges.total"` |
| `payment.charge.amount` | paymentservice | `metric.name: "payment.charge.amount"` |
