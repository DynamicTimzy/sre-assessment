/**
 * OpenTelemetry instrumentation for paymentservice (Node.js)
 *
 * Instrumentation coverage:
 *   - Node.js auto-instrumentation (HTTP, gRPC)
 *   - Custom spans: validate-payment-details, process-charge
 *   - Error recording with span status and exception details
 *   - Custom metric: payment.charges.total (counter), payment.charge.amount (histogram)
 */

const { NodeSDK } = require('@opentelemetry/sdk-node');
const { getNodeAutoInstrumentations } = require('@opentelemetry/auto-instrumentations-node');
const { OTLPTraceExporter } = require('@opentelemetry/exporter-trace-otlp-grpc');
const { OTLPMetricExporter } = require('@opentelemetry/exporter-metrics-otlp-grpc');
const { PeriodicExportingMetricReader } = require('@opentelemetry/sdk-metrics');
const { Resource } = require('@opentelemetry/resources');
const {
  SEMRESATTRS_SERVICE_NAME,
  SEMRESATTRS_SERVICE_VERSION,
  SEMRESATTRS_DEPLOYMENT_ENVIRONMENT,
} = require('@opentelemetry/semantic-conventions');
const { trace, metrics, SpanStatusCode } = require('@opentelemetry/api');

const SERVICE_NAME = 'paymentservice';
const SERVICE_VERSION = 'v0.3.9';

// Resolve OTLP endpoint: DaemonSet agent on local node
const hostIP = process.env.HOST_IP || 'otel-agent-collector-agent.observability.svc.cluster.local';
const otlpEndpoint = process.env.OTEL_EXPORTER_OTLP_ENDPOINT || `http://${hostIP}:4317`;

const resource = new Resource({
  [SEMRESATTRS_SERVICE_NAME]: SERVICE_NAME,
  [SEMRESATTRS_SERVICE_VERSION]: SERVICE_VERSION,
  [SEMRESATTRS_DEPLOYMENT_ENVIRONMENT]: 'assessment',
  'service.namespace': 'boutique',
});

const sdk = new NodeSDK({
  resource,
  traceExporter: new OTLPTraceExporter({ url: otlpEndpoint }),
  metricReader: new PeriodicExportingMetricReader({
    exporter: new OTLPMetricExporter({ url: otlpEndpoint }),
    exportIntervalMillis: 15000,
  }),
  instrumentations: [
    getNodeAutoInstrumentations({
      '@opentelemetry/instrumentation-fs': { enabled: false },
      '@opentelemetry/instrumentation-http': {
        ignoreIncomingRequestHook: (req) => req.url === '/health' || req.url === '/healthz',
      },
      '@opentelemetry/instrumentation-grpc': { enabled: true },
    }),
  ],
});

sdk.start();

const tracer = trace.getTracer(SERVICE_NAME);
const meter = metrics.getMeter(SERVICE_NAME);

// Custom business metrics
const chargeCounter = meter.createCounter('payment.charges.total', {
  description: 'Total number of payment charge attempts',
});
const chargeAmountHistogram = meter.createHistogram('payment.charge.amount', {
  description: 'Distribution of charge amounts in USD',
  unit: 'USD',
});

/**
 * Custom span: validate payment card details
 */
async function validatePaymentDetails(ctx, { cardNumber, cvv, expiryMonth, expiryYear, userId, orderTotal }) {
  return tracer.startActiveSpan('validate-payment-details', {
    attributes: {
      'user.id': userId,
      'order.total': orderTotal,
      'payment.card.last4': cardNumber.slice(-4),
      'transaction.type': 'payment',
    },
  }, async (span) => {
    try {
      if (!cardNumber || cardNumber.length < 13) {
        throw new Error('Invalid card number');
      }
      if (!cvv || cvv.length < 3) {
        throw new Error('Invalid CVV');
      }
      const now = new Date();
      const expiry = new Date(expiryYear, expiryMonth - 1);
      if (expiry < now) {
        throw new Error('Card expired');
      }

      span.addEvent('payment.validation.passed', {
        'payment.method': 'credit_card',
      });

      span.setStatus({ code: SpanStatusCode.OK });
      return { valid: true };
    } catch (err) {
      span.recordException(err);
      span.setStatus({ code: SpanStatusCode.ERROR, message: err.message });
      throw err;
    } finally {
      span.end();
    }
  });
}

/**
 * Custom span: process charge via payment processor
 */
async function processCharge(ctx, { userId, orderId, amount, currency, chargeFn }) {
  return tracer.startActiveSpan('process-charge', {
    attributes: {
      'user.id': userId,
      'order.id': orderId,
      'order.total': amount,
      'payment.currency': currency,
      'transaction.type': 'payment',
    },
  }, async (span) => {
    const startTime = Date.now();
    try {
      const result = await chargeFn();

      chargeCounter.add(1, {
        'payment.status': 'success',
        'payment.currency': currency,
      });
      chargeAmountHistogram.record(amount, {
        'payment.currency': currency,
        'payment.status': 'success',
      });

      span.addEvent('payment.charge.completed', {
        'payment.transaction_id': result.transactionId,
        'payment.duration_ms': Date.now() - startTime,
      });

      span.setAttributes({
        'payment.transaction_id': result.transactionId,
        'payment.status': 'success',
      });
      span.setStatus({ code: SpanStatusCode.OK });
      return result;
    } catch (err) {
      chargeCounter.add(1, {
        'payment.status': 'failure',
        'payment.currency': currency,
        'error.type': err.name,
      });

      span.recordException(err);
      span.setStatus({ code: SpanStatusCode.ERROR, message: err.message });
      span.setAttributes({
        'payment.status': 'failure',
        'error.message': err.message,
      });
      throw err;
    } finally {
      span.end();
    }
  });
}

// Graceful shutdown
process.on('SIGTERM', () => {
  sdk.shutdown()
    .then(() => console.log('OTel SDK shut down'))
    .catch((err) => console.error('OTel shutdown error', err))
    .finally(() => process.exit(0));
});

module.exports = { validatePaymentDetails, processCharge, tracer, meter };
