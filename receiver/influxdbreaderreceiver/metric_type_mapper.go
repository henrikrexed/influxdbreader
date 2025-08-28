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
func (r *influxdbReaderReceiver) determineMetricType(measurement, field string) MetricTypeInfo {
	// Default to gauge if no mapping is configured
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

	// Use default type
	defaultType := MetricTypeGauge
	if r.config.MetricTypeMapping.DefaultType != "" {
		defaultType = MetricType(r.config.MetricTypeMapping.DefaultType)
	} else {
		// Use intelligent detection based on measurement and field names
		if isLikelyCounter(measurement) || isLikelyCounter(field) {
			defaultType = MetricTypeCounter
		} else if isLikelyHistogram(measurement) || isLikelyHistogram(field) {
			defaultType = MetricTypeHistogram
		} else if isLikelyGauge(measurement) || isLikelyGauge(field) {
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
			// Counter field patterns (end with _total, _count, _bytes)
			{
				FieldPattern: ".*_total$",
				MetricType:   "counter",
				IsCumulative: true,
				IsMonotonic:  true,
			},
			{
				FieldPattern: ".*_count$",
				MetricType:   "counter",
				IsCumulative: true,
				IsMonotonic:  true,
			},
			{
				FieldPattern: ".*_bytes$",
				MetricType:   "counter",
				IsCumulative: true,
				IsMonotonic:  true,
			},
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
			{
				MeasurementPattern: ".*_bytes$",
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
