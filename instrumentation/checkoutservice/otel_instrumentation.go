// OpenTelemetry instrumentation for checkoutservice (Go)
// Apply to src/checkoutservice/main.go
//
// checkoutservice is the orchestrator of the checkout flow. Instrumenting it is
// essential: without it, the distributed trace shows a gap between frontend and
// paymentservice/emailservice/shippingservice (see docs/DECISIONS.md open items).
//
// Instrumentation coverage:
//   - gRPC server interceptors (auto span per PlaceOrder RPC)
//   - gRPC client stats handlers for all downstream calls (cart, catalog,
//     currency, shipping, payment, email) -> W3C context propagation
//   - Custom business spans: prepare-order-items, charge-and-ship
//   - Custom span attributes: order.id, order.total, order.item_count, user.id
//   - Custom metric: checkout.orders.placed (counter), checkout.order.value (histogram)

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	serviceName    = "checkoutservice"
	serviceVersion = "v0.3.9"
)

var (
	tracer        = otel.Tracer(serviceName)
	ordersCounter metric.Int64Counter
	orderValueHist metric.Float64Histogram
)

func initTelemetry(ctx context.Context) (func(context.Context) error, error) {
	otlpEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if otlpEndpoint == "" {
		hostIP := os.Getenv("HOST_IP")
		if hostIP == "" {
			hostIP = "otel-agent-collector-agent.observability.svc.cluster.local"
		}
		otlpEndpoint = fmt.Sprintf("%s:4317", hostIP)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
			semconv.DeploymentEnvironment("assessment"),
		),
		resource.WithFromEnv(),
		resource.WithProcess(),
		resource.WithContainer(),
	)
	if err != nil {
		return nil, fmt.Errorf("resource: %w", err)
	}

	traceExporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(otlpEndpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("trace exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	metricExporter, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint(otlpEndpoint),
		otlpmetricgrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("metric exporter: %w", err)
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter,
			sdkmetric.WithInterval(15*time.Second),
		)),
	)
	otel.SetMeterProvider(mp)

	meter := mp.Meter(serviceName)
	ordersCounter, err = meter.Int64Counter("checkout.orders.placed",
		metric.WithDescription("Number of orders successfully placed"),
		metric.WithUnit("{order}"),
	)
	if err != nil {
		return nil, fmt.Errorf("orders counter: %w", err)
	}
	orderValueHist, err = meter.Float64Histogram("checkout.order.value",
		metric.WithDescription("Distribution of completed order totals"),
		metric.WithUnit("USD"),
	)
	if err != nil {
		return nil, fmt.Errorf("order value histogram: %w", err)
	}

	shutdown := func(ctx context.Context) error {
		var errs error
		if err := tp.Shutdown(ctx); err != nil {
			errs = fmt.Errorf("tracer provider shutdown: %w", err)
		}
		if err := mp.Shutdown(ctx); err != nil {
			errs = fmt.Errorf("meter provider shutdown: %w", err)
		}
		return errs
	}
	return shutdown, nil
}

// grpcServerOptions returns server options with OTel interceptors so every
// inbound PlaceOrder RPC starts (or continues) a trace using the incoming
// traceparent header propagated from the frontend.
func grpcServerOptions() []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
	}
}

// newDownstreamConn dials a downstream service with client-side OTel stats
// handler, ensuring the current trace context is injected into outbound calls.
func newDownstreamConn(target string) (*grpc.ClientConn, error) {
	return grpc.Dial(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
}

// prepareOrderItems wraps the fan-out that resolves cart contents, product
// details, shipping quote, and currency conversion into a single business span.
func prepareOrderItems(ctx context.Context, userID, userCurrency string, itemCount int) (context.Context, trace.Span) {
	ctx, span := tracer.Start(ctx, "prepare-order-items",
		trace.WithAttributes(
			attribute.String("user.id", userID),
			attribute.String("order.currency", userCurrency),
			attribute.Int("order.item_count", itemCount),
			attribute.String("transaction.type", "checkout"),
		),
	)
	return ctx, span
}

// chargeAndShip wraps the payment + shipping + email fan-out and records the
// order-level business metrics on success.
func chargeAndShip(ctx context.Context, orderID, userID string, orderTotal float64, currency string, do func(context.Context) error) error {
	ctx, span := tracer.Start(ctx, "charge-and-ship",
		trace.WithAttributes(
			attribute.String("order.id", orderID),
			attribute.String("user.id", userID),
			attribute.Float64("order.total", orderTotal),
			attribute.String("order.currency", currency),
			attribute.String("transaction.type", "checkout"),
		),
	)
	defer span.End()

	if err := do(ctx); err != nil {
		span.RecordError(err)
		span.SetStatus(2, err.Error()) // codes.Error
		ordersCounter.Add(ctx, 1, metric.WithAttributes(
			attribute.String("order.status", "failed"),
			attribute.String("order.currency", currency),
		))
		return err
	}

	span.AddEvent("order.placed", trace.WithAttributes(
		attribute.String("order.id", orderID),
	))
	ordersCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("order.status", "placed"),
		attribute.String("order.currency", currency),
	))
	orderValueHist.Record(ctx, orderTotal, metric.WithAttributes(
		attribute.String("order.currency", currency),
	))
	return nil
}

// Example integration in main():
//
// func main() {
//     ctx := context.Background()
//     shutdown, err := initTelemetry(ctx)
//     if err != nil { log.Fatal(err) }
//     defer shutdown(ctx)
//
//     srv := grpc.NewServer(grpcServerOptions()...)
//     // register checkoutservice, dial downstreams with newDownstreamConn(...)
// }
