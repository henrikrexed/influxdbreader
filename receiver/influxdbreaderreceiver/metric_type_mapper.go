package influxdbreaderreceiver

import (
	"fmt"
	"regexp"
	"strings"
)

// MetricType represents OpenTelemetry metric types
type MetricType string

const (
	MetricTypeGauge     MetricType = "gauge"
	MetricTypeCounter   MetricType = "counter"
	MetricTypeHistogram MetricType = "histogram"
)

// MetricTypeInfo contains information about a metric type
type MetricTypeInfo struct {
	Type         MetricType
	IsCumulative bool
	IsMonotonic  bool
}

// determineMetricType determines the OpenTelemetry metric type for a given measurement and field
// Following Prometheus InfluxDB exporter: primarily creates gauges, only uses counter for very clear cumulative patterns
func (r *influxdbReaderReceiver) determineMetricType(measurement, field string) MetricTypeInfo {
	// Default to gauge if no mapping is configured (following Prometheus InfluxDB exporter approach)
	if r.config.MetricTypeMapping == nil {
		return MetricTypeInfo{
			Type:         MetricTypeGauge,
			IsCumulative: false,
			IsMonotonic:  false,
		}
	}

	// Check measurement-specific rules first
	for _, rule := range r.config.MetricTypeMapping.MeasurementRules {
		if rule.Measurement == measurement {
			return MetricTypeInfo{
				Type:         MetricType(rule.MetricType),
				IsCumulative: rule.IsCumulative,
				IsMonotonic:  rule.IsMonotonic,
			}
		}
	}

	// Check pattern rules
	for _, rule := range r.config.MetricTypeMapping.PatternRules {
		if matched, _ := regexp.MatchString(rule.MeasurementPattern, measurement); matched {
			return MetricTypeInfo{
				Type:         MetricType(rule.MetricType),
				IsCumulative: rule.IsCumulative,
				IsMonotonic:  rule.IsMonotonic,
			}
		}
	}

	// Check field rules
	for _, rule := range r.config.MetricTypeMapping.FieldRules {
		if matched, _ := regexp.MatchString(rule.FieldPattern, field); matched {
			return MetricTypeInfo{
				Type:         MetricType(rule.MetricType),
				IsCumulative: rule.IsCumulative,
				IsMonotonic:  rule.IsMonotonic,
			}
		}
	}

	// Use default type - be conservative and default to gauge for InfluxDB data
	var defaultType MetricType
	if r.config.MetricTypeMapping.DefaultType != "" {
		defaultType = MetricType(r.config.MetricTypeMapping.DefaultType)
	} else {
		// Following Prometheus InfluxDB exporter: primarily use gauges, only use counter for very clear cumulative patterns
		if isDefinitelyCounter(measurement) && isDefinitelyCounter(field) {
			// Only use counter if BOTH measurement and field are clearly counters
			defaultType = MetricTypeCounter
		} else if isLikelyHistogram(measurement) || isLikelyHistogram(field) {
			defaultType = MetricTypeHistogram
		} else {
			// Default to gauge for most InfluxDB measurements (like Prometheus InfluxDB exporter)
			defaultType = MetricTypeGauge
		}
	}

	return MetricTypeInfo{
		Type:         defaultType,
		IsCumulative: defaultType == MetricTypeCounter,
		IsMonotonic:  defaultType == MetricTypeCounter,
	}
}

// createMetricTypeMapping creates a default metric type mapping configuration
// Following Prometheus conventions for metric type classification
func createDefaultMetricTypeMapping() *MetricTypeMappingConfig {
	return &MetricTypeMappingConfig{
		DefaultType: "gauge",
		MeasurementRules: []MeasurementRule{
			// Common gauge measurements (descriptive names)
			{
				Measurement:  "cpu_usage",
				MetricType:   "gauge",
				IsCumulative: false,
				IsMonotonic:  false,
			},
			{
				Measurement:  "memory_free",
				MetricType:   "gauge",
				IsCumulative: false,
				IsMonotonic:  false,
			},
			{
				Measurement:  "temperature",
				MetricType:   "gauge",
				IsCumulative: false,
				IsMonotonic:  false,
			},
			{
				Measurement:  "disk_io",
				MetricType:   "gauge",
				IsCumulative: false,
				IsMonotonic:  false,
			},
		},
		FieldRules: []FieldRule{
			// Very specific counter field patterns (following Prometheus InfluxDB exporter conservatism)
			{
				FieldPattern: ".*_requests_total$",
				MetricType:   "counter",
				IsCumulative: true,
				IsMonotonic:  true,
			},
			{
				FieldPattern: ".*_errors_total$",
				MetricType:   "counter",
				IsCumulative: true,
				IsMonotonic:  true,
			},
			{
				FieldPattern: ".*_operations_total$",
				MetricType:   "counter",
				IsCumulative: true,
				IsMonotonic:  true,
			},
			{
				FieldPattern: ".*_events_total$",
				MetricType:   "counter",
				IsCumulative: true,
				IsMonotonic:  true,
			},
			// Note: Removed general _total and _count patterns to be more conservative
			// Most InfluxDB measurements are better treated as gauges
			// Histogram field patterns (end with _bucket, _sum, _quantile)
			{
				FieldPattern: ".*_bucket$",
				MetricType:   "histogram",
				IsCumulative: false,
				IsMonotonic:  false,
			},
			{
				FieldPattern: ".*_sum$",
				MetricType:   "histogram",
				IsCumulative: false,
				IsMonotonic:  false,
			},
			{
				FieldPattern: ".*_quantile$",
				MetricType:   "histogram",
				IsCumulative: false,
				IsMonotonic:  false,
			},
			// Gauge field patterns (descriptive names, no specific suffix)
			{
				FieldPattern: ".*_usage$",
				MetricType:   "gauge",
				IsCumulative: false,
				IsMonotonic:  false,
			},
			{
				FieldPattern: ".*_free$",
				MetricType:   "gauge",
				IsCumulative: false,
				IsMonotonic:  false,
			},
			{
				FieldPattern: ".*_temperature$",
				MetricType:   "gauge",
				IsCumulative: false,
				IsMonotonic:  false,
			},
		},
		PatternRules: []PatternRule{
			// Counter measurement patterns
			// Note: Removed _bytes pattern as it's too aggressive for InfluxDB data
			{
				MeasurementPattern: ".*_total$",
				MetricType:         "counter",
				IsCumulative:       true,
				IsMonotonic:        true,
			},
			{
				MeasurementPattern: ".*_count$",
				MetricType:         "counter",
				IsCumulative:       true,
				IsMonotonic:        true,
			},
			// Histogram measurement patterns
			{
				MeasurementPattern: ".*_histogram$",
				MetricType:         "histogram",
				IsCumulative:       false,
				IsMonotonic:        false,
			},
			{
				MeasurementPattern: ".*_bucket$",
				MetricType:         "histogram",
				IsCumulative:       false,
				IsMonotonic:        false,
			},
		},
	}
}

// validateMetricTypeMapping validates the metric type mapping configuration
func validateMetricTypeMapping(config *MetricTypeMappingConfig) error {
	if config == nil {
		return nil
	}

	validTypes := map[string]bool{
		"gauge":     true,
		"counter":   true,
		"histogram": true,
	}

	// Validate default type
	if config.DefaultType != "" && !validTypes[config.DefaultType] {
		return fmt.Errorf("invalid default metric type: %s", config.DefaultType)
	}

	// Validate measurement rules
	for i, rule := range config.MeasurementRules {
		if !validTypes[rule.MetricType] {
			return fmt.Errorf("invalid metric type in measurement rule %d: %s", i, rule.MetricType)
		}
	}

	// Validate field rules
	for i, rule := range config.FieldRules {
		if !validTypes[rule.MetricType] {
			return fmt.Errorf("invalid metric type in field rule %d: %s", i, rule.MetricType)
		}
		// Validate regex pattern
		if _, err := regexp.Compile(rule.FieldPattern); err != nil {
			return fmt.Errorf("invalid regex pattern in field rule %d: %s", i, rule.FieldPattern)
		}
	}

	// Validate pattern rules
	for i, rule := range config.PatternRules {
		if !validTypes[rule.MetricType] {
			return fmt.Errorf("invalid metric type in pattern rule %d: %s", i, rule.MetricType)
		}
		// Validate regex pattern
		if _, err := regexp.Compile(rule.MeasurementPattern); err != nil {
			return fmt.Errorf("invalid regex pattern in pattern rule %d: %s", i, rule.MeasurementPattern)
		}
	}

	return nil
}

// isLikelyCounter checks if a measurement/field name suggests it's a counter
func isLikelyCounter(name string) bool {
	name = strings.ToLower(name)
	counterPatterns := []string{
		"_total", "_count", "_bytes", "_requests_total", "_errors_total",
		"total_", "count_", "bytes_",
	}

	for _, pattern := range counterPatterns {
		if strings.Contains(name, pattern) {
			return true
		}
	}

	return false
}

// isDefinitelyCounter checks if a measurement/field name is definitely a counter
// Following Prometheus InfluxDB exporter conservatism - only very clear cumulative patterns
func isDefinitelyCounter(name string) bool {
	name = strings.ToLower(name)
	// Very specific counter patterns - only patterns that are almost certainly cumulative
	counterPatterns := []string{
		"_requests_total", "_errors_total", "_operations_total", "_events_total",
		"_packets_total", "_connections_total", "_sessions_total", "_calls_total",
		"_transactions_total", "_messages_total",
		"requests_total", "errors_total", "operations_total", "events_total",
	}

	for _, pattern := range counterPatterns {
		if strings.Contains(name, pattern) {
			return true
		}
	}

	return false
}

// isLikelyGauge checks if a measurement/field name suggests it's a gauge
func isLikelyGauge(name string) bool {
	name = strings.ToLower(name)
	gaugePatterns := []string{
		"_usage", "_free", "_temperature", "_pressure", "_load",
		"usage_", "free_", "temperature_", "pressure_", "load_",
	}

	for _, pattern := range gaugePatterns {
		if strings.Contains(name, pattern) {
			return true
		}
	}

	return false
}

// isLikelyHistogram checks if a measurement/field name suggests it's a histogram
func isLikelyHistogram(name string) bool {
	name = strings.ToLower(name)
	histogramPatterns := []string{
		"_bucket", "_sum", "_quantile", "_histogram", "_percentile",
		"bucket_", "sum_", "quantile_", "histogram_", "percentile_",
	}

	for _, pattern := range histogramPatterns {
		if strings.Contains(name, pattern) {
			return true
		}
	}

	return false
}
