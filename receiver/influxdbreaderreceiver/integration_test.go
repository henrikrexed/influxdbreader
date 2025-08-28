package influxdbreaderreceiver

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.uber.org/zap"
)

// TestIntegration_WithRealInfluxDB is an integration test that requires a real InfluxDB instance
// This test is skipped by default and should only be run when explicitly testing with a real InfluxDB
func TestIntegration_WithRealInfluxDB(t *testing.T) {
	t.Skip("Skipping integration test - requires real InfluxDB instance")

	// This test would require a real InfluxDB instance running
	// You can enable it by setting an environment variable or removing the Skip

	config := &Config{
		Endpoint: &EndpointConfig{
			Address:  "localhost:8086",
			Protocol: "http",
			Authentication: &AuthenticationConfig{
				Username: "admin",
				Password: "password",
			},
		},
		Database: "testdb",
		Interval: 1 * time.Second,
		Timeout:  5 * time.Second,
	}

	consumer := &consumertest.MetricsSink{}
	logger := zap.NewNop()

	receiver := newInfluxDBReaderReceiver(config, consumer, logger)

	// Start the receiver
	ctx := context.Background()
	host := &mockHost{}

	err := receiver.Start(ctx, host)
	if err != nil {
		t.Logf("Failed to start receiver (expected if InfluxDB not running): %v", err)
		return
	}

	// Wait a bit for metrics to be collected
	time.Sleep(2 * time.Second)

	// Shutdown
	err = receiver.Shutdown(ctx)
	assert.NoError(t, err)

	// Check if any metrics were collected
	metrics := consumer.AllMetrics()
	t.Logf("Collected %d metric batches", len(metrics))
}

// TestIntegration_ConfigValidation tests configuration validation in a more realistic scenario
func TestIntegration_ConfigValidation(t *testing.T) {
	tests := []struct {
		name        string
		config      *Config
		expectValid bool
	}{
		{
			name: "valid minimal config",
			config: &Config{
				Endpoint: &EndpointConfig{
					Address:  "localhost:8086",
					Protocol: "http",
				},
				Database: "testdb",
			},
			expectValid: true,
		},
		{
			name: "valid config with v1 auth",
			config: &Config{
				Endpoint: &EndpointConfig{
					Address:  "localhost:8086",
					Protocol: "https",
					Authentication: &AuthenticationConfig{
						Username: "user",
						Password: "pass",
					},
				},
				Database: "testdb",
				Interval: 10 * time.Second,
			},
			expectValid: true,
		},
		{
			name: "valid config with v2 auth",
			config: &Config{
				Endpoint: &EndpointConfig{
					Address:  "localhost:8086",
					Protocol: "https",
					Authentication: &AuthenticationConfig{
						Token:        "token",
						Organization: "org",
						Bucket:       "bucket",
					},
				},
				Database: "testdb",
				UseV2API: true,
			},
			expectValid: true,
		},
		{
			name: "invalid config - missing endpoint",
			config: &Config{
				Database: "testdb",
			},
			expectValid: false,
		},
		{
			name: "invalid config - missing database",
			config: &Config{
				Endpoint: &EndpointConfig{
					Address:  "localhost:8086",
					Protocol: "http",
				},
			},
			expectValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectValid {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

// TestIntegration_ReceiverLifecycle tests the complete lifecycle of the receiver
func TestIntegration_ReceiverLifecycle(t *testing.T) {
	config := &Config{
		Endpoint: &EndpointConfig{
			Address:  "localhost:8086",
			Protocol: "http",
		},
		Database: "testdb",
		Interval: 100 * time.Millisecond, // Short interval for testing
		Timeout:  1 * time.Second,
	}

	consumer := &consumertest.MetricsSink{}
	logger := zap.NewNop()

	receiver := newInfluxDBReaderReceiver(config, consumer, logger)

	// Test that the receiver is created correctly
	assert.NotNil(t, receiver)

	// Test start (will fail due to no InfluxDB, but that's expected)
	ctx := context.Background()
	host := &mockHost{}

	err := receiver.Start(ctx, host)
	assert.Error(t, err) // Expected to fail due to no InfluxDB connection

	// Test shutdown
	err = receiver.Shutdown(ctx)
	assert.NoError(t, err)
}

// TestIntegration_ConfigDefaults tests that default values are properly set
func TestIntegration_ConfigDefaults(t *testing.T) {
	config := &Config{
		Endpoint: &EndpointConfig{
			Address:  "localhost:8086",
			Protocol: "http",
		},
		Database: "testdb",
		// Don't set defaults to test that they're applied
	}

	err := config.Validate()
	require.NoError(t, err)

	// Verify defaults are set
	assert.Equal(t, 30*time.Second, config.Interval)
	assert.Equal(t, 30*time.Second, config.Timeout)
	assert.Equal(t, "SELECT * FROM /.*/", config.Query)
	assert.False(t, config.UseV2API)
}

// TestIntegration_AuthenticationConfigs tests different authentication configurations
func TestIntegration_AuthenticationConfigs(t *testing.T) {
	tests := []struct {
		name     string
		config   *Config
		hasAuth  bool
		authType string
	}{
		{
			name: "no authentication",
			config: &Config{
				Endpoint: &EndpointConfig{
					Address:  "localhost:8086",
					Protocol: "http",
				},
				Database: "testdb",
			},
			hasAuth:  false,
			authType: "none",
		},
		{
			name: "v1 authentication",
			config: &Config{
				Endpoint: &EndpointConfig{
					Address:  "localhost:8086",
					Protocol: "http",
					Authentication: &AuthenticationConfig{
						Username: "user",
						Password: "pass",
					},
				},
				Database: "testdb",
			},
			hasAuth:  true,
			authType: "v1",
		},
		{
			name: "v2 authentication",
			config: &Config{
				Endpoint: &EndpointConfig{
					Address:  "localhost:8086",
					Protocol: "https",
					Authentication: &AuthenticationConfig{
						Token:        "token",
						Organization: "org",
						Bucket:       "bucket",
					},
				},
				Database: "testdb",
				UseV2API: true,
			},
			hasAuth:  true,
			authType: "v2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			require.NoError(t, err)

			if tt.hasAuth {
				assert.NotNil(t, tt.config.Endpoint.Authentication)
				if tt.authType == "v1" {
					assert.NotEmpty(t, tt.config.Endpoint.Authentication.Username)
					assert.NotEmpty(t, tt.config.Endpoint.Authentication.Password)
				} else if tt.authType == "v2" {
					assert.NotEmpty(t, tt.config.Endpoint.Authentication.Token)
					assert.NotEmpty(t, tt.config.Endpoint.Authentication.Organization)
				}
			} else {
				assert.Nil(t, tt.config.Endpoint.Authentication)
			}
		})
	}
}
