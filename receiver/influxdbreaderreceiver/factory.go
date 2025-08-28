package influxdbreaderreceiver

import (
	"context"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
)

const (
	// TypeStr is the type of the receiver
	TypeStr = "influxdbreader"
)

// NewFactory creates a new factory for the InfluxDB reader receiver
func NewFactory() receiver.Factory {
	return receiver.NewFactory(
		component.MustNewType(TypeStr),
		createDefaultConfig,
		receiver.WithMetrics(createMetricsReceiver, component.StabilityLevelAlpha),
	)
}

// createDefaultConfig creates the default configuration for the receiver
func createDefaultConfig() component.Config {
	return &Config{
		Endpoint: &EndpointConfig{
			Protocol: "http",
			Address:  "localhost:8086",
		},
		Database:          "telegraf",
		Interval:          30 * time.Second,
		Timeout:           30 * time.Second,
		Query:             "SELECT * FROM /.*/",
		AutoDiscover:      true, // Enable auto-discovery by default
		UseV2API:          false,
		MetricTypeMapping: createDefaultMetricTypeMapping(),
	}
}

// createMetricsReceiver creates a new metrics receiver
func createMetricsReceiver(
	ctx context.Context,
	set receiver.Settings,
	cfg component.Config,
	consumer consumer.Metrics,
) (receiver.Metrics, error) {
	config := cfg.(*Config)
	return newInfluxDBReaderReceiver(config, consumer, set.TelemetrySettings), nil
}
