// OpenTelemetry instrumentation for cartservice (C# / .NET)
// Apply to src/cartservice/src/Startup.cs and CartService.cs
//
// Instrumentation coverage:
//   - .NET auto-instrumentation via OpenTelemetry.Instrumentation.AspNetCore
//   - Redis client tracing via StackExchange.Redis instrumentation
//   - Custom spans: validate-cart-contents, calculate-shipping-cost
//   - Custom metric: cart.items.added (counter), cart.items.removed (counter)

using System.Diagnostics;
using OpenTelemetry;
using OpenTelemetry.Exporter;
using OpenTelemetry.Metrics;
using OpenTelemetry.Resources;
using OpenTelemetry.Trace;

namespace cartservice;

public static class TelemetryConfig
{
    public const string ServiceName = "cartservice";
    public const string ServiceVersion = "v0.3.9";
    public static readonly ActivitySource ActivitySource = new(ServiceName);

    public static IServiceCollection AddCartTelemetry(this IServiceCollection services)
    {
        var otlpEndpoint = Environment.GetEnvironmentVariable("OTEL_EXPORTER_OTLP_ENDPOINT")
            ?? "http://otel-agent-collector-agent.observability.svc.cluster.local:4317";

        services.AddOpenTelemetry()
            .ConfigureResource(resource => resource
                .AddService(ServiceName, serviceVersion: ServiceVersion)
                .AddAttributes(new Dictionary<string, object>
                {
                    ["deployment.environment"] = "assessment",
                    ["service.namespace"] = "boutique",
                }))
            .WithTracing(tracing => tracing
                .AddSource(ServiceName)
                .AddAspNetCoreInstrumentation(options =>
                {
                    options.RecordException = true;
                    options.EnrichWithHttpRequest = (activity, request) =>
                    {
                        activity.SetTag("user.session_id",
                            request.Cookies["shop_session-id"] ?? "anonymous");
                    };
                })
                .AddGrpcClientInstrumentation()
                .AddRedisInstrumentation()
                .AddOtlpExporter(options =>
                {
                    options.Endpoint = new Uri(otlpEndpoint);
                    options.Protocol = OtlpExportProtocol.Grpc;
                }))
            .WithMetrics(metrics => metrics
                .AddAspNetCoreInstrumentation()
                .AddRuntimeInstrumentation()
                .AddMeter("cartservice.business")
                .AddOtlpExporter(options =>
                {
                    options.Endpoint = new Uri(otlpEndpoint);
                    options.Protocol = OtlpExportProtocol.Grpc;
                }));

        return services;
    }
}

// Business metrics meter
public class CartMetrics
{
    private readonly Counter<long> _itemsAdded;
    private readonly Counter<long> _itemsRemoved;

    public CartMetrics(IMeterFactory meterFactory)
    {
        var meter = meterFactory.Create("cartservice.business");
        _itemsAdded = meter.CreateCounter<long>("cart.items.added",
            description: "Number of items added to carts");
        _itemsRemoved = meter.CreateCounter<long>("cart.items.removed",
            description: "Number of items removed from carts");
    }

    public void RecordItemAdded(string userId, string productId, int quantity)
    {
        _itemsAdded.Add(quantity,
            new KeyValuePair<string, object?>("user.id", userId),
            new KeyValuePair<string, object?>("product.id", productId));
    }

    public void RecordItemRemoved(string userId, string productId, int quantity)
    {
        _itemsRemoved.Add(quantity,
            new KeyValuePair<string, object?>("user.id", userId),
            new KeyValuePair<string, object?>("product.id", productId));
    }
}

// Instrumented cart operations with custom spans
public class InstrumentedCartService
{
    private readonly CartStore _store;
    private readonly CartMetrics _metrics;

    public InstrumentedCartService(CartStore store, CartMetrics metrics)
    {
        _store = store;
        _metrics = metrics;
    }

    public async Task<Cart> AddItemAsync(string userId, string productId, int quantity)
    {
        using var activity = TelemetryConfig.ActivitySource.StartActivity("validate-cart-contents");
        activity?.SetTag("user.id", userId);
        activity?.SetTag("product.id", productId);
        activity?.SetTag("product.count", quantity);
        activity?.SetTag("transaction.type", "cart");

        if (quantity <= 0)
        {
            activity?.SetStatus(ActivityStatusCode.Error, "invalid quantity");
            activity?.RecordException(new ArgumentException("quantity must be positive"));
            throw new ArgumentException("quantity must be positive");
        }

        activity?.AddEvent(new ActivityEvent("cart.validation.passed"));

        var cart = await _store.AddItemAsync(userId, productId, quantity);
        _metrics.RecordItemAdded(userId, productId, quantity);

        activity?.SetTag("cart.total_items", cart.Items.Count);
        return cart;
    }

    public async Task<decimal> CalculateShippingCostAsync(string userId, string countryCode)
    {
        using var activity = TelemetryConfig.ActivitySource.StartActivity("calculate-shipping-cost");
        activity?.SetTag("user.id", userId);
        activity?.SetTag("shipping.country", countryCode);

        // Business logic: flat rate + country multiplier
        var baseCost = 5.99m;
        var multiplier = countryCode switch
        {
            "US" => 1.0m,
            "CA" => 1.2m,
            "GB" => 1.5m,
            _ => 2.0m,
        };
        var cost = baseCost * multiplier;

        activity?.SetTag("order.shipping_cost", (double)cost);
        activity?.AddEvent(new ActivityEvent("shipping.calculated",
            tags: new ActivityTagsCollection
            {
                { "shipping.rate", (double)cost },
            }));

        return cost;
    }
}

// Startup.cs integration:
//
// public void ConfigureServices(IServiceCollection services)
// {
//     services.AddCartTelemetry();
//     services.AddSingleton<CartMetrics>();
//     services.AddSingleton<InstrumentedCartService>();
//     // ... existing registrations
// }
