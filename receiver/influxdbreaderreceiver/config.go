package influxdbreaderreceiver

import (
	"errors"
	"fmt"
	"time"
)

// EndpointConfig defines the endpoint configuration
type EndpointConfig struct {
	// Address of the InfluxDB server (host:port)
	Address string `mapstructure:"address"`

	// Protocol to use (http, https, udp)
	Protocol string `mapstructure:"protocol"`

	// Authentication configuration
	Authentication *AuthenticationConfig `mapstructure:"authentication"`
}

// AuthenticationConfig defines authentication settings
type AuthenticationConfig struct {
	// Username for authentication
	Username string `mapstructure:"username"`

	// Password for authentication
	Password string `mapstructure:"password"`

	// Token for InfluxDB 2.x authentication
	Token string `mapstructure:"token"`

	// Organization for InfluxDB 2.x
	Organization string `mapstructure:"organization"`

	// Bucket for InfluxDB 2.x
	Bucket string `mapstructure:"bucket"`
}

// SimplifiedMetricTypeConfig provides a simpler way to configure metric types
type SimplifiedMetricTypeConfig struct {
	// Enable automatic metric type detection based on naming patterns
	AutoDetect bool `mapstructure:"auto_detect"`

	// Override specific measurements to force a metric type
	Overrides map[string]string `mapstructure:"overrides"`

	// Default metric type when auto-detection fails
	DefaultType string `mapstructure:"default_type"`
}

// Config defines configuration for the InfluxDB reader receiver.
type Config struct {
	// Endpoint configuration
	Endpoint *EndpointConfig `mapstructure:"endpoint"`

	// Database name to connect to
	Database string `mapstructure:"database"`

	// Polling interval
	Interval time.Duration `mapstructure:"interval"`

	// Query to execute (optional, if not provided will auto-discover measurements)
	Query string `mapstructure:"query"`

	// Auto-discover measurements and fetch latest metrics (default: true)
	AutoDiscover bool `mapstructure:"auto_discover"`

	// Timeout for HTTP requests
	Timeout time.Duration `mapstructure:"timeout"`

	// Insecure skip TLS verification
	Insecure bool `mapstructure:"insecure"`

	// Use InfluxDB 2.x API (defaults to false for 1.x)
	UseV2API bool `mapstructure:"use_v2_api"`

	// Metric type mapping configuration (advanced)
	MetricTypeMapping *MetricTypeMappingConfig `mapstructure:"metric_type_mapping"`

	// Simplified metric type configuration
	MetricTypes *SimplifiedMetricTypeConfig `mapstructure:"metric_types"`

	// Prefix to prepend to all metric names (optional)
	Prefix string `mapstructure:"prefix"`
}

// MetricTypeMappingConfig defines how to map InfluxDB measurements to OpenTelemetry metric types
type MetricTypeMappingConfig struct {
	// Default metric type when no mapping is found (gauge, sum, histogram)
	DefaultType string `mapstructure:"default_type"`

	// Mapping rules for specific measurements
	MeasurementRules []MeasurementRule `mapstructure:"measurement_rules"`

	// Mapping rules based on field name patterns
	FieldRules []FieldRule `mapstructure:"field_rules"`

	// Mapping rules based on measurement name patterns (regex)
	PatternRules []PatternRule `mapstructure:"pattern_rules"`
}

// MeasurementRule maps a specific measurement to a metric type
type MeasurementRule struct {
	// Measurement name to match
	Measurement string `mapstructure:"measurement"`

	// OpenTelemetry metric type (gauge, sum, histogram)
	MetricType string `mapstructure:"metric_type"`

	// For sum metrics, whether it's cumulative or delta
	IsCumulative bool `mapstructure:"is_cumulative"`

	// For sum metrics, whether it's monotonic
	IsMonotonic bool `mapstructure:"is_monotonic"`
}

// FieldRule maps field names to metric types
type FieldRule struct {
	// Field name pattern to match (supports regex)
	FieldPattern string `mapstructure:"field_pattern"`

	// OpenTelemetry metric type
	MetricType string `mapstructure:"metric_type"`

	// For sum metrics, whether it's cumulative or delta
	IsCumulative bool `mapstructure:"is_cumulative"`

	// For sum metrics, whether it's monotonic
	IsMonotonic bool `mapstructure:"is_monotonic"`
}

// PatternRule maps measurement name patterns to metric types
type PatternRule struct {
	// Measurement name pattern (regex)
	MeasurementPattern string `mapstructure:"measurement_pattern"`

	// OpenTelemetry metric type
	MetricType string `mapstructure:"metric_type"`

	// For sum metrics, whether it's cumulative or delta
	IsCumulative bool `mapstructure:"is_cumulative"`

	// For sum metrics, whether it's monotonic
	IsMonotonic bool `mapstructure:"is_monotonic"`
}

// Validate checks if the receiver configuration is valid
func (cfg *Config) Validate() error {
	if cfg.Endpoint == nil {
		return errors.New("endpoint configuration is required")
	}
	if cfg.Endpoint.Address == "" {
		return errors.New("endpoint address is required")
	}
	if cfg.Database == "" {
		return errors.New("database is required")
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Second // default to 30 seconds
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second // default to 30 seconds
	}
	if cfg.AutoDiscover {
		// If auto-discover is enabled, we don't need a default query
		// The receiver will discover measurements automatically
	} else if cfg.Query == "" {
		cfg.Query = "SELECT * FROM /.*/" // default query for manual mode
	}

	// Validate metric type mapping configuration
	if err := validateMetricTypeMapping(cfg.MetricTypeMapping); err != nil {
		return fmt.Errorf("invalid metric type mapping: %w", err)
	}

	return nil
}
