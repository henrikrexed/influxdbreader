package influxdbreaderreceiver

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestNewInfluxDBReaderReceiver(t *testing.T) {
	config := &Config{
		Endpoint: &EndpointConfig{
			Address:  "localhost:8086",
			Protocol: "http",
		},
		Database: "testdb",
		Interval: 30 * time.Second,
	}

	consumer := &mockConsumer{}
	logger := zap.NewNop()

	receiver := newInfluxDBReaderReceiver(config, consumer, logger)

	assert.NotNil(t, receiver)

	// Test that the receiver is not nil
	assert.NotNil(t, receiver)
}

func TestInfluxDBReaderReceiver_Start_Shutdown(t *testing.T) {
	config := &Config{
		Endpoint: &EndpointConfig{
			Address:  "localhost:8086",
			Protocol: "http",
		},
		Database: "testdb",
		Interval: 100 * time.Millisecond, // Short interval for testing
		UseV2API: false,
	}

	consumer := &mockConsumer{}
	logger := zap.NewNop()

	receiver := newInfluxDBReaderReceiver(config, consumer, logger)

	// Mock the host
	host := &mockHost{}

	// Start the receiver
	ctx := context.Background()
	err := receiver.Start(ctx, host)

	// Should fail because we can't connect to InfluxDB in tests
	// but the receiver should be created successfully
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to initialize InfluxDB")

	// Shutdown should work
	err = receiver.Shutdown(ctx)
	assert.NoError(t, err)
}

func TestInfluxDBReaderReceiver_ValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
	}{
		{
			name: "valid config",
			config: &Config{
				Endpoint: &EndpointConfig{
					Address:  "localhost:8086",
					Protocol: "http",
				},
				Database: "testdb",
				Interval: 30 * time.Second,
			},
			wantErr: false,
		},
		{
			name: "invalid config - missing endpoint",
			config: &Config{
				Database: "testdb",
				Interval: 30 * time.Second,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestInfluxDBReaderReceiver_ConfigDefaults(t *testing.T) {
	config := &Config{
		Endpoint: &EndpointConfig{
			Address:  "localhost:8086",
			Protocol: "http",
		},
		Database: "testdb",
		// Don't set Interval, Timeout, or Query to test defaults
	}

	err := config.Validate()
	require.NoError(t, err)

	// Check that defaults are set
	assert.Equal(t, 30*time.Second, config.Interval)
	assert.Equal(t, 30*time.Second, config.Timeout)
	assert.Equal(t, "SELECT * FROM /.*/", config.Query)
}

func TestInfluxDBReaderReceiver_AuthenticationConfig(t *testing.T) {
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
				Interval: 30 * time.Second,
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
				Interval: 30 * time.Second,
			},
			hasAuth:  true,
			authType: "v1",
		},
		{
			name: "v2 authentication",
			config: &Config{
				Endpoint: &EndpointConfig{
					Address:  "localhost:8086",
					Protocol: "http",
					Authentication: &AuthenticationConfig{
						Token:        "token",
						Organization: "org",
						Bucket:       "bucket",
					},
				},
				Database: "testdb",
				Interval: 30 * time.Second,
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

func TestInfluxDBReaderReceiver_ContextCancellation(t *testing.T) {
	config := &Config{
		Endpoint: &EndpointConfig{
			Address:  "localhost:8086",
			Protocol: "http",
		},
		Database: "testdb",
		Interval: 100 * time.Millisecond,
	}

	consumer := &mockConsumer{}
	logger := zap.NewNop()

	receiver := newInfluxDBReaderReceiver(config, consumer, logger)

	// Create a context that will be cancelled
	ctx, cancel := context.WithCancel(context.Background())

	// Start the receiver (will fail due to connection, but that's expected)
	host := &mockHost{}
	_ = receiver.Start(ctx, host)

	// Cancel the context
	cancel()

	// Shutdown should work
	err := receiver.Shutdown(context.Background())
	assert.NoError(t, err)
}
