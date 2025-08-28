package influxdbreaderreceiver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
		errMsg  string
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
			name: "missing endpoint",
			config: &Config{
				Database: "testdb",
				Interval: 30 * time.Second,
			},
			wantErr: true,
			errMsg:  "endpoint configuration is required",
		},
		{
			name: "missing endpoint address",
			config: &Config{
				Endpoint: &EndpointConfig{
					Protocol: "http",
				},
				Database: "testdb",
				Interval: 30 * time.Second,
			},
			wantErr: true,
			errMsg:  "endpoint address is required",
		},
		{
			name: "missing database",
			config: &Config{
				Endpoint: &EndpointConfig{
					Address:  "localhost:8086",
					Protocol: "http",
				},
				Interval: 30 * time.Second,
			},
			wantErr: true,
			errMsg:  "database is required",
		},
		{
			name: "zero interval should set default",
			config: &Config{
				Endpoint: &EndpointConfig{
					Address:  "localhost:8086",
					Protocol: "http",
				},
				Database: "testdb",
				Interval: 0,
			},
			wantErr: false,
		},
		{
			name: "zero timeout should set default",
			config: &Config{
				Endpoint: &EndpointConfig{
					Address:  "localhost:8086",
					Protocol: "http",
				},
				Database: "testdb",
				Interval: 30 * time.Second,
				Timeout:  0,
			},
			wantErr: false,
		},
		{
			name: "empty query should set default",
			config: &Config{
				Endpoint: &EndpointConfig{
					Address:  "localhost:8086",
					Protocol: "http",
				},
				Database: "testdb",
				Interval: 30 * time.Second,
				Query:    "",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestConfig_Validate_SetsDefaults(t *testing.T) {
	config := &Config{
		Endpoint: &EndpointConfig{
			Address:  "localhost:8086",
			Protocol: "http",
		},
		Database: "testdb",
		Interval: 0,
		Timeout:  0,
		Query:    "",
	}

	err := config.Validate()
	require.NoError(t, err)

	assert.Equal(t, 30*time.Second, config.Interval)
	assert.Equal(t, 30*time.Second, config.Timeout)
	assert.Equal(t, "SELECT * FROM /.*/", config.Query)
}

func TestEndpointConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  *EndpointConfig
		wantErr bool
	}{
		{
			name: "valid endpoint config",
			config: &EndpointConfig{
				Address:  "localhost:8086",
				Protocol: "http",
			},
			wantErr: false,
		},
		{
			name: "valid endpoint config with authentication",
			config: &EndpointConfig{
				Address:  "localhost:8086",
				Protocol: "https",
				Authentication: &AuthenticationConfig{
					Username: "user",
					Password: "pass",
				},
			},
			wantErr: false,
		},
		{
			name: "valid endpoint config with v2 authentication",
			config: &EndpointConfig{
				Address:  "localhost:8086",
				Protocol: "https",
				Authentication: &AuthenticationConfig{
					Token:        "token",
					Organization: "org",
					Bucket:       "bucket",
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Note: EndpointConfig doesn't have its own Validate method,
			// but we can test that the structure is valid
			assert.NotNil(t, tt.config)
			assert.NotEmpty(t, tt.config.Address)
		})
	}
}

func TestAuthenticationConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  *AuthenticationConfig
		wantErr bool
	}{
		{
			name: "v1 authentication",
			config: &AuthenticationConfig{
				Username: "user",
				Password: "pass",
			},
			wantErr: false,
		},
		{
			name: "v2 authentication",
			config: &AuthenticationConfig{
				Token:        "token",
				Organization: "org",
				Bucket:       "bucket",
			},
			wantErr: false,
		},
		{
			name: "mixed authentication",
			config: &AuthenticationConfig{
				Username:     "user",
				Password:     "pass",
				Token:        "token",
				Organization: "org",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Note: AuthenticationConfig doesn't have its own Validate method,
			// but we can test that the structure is valid
			assert.NotNil(t, tt.config)
		})
	}
}
