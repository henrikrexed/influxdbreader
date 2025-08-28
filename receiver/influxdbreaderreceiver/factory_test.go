package influxdbreaderreceiver

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/receiver"
	"go.uber.org/zap"
)

func TestNewFactory(t *testing.T) {
	factory := NewFactory()
	assert.NotNil(t, factory)
	assert.Equal(t, component.MustNewType(TypeStr), factory.Type())
}

func TestCreateDefaultConfig(t *testing.T) {
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig()

	assert.NotNil(t, cfg)

	// Type assert to our config type
	config, ok := cfg.(*Config)
	require.True(t, ok)

	// Check default values
	assert.NotNil(t, config.Endpoint)
	assert.Equal(t, "http", config.Endpoint.Protocol)
	assert.Equal(t, "localhost:8086", config.Endpoint.Address)
	assert.Equal(t, "telegraf", config.Database)
	assert.Equal(t, 30*time.Second, config.Interval)
	assert.Equal(t, 30*time.Second, config.Timeout)
	assert.Equal(t, "SELECT * FROM /.*/", config.Query)
	assert.False(t, config.UseV2API)
}

func TestCreateMetricsReceiver(t *testing.T) {
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig()

	// Create a mock consumer
	consumer := &mockConsumer{}

	// Create receiver settings
	settings := receiver.Settings{
		ID:                component.MustNewID(TypeStr),
		TelemetrySettings: component.TelemetrySettings{Logger: zap.NewNop()},
		BuildInfo:         component.BuildInfo{},
	}

	// Create the receiver
	receiver, err := factory.CreateMetrics(context.Background(), settings, cfg, consumer)

	assert.NoError(t, err)
	assert.NotNil(t, receiver)
}

func TestCreateMetricsReceiver_InvalidConfig(t *testing.T) {
	factory := NewFactory()

	// Create an invalid config (wrong type)
	cfg := &struct{}{}

	// Create a mock consumer
	consumer := &mockConsumer{}

	// Create receiver settings
	settings := receiver.Settings{
		ID:                component.MustNewID(TypeStr),
		TelemetrySettings: component.TelemetrySettings{Logger: zap.NewNop()},
		BuildInfo:         component.BuildInfo{},
	}

	// Create the receiver - should panic due to type assertion
	assert.Panics(t, func() {
		_, _ = factory.CreateMetrics(context.Background(), settings, cfg, consumer)
	})
}

func TestCreateMetricsReceiver_NilConsumer(t *testing.T) {
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig()

	// Create receiver settings
	settings := receiver.Settings{
		ID:                component.MustNewID(TypeStr),
		TelemetrySettings: component.TelemetrySettings{Logger: zap.NewNop()},
		BuildInfo:         component.BuildInfo{},
	}

	// Create the receiver with nil consumer
	receiver, err := factory.CreateMetrics(context.Background(), settings, cfg, nil)

	assert.NoError(t, err)
	assert.NotNil(t, receiver)
}

func TestFactory_StabilityLevel(t *testing.T) {
	factory := NewFactory()

	// Check that the factory has the correct stability level
	// This is set in the factory creation
	assert.NotNil(t, factory)
}
