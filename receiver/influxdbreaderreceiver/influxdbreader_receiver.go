package influxdbreaderreceiver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync/atomic"
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
	config    *Config
	consumer  consumer.Metrics
	telemetry component.TelemetrySettings
	cancel    context.CancelFunc
	done      chan struct{}

	// InfluxDB 2.x client
	v2Client   influxdb2.Client
	v2QueryAPI api.QueryAPI

	// InfluxDB 1.x client
	v1Client *client.Client

	// Track last fetch time for incremental polling
	lastFetchTime time.Time

	// Telemetry metrics
	measurementsDiscovered int64
	metricsConverted       int64
	metricsDropped         int64
	queriesExecuted        int64
	queryErrors            int64
}

// newInfluxDBReaderReceiver creates a new InfluxDB reader receiver
func newInfluxDBReaderReceiver(
	config *Config,
	consumer consumer.Metrics,
	telemetry component.TelemetrySettings,
) receiver.Metrics {
	return &influxdbReaderReceiver{
		config:    config,
		consumer:  consumer,
		telemetry: telemetry,
		done:      make(chan struct{}),
	}
}

// Start starts the receiver
func (r *influxdbReaderReceiver) Start(ctx context.Context, host component.Host) error {
	r.telemetry.Logger.Info("Starting InfluxDB reader receiver", zap.String("address", r.config.Endpoint.Address))

	// Create context with cancellation
	ctx, cancel := context.WithCancel(ctx)
	r.cancel = cancel

	// Initialize InfluxDB client based on version
	if r.config.UseV2API {
		if err := r.initV2Client(); err != nil {
			close(r.done) // Ensure done channel is closed even on failure
			return fmt.Errorf("failed to initialize InfluxDB 2.x client: %w", err)
		}
		r.telemetry.Logger.Info("Successfully connected to InfluxDB 2.x", zap.String("address", r.config.Endpoint.Address))
	} else {
		if err := r.initV1Client(); err != nil {
			close(r.done) // Ensure done channel is closed even on failure
			return fmt.Errorf("failed to initialize InfluxDB 1.x client: %w", err)
		}
		r.telemetry.Logger.Info("Successfully connected to InfluxDB 1.x", zap.String("address", r.config.Endpoint.Address))
	}

	// Start polling goroutine
	go r.pollMetrics(ctx)

	return nil
}

// Shutdown stops the receiver
func (r *influxdbReaderReceiver) Shutdown(ctx context.Context) error {
	r.telemetry.Logger.Info("Shutting down InfluxDB reader receiver")

	if r.cancel != nil {
		r.cancel()
	}

	if r.v2Client != nil {
		r.v2Client.Close()
	}

	<-r.done
	return nil
}

// applyMetricPrefix applies the configured prefix to a metric name
func (r *influxdbReaderReceiver) applyMetricPrefix(metricName string) string {
	if r.config.Prefix == "" {
		return metricName
	}
	return fmt.Sprintf("%s_%s", r.config.Prefix, metricName)
}

// createTelemetryMetrics creates and sends telemetry metrics about the receiver's performance
func (r *influxdbReaderReceiver) createTelemetryMetrics(ctx context.Context) {
	metrics := pmetric.NewMetrics()
	resourceMetrics := metrics.ResourceMetrics().AppendEmpty()
	scopeMetrics := resourceMetrics.ScopeMetrics().AppendEmpty()

	// Add resource attributes following OpenTelemetry conventions
	resourceMetrics.Resource().Attributes().PutStr("service.name", "influxdbreader")
	resourceMetrics.Resource().Attributes().PutStr("service.version", "0.2.8")
	resourceMetrics.Resource().Attributes().PutStr("service.instance.id", "influxdbreader-receiver")

	// Add scope information
	scopeMetrics.Scope().SetName("influxdbreader")
	scopeMetrics.Scope().SetVersion("0.2.8")

	// Measurements discovered metric (Counter - non-monotonic for compatibility)
	measurementsMetric := scopeMetrics.Metrics().AppendEmpty()
	measurementsMetric.SetName("otelcol_receiver_influxdbreader_measurements_discovered")
	measurementsMetric.SetDescription("Number of measurements discovered by the receiver")
	measurementsMetric.SetUnit("1")
	dp := measurementsMetric.SetEmptySum().DataPoints().AppendEmpty()
	measurementsMetric.Sum().SetIsMonotonic(false) // Non-monotonic for backend compatibility
	dp.SetIntValue(atomic.LoadInt64(&r.measurementsDiscovered))
	dp.SetTimestamp(pcommon.Timestamp(time.Now().UnixNano()))

	// Metrics converted metric (Counter - non-monotonic for compatibility)
	convertedMetric := scopeMetrics.Metrics().AppendEmpty()
	convertedMetric.SetName("otelcol_receiver_influxdbreader_metrics_converted")
	convertedMetric.SetDescription("Number of metrics successfully converted from InfluxDB")
	convertedMetric.SetUnit("1")
	dp = convertedMetric.SetEmptySum().DataPoints().AppendEmpty()
	convertedMetric.Sum().SetIsMonotonic(false) // Non-monotonic for backend compatibility
	dp.SetIntValue(atomic.LoadInt64(&r.metricsConverted))
	dp.SetTimestamp(pcommon.Timestamp(time.Now().UnixNano()))

	// Metrics dropped metric (Counter - non-monotonic for compatibility)
	droppedMetric := scopeMetrics.Metrics().AppendEmpty()
	droppedMetric.SetName("otelcol_receiver_influxdbreader_metrics_dropped")
	droppedMetric.SetDescription("Number of metrics dropped due to errors or invalid data")
	droppedMetric.SetUnit("1")
	dp = droppedMetric.SetEmptySum().DataPoints().AppendEmpty()
	droppedMetric.Sum().SetIsMonotonic(false) // Non-monotonic for backend compatibility
	dp.SetIntValue(atomic.LoadInt64(&r.metricsDropped))
	dp.SetTimestamp(pcommon.Timestamp(time.Now().UnixNano()))

	// Queries executed metric (Counter - non-monotonic for compatibility)
	queriesMetric := scopeMetrics.Metrics().AppendEmpty()
	queriesMetric.SetName("otelcol_receiver_influxdbreader_queries_executed")
	queriesMetric.SetDescription("Number of queries executed against InfluxDB")
	queriesMetric.SetUnit("1")
	dp = queriesMetric.SetEmptySum().DataPoints().AppendEmpty()
	queriesMetric.Sum().SetIsMonotonic(false) // Non-monotonic for backend compatibility
	dp.SetIntValue(atomic.LoadInt64(&r.queriesExecuted))
	dp.SetTimestamp(pcommon.Timestamp(time.Now().UnixNano()))

	// Query errors metric (Counter - non-monotonic for compatibility)
	errorsMetric := scopeMetrics.Metrics().AppendEmpty()
	errorsMetric.SetName("otelcol_receiver_influxdbreader_queries_errors")
	errorsMetric.SetDescription("Number of query errors encountered")
	errorsMetric.SetUnit("1")
	dp = errorsMetric.SetEmptySum().DataPoints().AppendEmpty()
	errorsMetric.Sum().SetIsMonotonic(false) // Non-monotonic for backend compatibility
	dp.SetIntValue(atomic.LoadInt64(&r.queryErrors))
	dp.SetTimestamp(pcommon.Timestamp(time.Now().UnixNano()))

	// Send telemetry metrics to the consumer
	if err := r.consumer.ConsumeMetrics(ctx, metrics); err != nil {
		r.telemetry.Logger.Error("Failed to send telemetry metrics", zap.Error(err))
	} else {
		r.telemetry.Logger.Debug("Sent telemetry metrics",
			zap.Int64("measurementsDiscovered", atomic.LoadInt64(&r.measurementsDiscovered)),
			zap.Int64("metricsConverted", atomic.LoadInt64(&r.metricsConverted)),
			zap.Int64("metricsDropped", atomic.LoadInt64(&r.metricsDropped)),
			zap.Int64("queriesExecuted", atomic.LoadInt64(&r.queriesExecuted)),
			zap.Int64("queryErrors", atomic.LoadInt64(&r.queryErrors)))
	}
}

// initV2Client initializes the InfluxDB 2.x client
func (r *influxdbReaderReceiver) initV2Client() error {
	serverURL := fmt.Sprintf("%s://%s", r.config.Endpoint.Protocol, r.config.Endpoint.Address)
	r.telemetry.Logger.Info("Initializing InfluxDB 2.x client",
		zap.String("serverURL", serverURL),
		zap.Duration("timeout", r.config.Timeout),
		zap.Bool("insecure", r.config.Insecure))

	opts := influxdb2.DefaultOptions()
	opts.SetHTTPClient(&http.Client{
		Timeout: r.config.Timeout,
	})

	if r.config.Insecure {
		opts.SetTLSConfig(nil)
		r.telemetry.Logger.Debug("TLS verification disabled for InfluxDB 2.x client")
	}

	token := ""
	org := ""
	if r.config.Endpoint.Authentication != nil {
		token = r.config.Endpoint.Authentication.Token
		org = r.config.Endpoint.Authentication.Organization
		r.telemetry.Logger.Debug("Using authentication for InfluxDB 2.x",
			zap.String("organization", org),
			zap.Bool("hasToken", token != ""))
	} else {
		r.telemetry.Logger.Info("No authentication configured for InfluxDB 2.x")
	}

	r.v2Client = influxdb2.NewClientWithOptions(serverURL, token, opts)
	r.v2QueryAPI = r.v2Client.QueryAPI(org)

	r.telemetry.Logger.Debug("Testing InfluxDB 2.x connection...")
	// Test connection
	_, err := r.v2Client.Ping(context.Background())
	if err != nil {
		r.telemetry.Logger.Error("Failed to ping InfluxDB 2.x server", zap.Error(err))
		return err
	}

	r.telemetry.Logger.Info("Successfully connected to InfluxDB 2.x server")
	return nil
}

// initV1Client initializes the InfluxDB 1.x client
func (r *influxdbReaderReceiver) initV1Client() error {
	serverURL := fmt.Sprintf("%s://%s", r.config.Endpoint.Protocol, r.config.Endpoint.Address)
	r.telemetry.Logger.Debug("Initializing InfluxDB 1.x client",
		zap.String("serverURL", serverURL),
		zap.Duration("timeout", r.config.Timeout))

	parsedURL, err := url.Parse(serverURL)
	if err != nil {
		r.telemetry.Logger.Error("Invalid server URL", zap.String("serverURL", serverURL), zap.Error(err))
		return fmt.Errorf("invalid server URL: %w", err)
	}

	config := client.Config{
		URL:     *parsedURL,
		Timeout: r.config.Timeout,
	}

	if r.config.Endpoint.Authentication != nil {
		config.Username = r.config.Endpoint.Authentication.Username
		config.Password = r.config.Endpoint.Authentication.Password
		r.telemetry.Logger.Debug("Using authentication for InfluxDB 1.x",
			zap.String("username", config.Username),
			zap.Bool("hasPassword", config.Password != ""))
	} else {
		r.telemetry.Logger.Debug("No authentication configured for InfluxDB 1.x")
	}

	var clientErr error
	r.v1Client, clientErr = client.NewClient(config)
	if clientErr != nil {
		r.telemetry.Logger.Error("Failed to create InfluxDB 1.x client", zap.Error(clientErr))
		return clientErr
	}

	r.telemetry.Logger.Debug("Testing InfluxDB 1.x connection...")
	// Test connection
	_, _, err = r.v1Client.Ping()
	if err != nil {
		r.telemetry.Logger.Error("Failed to ping InfluxDB 1.x server", zap.Error(err))
		return err
	}

	r.telemetry.Logger.Debug("Successfully connected to InfluxDB 1.x server")
	return nil
}

// pollMetrics polls metrics from InfluxDB at regular intervals
func (r *influxdbReaderReceiver) pollMetrics(ctx context.Context) {
	r.telemetry.Logger.Info("Starting metrics polling",
		zap.Duration("interval", r.config.Interval),
		zap.Bool("autoDiscover", r.config.AutoDiscover),
		zap.Bool("useV2API", r.config.UseV2API))

	ticker := time.NewTicker(r.config.Interval)
	defer ticker.Stop()
	defer close(r.done)

	// Poll immediately on start
	r.telemetry.Logger.Info("Performing initial metrics fetch...")
	if err := r.fetchMetrics(ctx); err != nil {
		r.telemetry.Logger.Error("Failed to fetch metrics on startup", zap.Error(err))
	} else {
		r.telemetry.Logger.Debug("Initial metrics fetch completed successfully")
	}

	for {
		select {
		case <-ctx.Done():
			r.telemetry.Logger.Debug("Polling stopped due to context cancellation")
			return
		case <-ticker.C:
			if err := r.fetchMetrics(ctx); err != nil {
				r.telemetry.Logger.Error("Failed to fetch metrics", zap.Error(err))
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

	metricCount := metrics.MetricCount()
	r.telemetry.Logger.Info("Metrics fetch completed",
		zap.Int("metricCount", metricCount),
		zap.Int("resourceCount", metrics.ResourceMetrics().Len()))

	if metricCount > 0 {
		if err := r.consumer.ConsumeMetrics(ctx, metrics); err != nil {
			r.telemetry.Logger.Error("Failed to send metrics to consumer", zap.Error(err))
			return err
		}
		r.telemetry.Logger.Debug("Successfully sent metrics to consumer", zap.Int("metricCount", metricCount))
	} else {
		r.telemetry.Logger.Warn("No metrics found to send to consumer")
	}

	// Send telemetry metrics
	r.createTelemetryMetrics(ctx)

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

	r.telemetry.Logger.Debug("Executing InfluxDB 2.x query", zap.String("query", query))

	result, err := r.v2QueryAPI.Query(ctx, query)
	if err != nil {
		r.telemetry.Logger.Error("Failed to execute InfluxDB 2.x query", zap.Error(err))
		return metrics
	}
	defer result.Close()

	// Process results
	for result.Next() {
		record := result.Record()

		metric := scopeMetrics.Metrics().AppendEmpty()
		metricName := r.applyMetricPrefix(record.Measurement())
		metric.SetName(metricName)

		// Determine metric type based on configuration
		metricTypeInfo := r.determineMetricType(record.Measurement(), record.Field())

		// Set metric type based on field type and configuration
		if record.Value() != nil {
			switch v := record.Value().(type) {
			case float64:
				switch metricTypeInfo.Type {
				case MetricTypeCounter:
					// Only set the metric type once, then append data points
					if metric.Type() == pmetric.MetricTypeEmpty {
						metric.SetEmptySum()
						metric.Sum().SetIsMonotonic(true)
					}
					dp := metric.Sum().DataPoints().AppendEmpty()
					dp.SetDoubleValue(v)
				case MetricTypeHistogram:
					// Only set the metric type once, then append data points
					if metric.Type() == pmetric.MetricTypeEmpty {
						metric.SetEmptyHistogram()
					}
					dp := metric.Histogram().DataPoints().AppendEmpty()
					dp.SetSum(v)
					dp.SetCount(1)
				default: // MetricTypeGauge or unknown
					// Only set the metric type once, then append data points
					if metric.Type() == pmetric.MetricTypeEmpty {
						metric.SetEmptyGauge()
					}
					dp := metric.Gauge().DataPoints().AppendEmpty()
					dp.SetDoubleValue(v)
				}
			case int64:
				switch metricTypeInfo.Type {
				case MetricTypeCounter:
					// Only set the metric type once, then append data points
					if metric.Type() == pmetric.MetricTypeEmpty {
						metric.SetEmptySum()
						metric.Sum().SetIsMonotonic(true)
					}
					dp := metric.Sum().DataPoints().AppendEmpty()
					dp.SetIntValue(v)
				case MetricTypeHistogram:
					// Only set the metric type once, then append data points
					if metric.Type() == pmetric.MetricTypeEmpty {
						metric.SetEmptyHistogram()
					}
					dp := metric.Histogram().DataPoints().AppendEmpty()
					dp.SetSum(float64(v))
					dp.SetCount(1)
				default: // MetricTypeGauge or unknown
					// Only set the metric type once, then append data points
					if metric.Type() == pmetric.MetricTypeEmpty {
						metric.SetEmptyGauge()
					}
					dp := metric.Gauge().DataPoints().AppendEmpty()
					dp.SetIntValue(v)
				}
			case bool:
				// Only set the metric type once, then append data points
				if metric.Type() == pmetric.MetricTypeEmpty {
					metric.SetEmptyGauge()
				}
				dp := metric.Gauge().DataPoints().AppendEmpty()
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
		r.telemetry.Logger.Error("Error reading InfluxDB 2.x results", zap.Error(result.Err()))
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
		r.telemetry.Logger.Error("Failed to discover measurements", zap.Error(err))
		atomic.AddInt64(&r.queryErrors, 1)
		return metrics
	}

	// Update telemetry metrics
	atomic.AddInt64(&r.measurementsDiscovered, int64(len(measurements)))
	r.telemetry.Logger.Debug("Discovered measurements", zap.Int("count", len(measurements)))

	// For each measurement, fetch data since last fetch time
	// Track the latest timestamp across all measurements for next fetch
	var latestTimestamp time.Time

	for i, measurement := range measurements {
		r.telemetry.Logger.Debug("Processing measurement",
			zap.Int("measurementIndex", i+1),
			zap.Int("totalMeasurements", len(measurements)),
			zap.String("measurement", measurement))

		// Build query with time-based filtering
		var query string
		if r.lastFetchTime.IsZero() {
			// First run: get data from the past interval to avoid missing metrics
			// Calculate the start time as current time minus the interval
			startTime := time.Now().Add(-r.config.Interval)
			query = fmt.Sprintf("SELECT * FROM \"%s\" WHERE time >= '%s' ORDER BY time ASC",
				measurement, startTime.Format(time.RFC3339))
			r.telemetry.Logger.Debug("Executing V1 measurement query (first run - historical)",
				zap.String("measurement", measurement),
				zap.String("query", query),
				zap.Time("startTime", startTime),
				zap.Duration("interval", r.config.Interval))
		} else {
			// Subsequent runs: get data since last fetch
			query = fmt.Sprintf("SELECT * FROM \"%s\" WHERE time > '%s' ORDER BY time ASC",
				measurement, r.lastFetchTime.Format(time.RFC3339))
			r.telemetry.Logger.Debug("Executing V1 measurement query (incremental)",
				zap.String("measurement", measurement),
				zap.String("query", query),
				zap.Time("since", r.lastFetchTime))
		}

		q := client.Query{
			Command:  query,
			Database: r.config.Database,
		}

		response, err := r.v1Client.Query(q)
		if err != nil {
			r.telemetry.Logger.Error("Failed to query V1 measurement",
				zap.String("measurement", measurement),
				zap.Error(err))
			atomic.AddInt64(&r.queryErrors, 1)
			continue
		}

		if response.Error() != nil {
			r.telemetry.Logger.Error("V1 query returned error",
				zap.String("measurement", measurement),
				zap.Error(response.Error()))
			atomic.AddInt64(&r.queryErrors, 1)
			continue
		}

		// Update telemetry metrics
		atomic.AddInt64(&r.queriesExecuted, 1)

		r.telemetry.Logger.Debug("V1 query executed successfully",
			zap.String("measurement", measurement))

		// Process results for this measurement
		seriesCount := 0
		totalDataPoints := 0
		r.telemetry.Logger.Debug("Processing V1 measurement results",
			zap.String("measurement", measurement),
			zap.Int("resultCount", len(response.Results)))

		// Analyze columns to identify numeric vs string columns
		var numericColumns []string
		var stringColumns []string

		// Use the first series to determine column structure
		if len(response.Results) > 0 && len(response.Results[0].Series) > 0 {
			firstSeries := response.Results[0].Series[0]
			if len(firstSeries.Values) > 0 {
				firstRow := firstSeries.Values[0]
				for i, value := range firstRow {
					if i < len(firstSeries.Columns) {
						columnName := firstSeries.Columns[i]
						if columnName == "time" {
							continue // Skip time column
						}

						switch v := value.(type) {
						case float64, int64, int:
							numericColumns = append(numericColumns, columnName)
						case string:
							stringColumns = append(stringColumns, columnName)
						case json.Number:
							// json.Number represents numeric values in InfluxDB 1.x
							numericColumns = append(numericColumns, columnName)
						default:
							// Try to convert to number if it's a json.Number string
							if str, ok := v.(string); ok {
								if _, err := strconv.ParseFloat(str, 64); err == nil {
									numericColumns = append(numericColumns, columnName)
								} else {
									stringColumns = append(stringColumns, columnName)
								}
							} else {
								stringColumns = append(stringColumns, columnName)
							}
						}
					}
				}
			}
		}

		r.telemetry.Logger.Debug("Column analysis for measurement",
			zap.String("measurement", measurement),
			zap.Strings("numericColumns", numericColumns),
			zap.Strings("stringColumns", stringColumns))

		// Create a map to track metrics by their unique identifier (name + attributes)
		// This ensures we accumulate data points within the same metric
		metricMap := make(map[string]pmetric.Metric)

		// Process all results and series to add data points to the appropriate metrics
		for _, result := range response.Results {
			r.telemetry.Logger.Debug("Processing V1 result",
				zap.String("measurement", measurement),
				zap.Int("seriesCount", len(result.Series)))

			for _, series := range result.Series {
				seriesCount++
				totalDataPoints += len(series.Values)
				r.telemetry.Logger.Debug("Processing V1 series",
					zap.String("measurement", measurement),
					zap.String("seriesName", series.Name),
					zap.Int("seriesIndex", seriesCount),
					zap.Strings("columns", series.Columns),
					zap.Int("pointCount", len(series.Values)),
					zap.Any("tags", series.Tags))

				// Process each numeric column for this series
				for _, numericColumn := range numericColumns {
					metricTypeInfo := r.determineMetricType(measurement, numericColumn)

					// Process each point in the series
					pointCount := 0
					dataPointsCreated := 0
					r.telemetry.Logger.Debug("Processing data points for series",
						zap.String("measurement", measurement),
						zap.String("numericColumn", numericColumn),
						zap.Int("totalPoints", len(series.Values)),
						zap.Any("seriesTags", series.Tags))

					for _, point := range series.Values {
						pointCount++
						if len(point) < 2 {
							r.telemetry.Logger.Debug("Skipping V1 point - insufficient data",
								zap.String("measurement", measurement),
								zap.String("numericColumn", numericColumn),
								zap.Int("pointIndex", pointCount),
								zap.Int("pointLength", len(point)))
							atomic.AddInt64(&r.metricsDropped, 1)
							continue
						}

						r.telemetry.Logger.Debug("Processing V1 point for column",
							zap.String("measurement", measurement),
							zap.String("numericColumn", numericColumn),
							zap.Int("pointIndex", pointCount),
							zap.Any("pointData", point))

						// Find the column index for this numeric column
						columnIndex := -1
						for i, colName := range series.Columns {
							if colName == numericColumn {
								columnIndex = i
								break
							}
						}

						if columnIndex == -1 || columnIndex >= len(point) {
							r.telemetry.Logger.Warn("Column not found or index out of bounds",
								zap.String("measurement", measurement),
								zap.String("numericColumn", numericColumn),
								zap.Int("columnIndex", columnIndex),
								zap.Int("pointLength", len(point)))
							atomic.AddInt64(&r.metricsDropped, 1)
							continue
						}

						// Parse timestamp
						var timestamp time.Time
						if ts, ok := point[0].(string); ok {
							if t, err := time.Parse(time.RFC3339, ts); err == nil {
								timestamp = t
								r.telemetry.Logger.Debug("Parsed V1 timestamp",
									zap.String("measurement", measurement),
									zap.String("numericColumn", numericColumn),
									zap.Time("timestamp", timestamp))
							} else {
								r.telemetry.Logger.Debug("Failed to parse V1 timestamp",
									zap.String("measurement", measurement),
									zap.String("numericColumn", numericColumn),
									zap.String("timestampString", ts),
									zap.Error(err))
							}
						}

						// Build unique metric identifier based on measurement, column, and attributes
						// Extract attributes from string columns and series tags
						r.telemetry.Logger.Debug("Starting map-based metric creation",
							zap.String("measurement", measurement),
							zap.String("numericColumn", numericColumn),
							zap.Int("pointIndex", pointCount))

						attributes := make(map[string]string)

						// Add attributes from string columns
						for _, stringColumn := range stringColumns {
							for i, colName := range series.Columns {
								if colName == stringColumn && i < len(point) {
									if val, ok := point[i].(string); ok {
										attributes[stringColumn] = val
									}
								}
							}
						}

						// Add attributes from series tags
						for tagKey, tagValue := range series.Tags {
							attributes[tagKey] = tagValue
						}

						// Create unique metric key
						metricKey := fmt.Sprintf("%s_%s", measurement, numericColumn)
						for k, v := range attributes {
							metricKey += fmt.Sprintf("_%s_%s", k, v)
						}

						r.telemetry.Logger.Debug("Generated metric key",
							zap.String("measurement", measurement),
							zap.String("numericColumn", numericColumn),
							zap.String("metricKey", metricKey),
							zap.Any("attributes", attributes))

						// Get or create metric
						metric, exists := metricMap[metricKey]
						if !exists {
							metricName := fmt.Sprintf("%s_%s", measurement, numericColumn)
							metricName = r.applyMetricPrefix(metricName)
							metric = scopeMetrics.Metrics().AppendEmpty()
							metric.SetName(metricName)
							metric.SetDescription(fmt.Sprintf("Metric %s from InfluxDB measurement: %s", numericColumn, measurement))
							metricMap[metricKey] = metric

							// Track metric creation for telemetry
							atomic.AddInt64(&r.metricsConverted, 1)

							r.telemetry.Logger.Debug("Created new metric for unique combination",
								zap.String("measurement", measurement),
								zap.String("numericColumn", numericColumn),
								zap.String("metricKey", metricKey),
								zap.String("metricName", metricName),
								zap.String("metricType", string(metricTypeInfo.Type)),
								zap.Any("attributes", attributes))
						}

						// Parse value from the specific numeric column
						var dataPoint pmetric.NumberDataPoint
						switch metricTypeInfo.Type {
						case MetricTypeCounter:
							// Only set the metric type once, then append data points
							if metric.Type() == pmetric.MetricTypeEmpty {
								metric.SetEmptySum()
								metric.Sum().SetIsMonotonic(true)
							}
							dp := metric.Sum().DataPoints().AppendEmpty()
							dataPoint = dp
							r.telemetry.Logger.Debug("Created V1 counter data point",
								zap.String("measurement", measurement),
								zap.String("numericColumn", numericColumn),
								zap.Int("pointIndex", pointCount),
								zap.Int("totalDataPoints", metric.Sum().DataPoints().Len()))
						case MetricTypeHistogram:
							// Only set the metric type once, then append data points
							if metric.Type() == pmetric.MetricTypeEmpty {
								metric.SetEmptyHistogram()
							}
							dp := metric.Histogram().DataPoints().AppendEmpty()
							// For now, we'll create a simple histogram from a single value
							if val, ok := point[columnIndex].(float64); ok {
								dp.SetSum(val)
								dp.SetCount(1)
								r.telemetry.Logger.Debug("Created V1 histogram data point",
									zap.String("measurement", measurement),
									zap.String("numericColumn", numericColumn),
									zap.Float64("sum", val),
									zap.Uint64("count", 1))
							}
							continue // Skip the regular data point creation for histograms
						default: // MetricTypeGauge or unknown
							// Only set the metric type once, then append data points
							if metric.Type() == pmetric.MetricTypeEmpty {
								metric.SetEmptyGauge()
							}
							dp := metric.Gauge().DataPoints().AppendEmpty()
							dataPoint = dp
							r.telemetry.Logger.Debug("Created V1 gauge data point",
								zap.String("measurement", measurement),
								zap.String("numericColumn", numericColumn),
								zap.Int("pointIndex", pointCount),
								zap.Int("totalDataPoints", metric.Gauge().DataPoints().Len()))
						}

						// Set value from the specific numeric column
						// Handle nil values by converting to 0, otherwise use the actual value
						var value interface{}
						if point[columnIndex] == nil {
							r.telemetry.Logger.Debug("Converting nil value to 0 for numeric column",
								zap.String("measurement", measurement),
								zap.String("numericColumn", numericColumn))
							value = float64(0)
						} else {
							value = point[columnIndex]
						}

						switch v := value.(type) {
						case float64:
							dataPoint.SetDoubleValue(v)
						case int64:
							dataPoint.SetIntValue(v)
						case int:
							dataPoint.SetIntValue(int64(v))
						case bool:
							if v {
								dataPoint.SetIntValue(1)
							} else {
								dataPoint.SetIntValue(0)
							}
						case json.Number:
							// Convert json.Number to float64
							if floatVal, err := v.Float64(); err == nil {
								dataPoint.SetDoubleValue(floatVal)
							} else {
								r.telemetry.Logger.Warn("Failed to convert json.Number to float64",
									zap.String("measurement", measurement),
									zap.String("numericColumn", numericColumn),
									zap.String("value", v.String()),
									zap.Error(err))
								atomic.AddInt64(&r.metricsDropped, 1)
								continue
							}
						default:
							r.telemetry.Logger.Warn("Unsupported V1 data point value type",
								zap.String("measurement", measurement),
								zap.String("numericColumn", numericColumn),
								zap.Any("value", v),
								zap.String("type", fmt.Sprintf("%T", v)))
							atomic.AddInt64(&r.metricsDropped, 1)
							continue
						}

						// Set timestamp
						if !timestamp.IsZero() {
							dataPoint.SetTimestamp(pcommon.Timestamp(timestamp.UnixNano()))
							r.telemetry.Logger.Debug("Set V1 data point timestamp",
								zap.String("measurement", measurement),
								zap.String("numericColumn", numericColumn),
								zap.Time("timestamp", timestamp))
						} else {
							r.telemetry.Logger.Debug("No timestamp available for V1 data point",
								zap.String("measurement", measurement),
								zap.String("numericColumn", numericColumn))
						}

						// Set attributes from series tags (InfluxDB 1.x tags)
						attributeCount := 0
						for tagKey, tagValue := range series.Tags {
							dataPoint.Attributes().PutStr(tagKey, tagValue)
							attributeCount++
							r.telemetry.Logger.Debug("Set V1 metric attribute from series tag",
								zap.String("measurement", measurement),
								zap.String("numericColumn", numericColumn),
								zap.String("attributeKey", tagKey),
								zap.String("attributeValue", tagValue))
						}

						// Set attributes from string columns (field values)
						for i, column := range series.Columns {
							if i < len(point) && column != "time" && column != numericColumn {
								// Only add string values as attributes
								if val, ok := point[i].(string); ok {
									dataPoint.Attributes().PutStr(column, val)
									attributeCount++
									r.telemetry.Logger.Debug("Set V1 metric attribute from field",
										zap.String("measurement", measurement),
										zap.String("numericColumn", numericColumn),
										zap.String("attributeKey", column),
										zap.String("attributeValue", val))
								} else {
									r.telemetry.Logger.Debug("Skipping non-string attribute",
										zap.String("measurement", measurement),
										zap.String("numericColumn", numericColumn),
										zap.String("attributeKey", column),
										zap.Any("attributeValue", point[i]),
										zap.String("valueType", fmt.Sprintf("%T", point[i])))
								}
							}
						}

						dataPointsCreated++
						r.telemetry.Logger.Debug("Completed V1 data point creation for column",
							zap.String("measurement", measurement),
							zap.String("numericColumn", numericColumn),
							zap.Int("pointIndex", pointCount),
							zap.Int("attributeCount", attributeCount))
					}

					r.telemetry.Logger.Debug("Processed V1 data point",
						zap.String("measurement", measurement),
						zap.String("numericColumn", numericColumn),
						zap.String("metricType", string(metricTypeInfo.Type)),
						zap.Int("dataPointsCreated", dataPointsCreated),
						zap.Any("seriesTags", series.Tags))
				}
			}
		} // End of result loop

		// Track the latest timestamp from this measurement
		for _, result := range response.Results {
			for _, series := range result.Series {
				for _, point := range series.Values {
					if len(point) > 0 {
						if ts, ok := point[0].(string); ok {
							if t, err := time.Parse(time.RFC3339, ts); err == nil {
								if t.After(latestTimestamp) {
									latestTimestamp = t
								}
							}
						}
					}
				}
			}
		}

		r.telemetry.Logger.Debug("V1 measurement processing completed",
			zap.String("measurement", measurement),
			zap.Int("seriesCount", seriesCount),
			zap.Int("totalDataPoints", totalDataPoints))
	} // End of measurement loop

	// Update the last fetch time for next iteration
	if !latestTimestamp.IsZero() {
		r.lastFetchTime = latestTimestamp
		r.telemetry.Logger.Debug("Updated last fetch time",
			zap.Time("lastFetchTime", r.lastFetchTime))
	}

	return metrics
}

// describeMeasurementStructure describes the columns and data types of a measurement
func (r *influxdbReaderReceiver) describeMeasurementStructure(ctx context.Context, measurement string) {
	// Query to get the structure of the measurement
	query := fmt.Sprintf("SELECT * FROM \"%s\" LIMIT 1", measurement)

	q := client.Query{
		Command:  query,
		Database: r.config.Database,
	}

	response, err := r.v1Client.Query(q)
	if err != nil {
		r.telemetry.Logger.Error("Failed to describe measurement structure",
			zap.String("measurement", measurement),
			zap.Error(err))
		return
	}

	if response.Error() != nil {
		r.telemetry.Logger.Error("Describe measurement structure query returned error",
			zap.String("measurement", measurement),
			zap.Error(response.Error()))
		return
	}

	// Process results to describe the structure
	for _, result := range response.Results {
		for _, series := range result.Series {
			r.telemetry.Logger.Debug("Measurement structure",
				zap.String("measurement", measurement),
				zap.Strings("columns", series.Columns),
				zap.Int("columnCount", len(series.Columns)))

			// Analyze the first row to determine data types
			if len(series.Values) > 0 {
				firstRow := series.Values[0]
				columnTypes := make([]string, len(firstRow))
				columnValues := make([]interface{}, len(firstRow))

				for i, value := range firstRow {
					columnTypes[i] = fmt.Sprintf("%T", value)
					columnValues[i] = value
				}

				r.telemetry.Logger.Debug("Measurement data types",
					zap.String("measurement", measurement),
					zap.Strings("columnNames", series.Columns),
					zap.Strings("columnTypes", columnTypes),
					zap.Any("sampleValues", columnValues))

				// Identify numeric columns that could be metrics
				var numericColumns []string
				var stringColumns []string
				for i, value := range firstRow {
					switch value.(type) {
					case float64, int64, int:
						numericColumns = append(numericColumns, series.Columns[i])
					case string:
						stringColumns = append(stringColumns, series.Columns[i])
					}
				}

				r.telemetry.Logger.Debug("Measurement column analysis",
					zap.String("measurement", measurement),
					zap.Strings("numericColumns", numericColumns),
					zap.Strings("stringColumns", stringColumns),
					zap.Int("numericColumnCount", len(numericColumns)),
					zap.Int("stringColumnCount", len(stringColumns)))
			}
		}
	}
}

// fetchV2MetricsWithDiscovery discovers all measurements and fetches latest metrics for each
func (r *influxdbReaderReceiver) fetchV2MetricsWithDiscovery(ctx context.Context) pmetric.Metrics {
	r.telemetry.Logger.Debug("Starting V2 metrics discovery and fetch")

	metrics := pmetric.NewMetrics()
	resourceMetrics := metrics.ResourceMetrics().AppendEmpty()
	scopeMetrics := resourceMetrics.ScopeMetrics().AppendEmpty()

	// First, discover all measurements
	r.telemetry.Logger.Debug("Discovering V2 measurements...")
	measurements, err := r.discoverV2Measurements(ctx)
	if err != nil {
		r.telemetry.Logger.Error("Failed to discover V2 measurements", zap.Error(err))
		return metrics
	}

	r.telemetry.Logger.Debug("V2 measurements discovery completed",
		zap.Int("measurementCount", len(measurements)),
		zap.Strings("measurements", measurements))

	// For each measurement, fetch the latest data point
	r.telemetry.Logger.Debug("Starting to process measurements", zap.Int("totalMeasurements", len(measurements)))

	for i, measurement := range measurements {
		r.telemetry.Logger.Debug("Processing measurement",
			zap.Int("measurementIndex", i+1),
			zap.Int("totalMeasurements", len(measurements)),
			zap.String("measurement", measurement))

		bucket := r.config.Database
		if r.config.Endpoint.Authentication != nil && r.config.Endpoint.Authentication.Bucket != "" {
			bucket = r.config.Endpoint.Authentication.Bucket
		}

		query := fmt.Sprintf(`from(bucket:"%s") |> range(start: -1h) |> filter(fn: (r) => r["_measurement"] == "%s") |> last()`, bucket, measurement)

		r.telemetry.Logger.Debug("Executing V2 measurement query",
			zap.String("measurement", measurement),
			zap.String("bucket", bucket),
			zap.String("query", query))

		result, err := r.v2QueryAPI.Query(ctx, query)
		if err != nil {
			r.telemetry.Logger.Error("Failed to query V2 measurement",
				zap.String("measurement", measurement),
				zap.String("bucket", bucket),
				zap.Error(err))
			continue
		}
		defer result.Close()

		r.telemetry.Logger.Debug("V2 query executed successfully",
			zap.String("measurement", measurement),
			zap.String("bucket", bucket))

		// Process results for this measurement
		recordCount := 0
		for result.Next() {
			recordCount++
			record := result.Record()

			r.telemetry.Logger.Debug("Processing V2 record",
				zap.String("measurement", record.Measurement()),
				zap.String("field", record.Field()),
				zap.Any("value", record.Value()),
				zap.Time("timestamp", record.Time()),
				zap.Any("tags", record.Values()))

			metric := scopeMetrics.Metrics().AppendEmpty()
			metricName := r.applyMetricPrefix(record.Measurement())
			metric.SetName(metricName)

			r.telemetry.Logger.Debug("Created metric",
				zap.String("metricName", metricName),
				zap.String("field", record.Field()),
				zap.Any("value", record.Value()))

			// Determine metric type based on configuration
			metricTypeInfo := r.determineMetricType(record.Measurement(), record.Field())

			r.telemetry.Logger.Debug("Determined metric type",
				zap.String("metricName", metricName),
				zap.String("field", record.Field()),
				zap.String("metricType", string(metricTypeInfo.Type)),
				zap.Bool("isCumulative", metricTypeInfo.IsCumulative),
				zap.Bool("isMonotonic", metricTypeInfo.IsMonotonic))

			// Set metric type based on field type and configuration
			if record.Value() != nil {
				switch v := record.Value().(type) {
				case float64:
					switch metricTypeInfo.Type {
					case MetricTypeCounter:
						// Only set the metric type once, then append data points
						if metric.Type() == pmetric.MetricTypeEmpty {
							metric.SetEmptySum()
							metric.Sum().SetIsMonotonic(true)
						}
						dp := metric.Sum().DataPoints().AppendEmpty()
						dp.SetDoubleValue(v)
						r.telemetry.Logger.Debug("Created counter data point",
							zap.String("metricName", metricName),
							zap.String("field", record.Field()),
							zap.Float64("value", v))
					case MetricTypeHistogram:
						// Only set the metric type once, then append data points
						if metric.Type() == pmetric.MetricTypeEmpty {
							metric.SetEmptyHistogram()
						}
						dp := metric.Histogram().DataPoints().AppendEmpty()
						dp.SetSum(v)
						dp.SetCount(1)
						r.telemetry.Logger.Debug("Created histogram data point",
							zap.String("metricName", metricName),
							zap.String("field", record.Field()),
							zap.Float64("sum", v),
							zap.Uint64("count", 1))
					default: // MetricTypeGauge or unknown
						// Only set the metric type once, then append data points
						if metric.Type() == pmetric.MetricTypeEmpty {
							metric.SetEmptyGauge()
						}
						dp := metric.Gauge().DataPoints().AppendEmpty()
						dp.SetDoubleValue(v)
						r.telemetry.Logger.Debug("Created gauge data point",
							zap.String("metricName", metricName),
							zap.String("field", record.Field()),
							zap.Float64("value", v))
					}
				case int64:
					switch metricTypeInfo.Type {
					case MetricTypeCounter:
						// Only set the metric type once, then append data points
						if metric.Type() == pmetric.MetricTypeEmpty {
							metric.SetEmptySum()
							metric.Sum().SetIsMonotonic(true)
						}
						dp := metric.Sum().DataPoints().AppendEmpty()
						dp.SetIntValue(v)
						r.telemetry.Logger.Debug("Created counter data point",
							zap.String("metricName", metricName),
							zap.String("field", record.Field()),
							zap.Int64("value", v))
					case MetricTypeHistogram:
						// Only set the metric type once, then append data points
						if metric.Type() == pmetric.MetricTypeEmpty {
							metric.SetEmptyHistogram()
						}
						dp := metric.Histogram().DataPoints().AppendEmpty()
						dp.SetSum(float64(v))
						dp.SetCount(1)
						r.telemetry.Logger.Debug("Created histogram data point",
							zap.String("metricName", metricName),
							zap.String("field", record.Field()),
							zap.Float64("sum", float64(v)),
							zap.Uint64("count", 1))
					default: // MetricTypeGauge or unknown
						// Only set the metric type once, then append data points
						if metric.Type() == pmetric.MetricTypeEmpty {
							metric.SetEmptyGauge()
						}
						dp := metric.Gauge().DataPoints().AppendEmpty()
						dp.SetIntValue(v)
						r.telemetry.Logger.Debug("Created gauge data point",
							zap.String("metricName", metricName),
							zap.String("field", record.Field()),
							zap.Int64("value", v))
					}
				case bool:
					// Only set the metric type once, then append data points
					if metric.Type() == pmetric.MetricTypeEmpty {
						metric.SetEmptyGauge()
					}
					dp := metric.Gauge().DataPoints().AppendEmpty()
					if v {
						dp.SetIntValue(1)
					} else {
						dp.SetIntValue(0)
					}
					r.telemetry.Logger.Debug("Created boolean gauge data point",
						zap.String("metricName", metricName),
						zap.String("field", record.Field()),
						zap.Bool("value", v),
						zap.Int64("intValue", func() int64 {
							if v {
								return 1
							} else {
								return 0
							}
						}()))
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
				r.telemetry.Logger.Debug("Set metric timestamp",
					zap.String("metricName", record.Measurement()),
					zap.Time("timestamp", timestamp))
			} else {
				r.telemetry.Logger.Debug("No timestamp available for metric",
					zap.String("metricName", record.Measurement()))
			}

			// Set labels from tags
			attributeCount := 0
			if record.Values() != nil {
				for k, v := range record.Values() {
					if k != "_time" && k != "_value" && k != "_field" && k != "_measurement" {
						attributeCount++
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
						r.telemetry.Logger.Debug("Set metric attribute",
							zap.String("metricName", record.Measurement()),
							zap.String("attributeKey", k),
							zap.String("attributeValue", fmt.Sprintf("%v", v)))
					}
				}
			}
			r.telemetry.Logger.Debug("Completed metric creation",
				zap.String("metricName", record.Measurement()),
				zap.String("field", record.Field()),
				zap.String("metricType", string(metricTypeInfo.Type)),
				zap.Int("attributeCount", attributeCount))
		}

		r.telemetry.Logger.Debug("V2 measurement processing completed",
			zap.String("measurement", measurement),
			zap.Int("recordsProcessed", recordCount))

		if result.Err() != nil {
			r.telemetry.Logger.Error("Error reading V2 measurement results",
				zap.String("measurement", measurement),
				zap.Error(result.Err()))
		}
	}

	r.telemetry.Logger.Debug("V2 metrics discovery and fetch completed",
		zap.Int("totalMeasurements", len(measurements)),
		zap.Int("totalMetrics", metrics.MetricCount()))

	return metrics
}

// discoverV1Measurements discovers all measurements in InfluxDB 1.x
func (r *influxdbReaderReceiver) discoverV1Measurements(ctx context.Context) ([]string, error) {
	r.telemetry.Logger.Debug("Starting V1 measurements discovery", zap.String("database", r.config.Database))

	query := "SHOW MEASUREMENTS"
	r.telemetry.Logger.Debug("V1 discovery query", zap.String("query", query))

	q := client.Query{
		Command:  query,
		Database: r.config.Database,
	}

	response, err := r.v1Client.Query(q)
	if err != nil {
		r.telemetry.Logger.Error("Failed to execute V1 discovery query",
			zap.String("database", r.config.Database),
			zap.Error(err))
		return nil, fmt.Errorf("failed to discover measurements: %w", err)
	}

	if response.Error() != nil {
		r.telemetry.Logger.Error("V1 discovery query returned error",
			zap.String("database", r.config.Database),
			zap.Error(response.Error()))
		return nil, fmt.Errorf("show measurements query returned error: %w", response.Error())
	}

	r.telemetry.Logger.Debug("V1 discovery query executed successfully", zap.String("database", r.config.Database))

	var measurements []string
	measurementCount := 0
	for _, result := range response.Results {
		for _, series := range result.Series {
			for _, point := range series.Values {
				if len(point) > 0 {
					if measurement, ok := point[0].(string); ok {
						measurements = append(measurements, measurement)
						measurementCount++
						r.telemetry.Logger.Debug("Discovered V1 measurement",
							zap.String("measurement", measurement),
							zap.Int("measurementIndex", measurementCount))
					}
				}
			}
		}
	}

	r.telemetry.Logger.Debug("V1 measurements discovery completed successfully",
		zap.String("database", r.config.Database),
		zap.Int("measurementCount", len(measurements)))

	// Describe the structure of each measurement
	r.telemetry.Logger.Debug("Describing measurement structures...")
	for _, measurement := range measurements {
		r.describeMeasurementStructure(ctx, measurement)
	}

	return measurements, nil
}

// discoverV2Measurements discovers all measurements in InfluxDB 2.x
func (r *influxdbReaderReceiver) discoverV2Measurements(ctx context.Context) ([]string, error) {
	r.telemetry.Logger.Info("Starting V2 measurements discovery")

	bucket := r.config.Database
	if r.config.Endpoint.Authentication != nil && r.config.Endpoint.Authentication.Bucket != "" {
		bucket = r.config.Endpoint.Authentication.Bucket
	}

	r.telemetry.Logger.Debug("V2 discovery configuration",
		zap.String("bucket", bucket),
		zap.String("database", r.config.Database))

	query := fmt.Sprintf(`import "influxdata/influxdb/schema" schema.measurements(bucket: "%s")`, bucket)
	r.telemetry.Logger.Debug("V2 discovery query", zap.String("query", query))

	r.telemetry.Logger.Debug("Executing measurement discovery query", zap.String("query", query))

	result, err := r.v2QueryAPI.Query(ctx, query)
	if err != nil {
		r.telemetry.Logger.Error("Failed to execute V2 discovery query",
			zap.String("bucket", bucket),
			zap.Error(err))
		return nil, fmt.Errorf("failed to discover measurements: %w", err)
	}
	defer result.Close()

	r.telemetry.Logger.Debug("V2 discovery query executed successfully", zap.String("bucket", bucket))

	var measurements []string
	recordCount := 0
	for result.Next() {
		recordCount++
		record := result.Record()
		if record.Value() != nil {
			if measurement, ok := record.Value().(string); ok {
				measurements = append(measurements, measurement)
				r.telemetry.Logger.Debug("Discovered measurement",
					zap.String("measurement", measurement),
					zap.Int("recordIndex", recordCount))
			}
		}
	}

	r.telemetry.Logger.Debug("V2 discovery query processing completed",
		zap.String("bucket", bucket),
		zap.Int("recordsProcessed", recordCount),
		zap.Int("measurementsFound", len(measurements)))

	if result.Err() != nil {
		r.telemetry.Logger.Error("Error reading V2 discovery results",
			zap.String("bucket", bucket),
			zap.Error(result.Err()))
		return nil, fmt.Errorf("error reading measurement discovery results: %w", result.Err())
	}

	r.telemetry.Logger.Debug("V2 measurements discovery completed successfully",
		zap.String("bucket", bucket),
		zap.Int("measurementCount", len(measurements)))

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

	r.telemetry.Logger.Debug("Executing InfluxDB 1.x query", zap.String("query", query))

	q := client.Query{
		Command:  query,
		Database: r.config.Database,
	}

	response, err := r.v1Client.Query(q)
	if err != nil {
		r.telemetry.Logger.Error("Failed to execute InfluxDB 1.x query", zap.Error(err))
		return metrics
	}

	if response.Error() != nil {
		r.telemetry.Logger.Error("InfluxDB 1.x query returned error", zap.Error(response.Error()))
		return metrics
	}

	// Process results
	seriesCount := 0
	for _, result := range response.Results {
		for _, series := range result.Series {
			seriesCount++
			r.telemetry.Logger.Debug("Processing V1 series",
				zap.String("seriesName", series.Name),
				zap.Int("seriesIndex", seriesCount),
				zap.Strings("columns", series.Columns),
				zap.Int("pointCount", len(series.Values)))

			metric := scopeMetrics.Metrics().AppendEmpty()
			metric.SetName(series.Name)

			r.telemetry.Logger.Debug("Created V1 metric",
				zap.String("metricName", series.Name),
				zap.Strings("columns", series.Columns))

			// Process each point in the series
			pointCount := 0
			for _, point := range series.Values {
				pointCount++
				if len(point) < 2 {
					r.telemetry.Logger.Debug("Skipping V1 point - insufficient data",
						zap.String("metricName", series.Name),
						zap.Int("pointIndex", pointCount),
						zap.Int("pointLength", len(point)))
					continue
				}

				r.telemetry.Logger.Debug("Processing V1 point",
					zap.String("metricName", series.Name),
					zap.Int("pointIndex", pointCount),
					zap.Any("pointData", point))

				// Parse timestamp
				var timestamp time.Time
				if ts, ok := point[0].(string); ok {
					if t, err := time.Parse(time.RFC3339, ts); err == nil {
						timestamp = t
						r.telemetry.Logger.Debug("Parsed V1 timestamp",
							zap.String("metricName", series.Name),
							zap.Time("timestamp", timestamp))
					} else {
						r.telemetry.Logger.Debug("Failed to parse V1 timestamp",
							zap.String("metricName", series.Name),
							zap.String("timestampString", ts),
							zap.Error(err))
					}
				}

				// Parse value
				if value, ok := point[1].(float64); ok {
					// Only set the metric type once, then append data points
					if metric.Type() == pmetric.MetricTypeEmpty {
						metric.SetEmptyGauge()
					}
					dp := metric.Gauge().DataPoints().AppendEmpty()
					dp.SetDoubleValue(value)

					r.telemetry.Logger.Debug("Created V1 gauge data point",
						zap.String("metricName", series.Name),
						zap.Float64("value", value))

					if !timestamp.IsZero() {
						dp.SetTimestamp(pcommon.Timestamp(timestamp.UnixNano()))
						r.telemetry.Logger.Debug("Set V1 data point timestamp",
							zap.String("metricName", series.Name),
							zap.Time("timestamp", timestamp))
					}

					// Set labels from tags
					attributeCount := 0
					for i, column := range series.Columns {
						if i >= 2 && i < len(point) && column != "time" && column != "value" {
							if val, ok := point[i].(string); ok {
								dp.Attributes().PutStr(column, val)
								attributeCount++
								r.telemetry.Logger.Debug("Set V1 metric attribute",
									zap.String("metricName", series.Name),
									zap.String("attributeKey", column),
									zap.String("attributeValue", val))
							}
						}
					}
					r.telemetry.Logger.Debug("Completed V1 data point creation",
						zap.String("metricName", series.Name),
						zap.Float64("value", value),
						zap.Int("attributeCount", attributeCount))
				} else {
					r.telemetry.Logger.Debug("Skipping V1 point - value not float64",
						zap.String("metricName", series.Name),
						zap.Any("value", point[1]),
						zap.String("valueType", fmt.Sprintf("%T", point[1])))
				}
			}
		}
	}

	r.telemetry.Logger.Debug("V1 metrics fetch completed",
		zap.Int("seriesCount", seriesCount),
		zap.Int("totalMetrics", metrics.MetricCount()),
		zap.Int("resourceCount", metrics.ResourceMetrics().Len()))

	return metrics
}
