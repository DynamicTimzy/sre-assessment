// OpenTelemetry instrumentation for frontend service (Go)
// Apply as a patch to src/frontend/main.go or integrate into handlers.go
//
// Instrumentation coverage:
//   - HTTP middleware tracing (auto via otelhttp)
//   - Template rendering custom spans
//   - gRPC client calls to downstream services
//   - Custom business spans: render-product-page, validate-session
//   - Custom metric: frontend.page.views (counter)

package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
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
	serviceName    = "frontend"
	serviceVersion = "v0.3.9"
)

var (
	tracer         = otel.Tracer(serviceName)
	pageViewCounter metric.Int64Counter
)

func initTelemetry(ctx context.Context) (func(context.Context) error, error) {
	// OTLP endpoint: DaemonSet agent on local node via hostIP
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
		resource.WithOS(),
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
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(meterProvider)

	meter := meterProvider.Meter(serviceName)
	pageViewCounter, err = meter.Int64Counter("frontend.page.views",
		metric.WithDescription("Number of page views by route"),
		metric.WithUnit("{page}"),
	)
	if err != nil {
		return nil, fmt.Errorf("counter: %w", err)
	}

	return tp.Shutdown, nil
}

// renderProductPage wraps template rendering with a custom span
func renderProductPage(ctx context.Context, w http.ResponseWriter, r *http.Request, productID string) error {
	ctx, span := tracer.Start(ctx, "render-product-page",
		trace.WithAttributes(
			attribute.String("product.id", productID),
			attribute.String("page.route", "/product/"+productID),
			attribute.String("user.session_id", getSessionID(r)),
		),
	)
	defer span.End()

	start := time.Now()

	// Simulate template rendering work
	tmplCtx, tmplSpan := tracer.Start(ctx, "render-template",
		trace.WithAttributes(
			attribute.String("template.name", "product.html"),
			attribute.Int("template.cache_hit", 1),
		),
	)
	_ = tmplCtx
	tmplSpan.End()

	pageViewCounter.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String("page.route", "/product"),
			attribute.String("product.id", productID),
		),
	)

	span.SetAttributes(
		attribute.Int64("render.duration_ms", time.Since(start).Milliseconds()),
		attribute.Int("product.count", 1),
	)

	return nil
}

// validateSession creates a custom span for session validation business logic
func validateSession(ctx context.Context, r *http.Request) (string, error) {
	ctx, span := tracer.Start(ctx, "validate-session",
		trace.WithAttributes(
			attribute.String("user.session_id", getSessionID(r)),
			attribute.String("http.user_agent", r.UserAgent()),
		),
	)
	defer span.End()

	sessionID := getSessionID(r)
	if sessionID == "" {
		span.RecordError(fmt.Errorf("missing session cookie"))
		span.SetStatus(1, "session not found")
		return "", fmt.Errorf("no session")
	}

	span.AddEvent("session.validated", trace.WithAttributes(
		attribute.Bool("session.valid", true),
	))
	return sessionID, nil
}

func getSessionID(r *http.Request) string {
	cookie, err := r.Cookie("shop_session-id")
	if err != nil {
		return ""
	}
	return cookie.Value
}

// newGRPCConn creates a gRPC connection with OTel client interceptors for context propagation
func newGRPCConn(target string) (*grpc.ClientConn, error) {
	return grpc.Dial(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
}

// instrumentedHandler wraps the HTTP handler with OTel middleware
func instrumentedHandler(handler http.Handler) http.Handler {
	return otelhttp.NewHandler(handler, "frontend",
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return fmt.Sprintf("%s %s", r.Method, r.URL.Path)
		}),
	)
}

// Example integration in main():
//
// func main() {
//     ctx := context.Background()
//     shutdown, err := initTelemetry(ctx)
//     if err != nil { log.Fatal(err) }
//     defer shutdown(ctx)
//
//     mux := http.NewServeMux()
//     mux.HandleFunc("/product/{id}", productHandler)
//     log.Fatal(http.ListenAndServe(":8080", instrumentedHandler(mux)))
// }
