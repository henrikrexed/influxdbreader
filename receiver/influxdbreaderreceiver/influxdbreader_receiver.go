package influxdbreaderreceiver

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/influxdata/influxdb-client-go/v2/api"
	client "github.com/influxdata/influxdb1-client"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver"
	"go.uber.org/zap"
)

// influxdbReaderReceiver is the receiver that reads metrics from InfluxDB
type influxdbReaderReceiver struct {
	config   *Config
	consumer consumer.Metrics
	logger   *zap.Logger
	cancel   context.CancelFunc
	done     chan struct{}

	// InfluxDB 2.x client
	v2Client   influxdb2.Client
	v2QueryAPI api.QueryAPI

	// InfluxDB 1.x client
	v1Client *client.Client
}

// newInfluxDBReaderReceiver creates a new InfluxDB reader receiver
func newInfluxDBReaderReceiver(
	config *Config,
	consumer consumer.Metrics,
	logger *zap.Logger,
) receiver.Metrics {
	return &influxdbReaderReceiver{
		config:   config,
		consumer: consumer,
		logger:   logger,
		done:     make(chan struct{}),
	}
}

// Start starts the receiver
func (r *influxdbReaderReceiver) Start(ctx context.Context, host component.Host) error {
	r.logger.Info("Starting InfluxDB reader receiver", zap.String("address", r.config.Endpoint.Address))

	// Create context with cancellation
	ctx, cancel := context.WithCancel(ctx)
	r.cancel = cancel

	// Initialize InfluxDB client based on version
	if r.config.UseV2API {
		if err := r.initV2Client(); err != nil {
			close(r.done) // Ensure done channel is closed even on failure
			return fmt.Errorf("failed to initialize InfluxDB 2.x client: %w", err)
		}
	} else {
		if err := r.initV1Client(); err != nil {
			close(r.done) // Ensure done channel is closed even on failure
			return fmt.Errorf("failed to initialize InfluxDB 1.x client: %w", err)
		}
	}

	// Start polling goroutine
	go r.pollMetrics(ctx)

	return nil
}

// Shutdown stops the receiver
func (r *influxdbReaderReceiver) Shutdown(ctx context.Context) error {
	r.logger.Info("Shutting down InfluxDB reader receiver")

	if r.cancel != nil {
		r.cancel()
	}

	if r.v2Client != nil {
		r.v2Client.Close()
	}

	<-r.done
	return nil
}

// initV2Client initializes the InfluxDB 2.x client
func (r *influxdbReaderReceiver) initV2Client() error {
	serverURL := fmt.Sprintf("%s://%s", r.config.Endpoint.Protocol, r.config.Endpoint.Address)

	opts := influxdb2.DefaultOptions()
	opts.SetHTTPClient(&http.Client{
		Timeout: r.config.Timeout,
	})

	if r.config.Insecure {
		opts.SetTLSConfig(nil)
	}

	token := ""
	if r.config.Endpoint.Authentication != nil {
		token = r.config.Endpoint.Authentication.Token
	}

	r.v2Client = influxdb2.NewClientWithOptions(serverURL, token, opts)

	org := ""
	if r.config.Endpoint.Authentication != nil {
		org = r.config.Endpoint.Authentication.Organization
	}
	r.v2QueryAPI = r.v2Client.QueryAPI(org)

	// Test connection
	_, err := r.v2Client.Ping(context.Background())
	return err
}

// initV1Client initializes the InfluxDB 1.x client
func (r *influxdbReaderReceiver) initV1Client() error {
	serverURL := fmt.Sprintf("%s://%s", r.config.Endpoint.Protocol, r.config.Endpoint.Address)

	parsedURL, err := url.Parse(serverURL)
	if err != nil {
		return fmt.Errorf("invalid server URL: %w", err)
	}

	config := client.Config{
		URL:     *parsedURL,
		Timeout: r.config.Timeout,
	}

	if r.config.Endpoint.Authentication != nil {
		config.Username = r.config.Endpoint.Authentication.Username
		config.Password = r.config.Endpoint.Authentication.Password
	}

	var clientErr error
	r.v1Client, clientErr = client.NewClient(config)
	if clientErr != nil {
		return clientErr
	}

	// Test connection
	_, _, err = r.v1Client.Ping()
	return err
}

// pollMetrics polls metrics from InfluxDB at regular intervals
func (r *influxdbReaderReceiver) pollMetrics(ctx context.Context) {
	ticker := time.NewTicker(r.config.Interval)
	defer ticker.Stop()
	defer close(r.done)

	// Poll immediately on start
	if err := r.fetchMetrics(ctx); err != nil {
		r.logger.Error("Failed to fetch metrics on startup", zap.Error(err))
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.fetchMetrics(ctx); err != nil {
				r.logger.Error("Failed to fetch metrics", zap.Error(err))
			}
		}
	}
}

// fetchMetrics fetches metrics from InfluxDB and sends them to the consumer
func (r *influxdbReaderReceiver) fetchMetrics(ctx context.Context) error {
	var metrics pmetric.Metrics

	if r.config.AutoDiscover {
		// Auto-discover measurements and fetch latest metrics
		if r.config.UseV2API {
			metrics = r.fetchV2MetricsWithDiscovery(ctx)
		} else {
			metrics = r.fetchV1MetricsWithDiscovery(ctx)
		}
	} else {
		// Use custom query
		if r.config.UseV2API {
			metrics = r.fetchV2Metrics(ctx)
		} else {
			metrics = r.fetchV1Metrics(ctx)
		}
	}

	if metrics.MetricCount() > 0 {
		return r.consumer.ConsumeMetrics(ctx, metrics)
	}

	return nil
}

// fetchV2Metrics fetches metrics from InfluxDB 2.x
func (r *influxdbReaderReceiver) fetchV2Metrics(ctx context.Context) pmetric.Metrics {
	metrics := pmetric.NewMetrics()
	resourceMetrics := metrics.ResourceMetrics().AppendEmpty()
	scopeMetrics := resourceMetrics.ScopeMetrics().AppendEmpty()

	// Build query
	query := r.config.Query
	if r.config.Endpoint.Authentication != nil && r.config.Endpoint.Authentication.Bucket != "" {
		query = fmt.Sprintf("from(bucket:\"%s\") %s", r.config.Endpoint.Authentication.Bucket, query)
	}

	r.logger.Debug("Executing InfluxDB 2.x query", zap.String("query", query))

	result, err := r.v2QueryAPI.Query(ctx, query)
	if err != nil {
		r.logger.Error("Failed to execute InfluxDB 2.x query", zap.Error(err))
		return metrics
	}
	defer result.Close()

	// Process results
	for result.Next() {
		record := result.Record()

		metric := scopeMetrics.Metrics().AppendEmpty()
		metric.SetName(record.Measurement())

		// Determine metric type based on configuration
		metricTypeInfo := r.determineMetricType(record.Measurement(), record.Field())

		// Set metric type based on field type and configuration
		if record.Value() != nil {
			switch v := record.Value().(type) {
			case float64:
				switch metricTypeInfo.Type {
				case MetricTypeCounter:
					dp := metric.SetEmptySum().DataPoints().AppendEmpty()
					dp.SetDoubleValue(v)
					metric.Sum().SetIsMonotonic(true)
				case MetricTypeHistogram:
					dp := metric.SetEmptyHistogram().DataPoints().AppendEmpty()
					dp.SetSum(v)
					dp.SetCount(1)
				default: // MetricTypeGauge or unknown
					dp := metric.SetEmptyGauge().DataPoints().AppendEmpty()
					dp.SetDoubleValue(v)
				}
			case int64:
				switch metricTypeInfo.Type {
				case MetricTypeCounter:
					dp := metric.SetEmptySum().DataPoints().AppendEmpty()
					dp.SetIntValue(v)
					metric.Sum().SetIsMonotonic(true)
				case MetricTypeHistogram:
					dp := metric.SetEmptyHistogram().DataPoints().AppendEmpty()
					dp.SetSum(float64(v))
					dp.SetCount(1)
				default: // MetricTypeGauge or unknown
					dp := metric.SetEmptyGauge().DataPoints().AppendEmpty()
					dp.SetIntValue(v)
				}
			case bool:
				dp := metric.SetEmptyGauge().DataPoints().AppendEmpty()
				if v {
					dp.SetIntValue(1)
				} else {
					dp.SetIntValue(0)
				}
			}
		}

		// Set timestamp
		timestamp := record.Time()
		if !timestamp.IsZero() {
			dp := metric.Gauge().DataPoints().At(0)
			dp.SetTimestamp(pcommon.Timestamp(timestamp.UnixNano()))
		}

		// Set labels from tags
		if record.Values() != nil {
			dp := metric.Gauge().DataPoints().At(0)
			for k, v := range record.Values() {
				if k != "_time" && k != "_value" && k != "_field" && k != "_measurement" {
					dp.Attributes().PutStr(k, fmt.Sprintf("%v", v))
				}
			}
		}
	}

	if result.Err() != nil {
		r.logger.Error("Error reading InfluxDB 2.x results", zap.Error(result.Err()))
	}

	return metrics
}

// fetchV1MetricsWithDiscovery discovers all measurements and fetches latest metrics for each
func (r *influxdbReaderReceiver) fetchV1MetricsWithDiscovery(ctx context.Context) pmetric.Metrics {
	metrics := pmetric.NewMetrics()
	resourceMetrics := metrics.ResourceMetrics().AppendEmpty()
	scopeMetrics := resourceMetrics.ScopeMetrics().AppendEmpty()

	// First, discover all measurements
	measurements, err := r.discoverV1Measurements(ctx)
	if err != nil {
		r.logger.Error("Failed to discover measurements", zap.Error(err))
		return metrics
	}

	r.logger.Debug("Discovered measurements", zap.Strings("measurements", measurements))

	// For each measurement, fetch the latest data point
	for _, measurement := range measurements {
		query := fmt.Sprintf("SELECT * FROM \"%s\" ORDER BY time DESC LIMIT 1", measurement)

		q := client.Query{
			Command:  query,
			Database: r.config.Database,
		}

		response, err := r.v1Client.Query(q)
		if err != nil {
			r.logger.Error("Failed to query measurement", zap.String("measurement", measurement), zap.Error(err))
			continue
		}

		if response.Error() != nil {
			r.logger.Error("Query returned error", zap.String("measurement", measurement), zap.Error(response.Error()))
			continue
		}

		// Process results for this measurement
		for _, result := range response.Results {
			for _, series := range result.Series {
				metric := scopeMetrics.Metrics().AppendEmpty()
				metric.SetName(series.Name)

				// Process each point in the series
				for _, point := range series.Values {
					if len(point) < 2 {
						continue
					}

					// Parse timestamp
					var timestamp time.Time
					if ts, ok := point[0].(string); ok {
						if t, err := time.Parse(time.RFC3339, ts); err == nil {
							timestamp = t
						}
					}

					// Parse value
					if value, ok := point[1].(float64); ok {
						// Determine metric type based on configuration
						metricTypeInfo := r.determineMetricType(series.Name, "value")

						// Set metric type based on configuration
						switch metricTypeInfo.Type {
						case MetricTypeCounter:
							dp := metric.SetEmptySum().DataPoints().AppendEmpty()
							dp.SetDoubleValue(value)
							metric.Sum().SetIsMonotonic(true)
						case MetricTypeHistogram:
							dp := metric.SetEmptyHistogram().DataPoints().AppendEmpty()
							dp.SetSum(value)
							dp.SetCount(1)
						default: // MetricTypeGauge or unknown
							dp := metric.SetEmptyGauge().DataPoints().AppendEmpty()
							dp.SetDoubleValue(value)
						}

						if !timestamp.IsZero() {
							// Set timestamp based on metric type
							switch metricTypeInfo.Type {
							case MetricTypeCounter:
								dp := metric.Sum().DataPoints().At(0)
								dp.SetTimestamp(pcommon.Timestamp(timestamp.UnixNano()))
							case MetricTypeHistogram:
								dp := metric.Histogram().DataPoints().At(0)
								dp.SetTimestamp(pcommon.Timestamp(timestamp.UnixNano()))
							default:
								dp := metric.Gauge().DataPoints().At(0)
								dp.SetTimestamp(pcommon.Timestamp(timestamp.UnixNano()))
							}
						}

						// Set labels from tags
						for i, column := range series.Columns {
							if i >= 2 && i < len(point) && column != "time" && column != "value" {
								if val, ok := point[i].(string); ok {
									// Set attributes based on metric type
									switch metricTypeInfo.Type {
									case MetricTypeCounter:
										dp := metric.Sum().DataPoints().At(0)
										dp.Attributes().PutStr(column, val)
									case MetricTypeHistogram:
										dp := metric.Histogram().DataPoints().At(0)
										dp.Attributes().PutStr(column, val)
									default:
										dp := metric.Gauge().DataPoints().At(0)
										dp.Attributes().PutStr(column, val)
									}
								}
							}
						}
					}
				}
			}
		}
	}

	return metrics
}

// fetchV2MetricsWithDiscovery discovers all measurements and fetches latest metrics for each
func (r *influxdbReaderReceiver) fetchV2MetricsWithDiscovery(ctx context.Context) pmetric.Metrics {
	metrics := pmetric.NewMetrics()
	resourceMetrics := metrics.ResourceMetrics().AppendEmpty()
	scopeMetrics := resourceMetrics.ScopeMetrics().AppendEmpty()

	// First, discover all measurements
	measurements, err := r.discoverV2Measurements(ctx)
	if err != nil {
		r.logger.Error("Failed to discover measurements", zap.Error(err))
		return metrics
	}

	r.logger.Debug("Discovered measurements", zap.Strings("measurements", measurements))

	// For each measurement, fetch the latest data point
	for _, measurement := range measurements {
		bucket := r.config.Database
		if r.config.Endpoint.Authentication != nil && r.config.Endpoint.Authentication.Bucket != "" {
			bucket = r.config.Endpoint.Authentication.Bucket
		}

		query := fmt.Sprintf(`from(bucket:"%s") |> range(start: -1h) |> filter(fn: (r) => r["_measurement"] == "%s") |> last()`, bucket, measurement)

		r.logger.Debug("Executing measurement query", zap.String("measurement", measurement), zap.String("query", query))

		result, err := r.v2QueryAPI.Query(ctx, query)
		if err != nil {
			r.logger.Error("Failed to query measurement", zap.String("measurement", measurement), zap.Error(err))
			continue
		}
		defer result.Close()

		// Process results for this measurement
		for result.Next() {
			record := result.Record()

			metric := scopeMetrics.Metrics().AppendEmpty()
			metric.SetName(record.Measurement())

			// Determine metric type based on configuration
			metricTypeInfo := r.determineMetricType(record.Measurement(), record.Field())

			// Set metric type based on field type and configuration
			if record.Value() != nil {
				switch v := record.Value().(type) {
				case float64:
					switch metricTypeInfo.Type {
					case MetricTypeCounter:
						dp := metric.SetEmptySum().DataPoints().AppendEmpty()
						dp.SetDoubleValue(v)
						metric.Sum().SetIsMonotonic(true)
					case MetricTypeHistogram:
						dp := metric.SetEmptyHistogram().DataPoints().AppendEmpty()
						dp.SetSum(v)
						dp.SetCount(1)
					default: // MetricTypeGauge or unknown
						dp := metric.SetEmptyGauge().DataPoints().AppendEmpty()
						dp.SetDoubleValue(v)
					}
				case int64:
					switch metricTypeInfo.Type {
					case MetricTypeCounter:
						dp := metric.SetEmptySum().DataPoints().AppendEmpty()
						dp.SetIntValue(v)
						metric.Sum().SetIsMonotonic(true)
					case MetricTypeHistogram:
						dp := metric.SetEmptyHistogram().DataPoints().AppendEmpty()
						dp.SetSum(float64(v))
						dp.SetCount(1)
					default: // MetricTypeGauge or unknown
						dp := metric.SetEmptyGauge().DataPoints().AppendEmpty()
						dp.SetIntValue(v)
					}
				case bool:
					dp := metric.SetEmptyGauge().DataPoints().AppendEmpty()
					if v {
						dp.SetIntValue(1)
					} else {
						dp.SetIntValue(0)
					}
				}
			}

			// Set timestamp
			timestamp := record.Time()
			if !timestamp.IsZero() {
				switch metricTypeInfo.Type {
				case MetricTypeCounter:
					dp := metric.Sum().DataPoints().At(0)
					dp.SetTimestamp(pcommon.Timestamp(timestamp.UnixNano()))
				case MetricTypeHistogram:
					dp := metric.Histogram().DataPoints().At(0)
					dp.SetTimestamp(pcommon.Timestamp(timestamp.UnixNano()))
				default:
					dp := metric.Gauge().DataPoints().At(0)
					dp.SetTimestamp(pcommon.Timestamp(timestamp.UnixNano()))
				}
			}

			// Set labels from tags
			if record.Values() != nil {
				for k, v := range record.Values() {
					if k != "_time" && k != "_value" && k != "_field" && k != "_measurement" {
						switch metricTypeInfo.Type {
						case MetricTypeCounter:
							dp := metric.Sum().DataPoints().At(0)
							dp.Attributes().PutStr(k, fmt.Sprintf("%v", v))
						case MetricTypeHistogram:
							dp := metric.Histogram().DataPoints().At(0)
							dp.Attributes().PutStr(k, fmt.Sprintf("%v", v))
						default:
							dp := metric.Gauge().DataPoints().At(0)
							dp.Attributes().PutStr(k, fmt.Sprintf("%v", v))
						}
					}
				}
			}
		}

		if result.Err() != nil {
			r.logger.Error("Error reading measurement results", zap.String("measurement", measurement), zap.Error(result.Err()))
		}
	}

	return metrics
}

// discoverV1Measurements discovers all measurements in InfluxDB 1.x
func (r *influxdbReaderReceiver) discoverV1Measurements(ctx context.Context) ([]string, error) {
	query := "SHOW MEASUREMENTS"

	q := client.Query{
		Command:  query,
		Database: r.config.Database,
	}

	response, err := r.v1Client.Query(q)
	if err != nil {
		return nil, fmt.Errorf("failed to discover measurements: %w", err)
	}

	if response.Error() != nil {
		return nil, fmt.Errorf("show measurements query returned error: %w", response.Error())
	}

	var measurements []string
	for _, result := range response.Results {
		for _, series := range result.Series {
			for _, point := range series.Values {
				if len(point) > 0 {
					if measurement, ok := point[0].(string); ok {
						measurements = append(measurements, measurement)
					}
				}
			}
		}
	}

	return measurements, nil
}

// discoverV2Measurements discovers all measurements in InfluxDB 2.x
func (r *influxdbReaderReceiver) discoverV2Measurements(ctx context.Context) ([]string, error) {
	bucket := r.config.Database
	if r.config.Endpoint.Authentication != nil && r.config.Endpoint.Authentication.Bucket != "" {
		bucket = r.config.Endpoint.Authentication.Bucket
	}

	query := fmt.Sprintf(`import "influxdata/influxdb/schema" schema.measurements(bucket: "%s")`, bucket)

	r.logger.Debug("Executing measurement discovery query", zap.String("query", query))

	result, err := r.v2QueryAPI.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to discover measurements: %w", err)
	}
	defer result.Close()

	var measurements []string
	for result.Next() {
		record := result.Record()
		if record.Value() != nil {
			if measurement, ok := record.Value().(string); ok {
				measurements = append(measurements, measurement)
			}
		}
	}

	if result.Err() != nil {
		return nil, fmt.Errorf("error reading measurement discovery results: %w", result.Err())
	}

	return measurements, nil
}

// fetchV1Metrics fetches metrics from InfluxDB 1.x
func (r *influxdbReaderReceiver) fetchV1Metrics(ctx context.Context) pmetric.Metrics {
	metrics := pmetric.NewMetrics()
	resourceMetrics := metrics.ResourceMetrics().AppendEmpty()
	scopeMetrics := resourceMetrics.ScopeMetrics().AppendEmpty()

	// Build query
	query := fmt.Sprintf("SELECT * FROM /.*/ WHERE time > now() - %ds", int(r.config.Interval.Seconds()))
	if r.config.Query != "" {
		query = r.config.Query
	}

	r.logger.Debug("Executing InfluxDB 1.x query", zap.String("query", query))

	q := client.Query{
		Command:  query,
		Database: r.config.Database,
	}

	response, err := r.v1Client.Query(q)
	if err != nil {
		r.logger.Error("Failed to execute InfluxDB 1.x query", zap.Error(err))
		return metrics
	}

	if response.Error() != nil {
		r.logger.Error("InfluxDB 1.x query returned error", zap.Error(response.Error()))
		return metrics
	}

	// Process results
	for _, result := range response.Results {
		for _, series := range result.Series {
			metric := scopeMetrics.Metrics().AppendEmpty()
			metric.SetName(series.Name)

			// Process each point in the series
			for _, point := range series.Values {
				if len(point) < 2 {
					continue
				}

				// Parse timestamp
				var timestamp time.Time
				if ts, ok := point[0].(string); ok {
					if t, err := time.Parse(time.RFC3339, ts); err == nil {
						timestamp = t
					}
				}

				// Parse value
				if value, ok := point[1].(float64); ok {
					dp := metric.SetEmptyGauge().DataPoints().AppendEmpty()
					dp.SetDoubleValue(value)

					if !timestamp.IsZero() {
						dp.SetTimestamp(pcommon.Timestamp(timestamp.UnixNano()))
					}

					// Set labels from tags
					for i, column := range series.Columns {
						if i >= 2 && i < len(point) && column != "time" && column != "value" {
							if val, ok := point[i].(string); ok {
								dp.Attributes().PutStr(column, val)
							}
						}
					}
				}
			}
		}
	}

	return metrics
}
