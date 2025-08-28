package influxdbreaderreceiver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
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

	// Track last fetch time for incremental polling
	lastFetchTime time.Time
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
	r.logger.Info("Initializing InfluxDB 2.x client",
		zap.String("serverURL", serverURL),
		zap.Duration("timeout", r.config.Timeout),
		zap.Bool("insecure", r.config.Insecure))

	opts := influxdb2.DefaultOptions()
	opts.SetHTTPClient(&http.Client{
		Timeout: r.config.Timeout,
	})

	if r.config.Insecure {
		opts.SetTLSConfig(nil)
		r.logger.Info("TLS verification disabled for InfluxDB 2.x client")
	}

	token := ""
	org := ""
	if r.config.Endpoint.Authentication != nil {
		token = r.config.Endpoint.Authentication.Token
		org = r.config.Endpoint.Authentication.Organization
		r.logger.Info("Using authentication for InfluxDB 2.x",
			zap.String("organization", org),
			zap.Bool("hasToken", token != ""))
	} else {
		r.logger.Info("No authentication configured for InfluxDB 2.x")
	}

	r.v2Client = influxdb2.NewClientWithOptions(serverURL, token, opts)
	r.v2QueryAPI = r.v2Client.QueryAPI(org)

	r.logger.Info("Testing InfluxDB 2.x connection...")
	// Test connection
	_, err := r.v2Client.Ping(context.Background())
	if err != nil {
		r.logger.Error("Failed to ping InfluxDB 2.x server", zap.Error(err))
		return err
	}

	r.logger.Info("Successfully connected to InfluxDB 2.x server")
	return nil
}

// initV1Client initializes the InfluxDB 1.x client
func (r *influxdbReaderReceiver) initV1Client() error {
	serverURL := fmt.Sprintf("%s://%s", r.config.Endpoint.Protocol, r.config.Endpoint.Address)
	r.logger.Info("Initializing InfluxDB 1.x client",
		zap.String("serverURL", serverURL),
		zap.Duration("timeout", r.config.Timeout))

	parsedURL, err := url.Parse(serverURL)
	if err != nil {
		r.logger.Error("Invalid server URL", zap.String("serverURL", serverURL), zap.Error(err))
		return fmt.Errorf("invalid server URL: %w", err)
	}

	config := client.Config{
		URL:     *parsedURL,
		Timeout: r.config.Timeout,
	}

	if r.config.Endpoint.Authentication != nil {
		config.Username = r.config.Endpoint.Authentication.Username
		config.Password = r.config.Endpoint.Authentication.Password
		r.logger.Info("Using authentication for InfluxDB 1.x",
			zap.String("username", config.Username),
			zap.Bool("hasPassword", config.Password != ""))
	} else {
		r.logger.Info("No authentication configured for InfluxDB 1.x")
	}

	var clientErr error
	r.v1Client, clientErr = client.NewClient(config)
	if clientErr != nil {
		r.logger.Error("Failed to create InfluxDB 1.x client", zap.Error(clientErr))
		return clientErr
	}

	r.logger.Info("Testing InfluxDB 1.x connection...")
	// Test connection
	_, _, err = r.v1Client.Ping()
	if err != nil {
		r.logger.Error("Failed to ping InfluxDB 1.x server", zap.Error(err))
		return err
	}

	r.logger.Info("Successfully connected to InfluxDB 1.x server")
	return nil
}

// pollMetrics polls metrics from InfluxDB at regular intervals
func (r *influxdbReaderReceiver) pollMetrics(ctx context.Context) {
	r.logger.Info("Starting metrics polling",
		zap.Duration("interval", r.config.Interval),
		zap.Bool("autoDiscover", r.config.AutoDiscover),
		zap.Bool("useV2API", r.config.UseV2API))

	ticker := time.NewTicker(r.config.Interval)
	defer ticker.Stop()
	defer close(r.done)

	// Poll immediately on start
	r.logger.Info("Performing initial metrics fetch...")
	if err := r.fetchMetrics(ctx); err != nil {
		r.logger.Error("Failed to fetch metrics on startup", zap.Error(err))
	} else {
		r.logger.Info("Initial metrics fetch completed successfully")
	}

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("Polling stopped due to context cancellation")
			return
		case <-ticker.C:
			r.logger.Debug("Starting scheduled metrics fetch...")
			if err := r.fetchMetrics(ctx); err != nil {
				r.logger.Error("Failed to fetch metrics", zap.Error(err))
			} else {
				r.logger.Debug("Scheduled metrics fetch completed successfully")
			}
		}
	}
}

// fetchMetrics fetches metrics from InfluxDB and sends them to the consumer
func (r *influxdbReaderReceiver) fetchMetrics(ctx context.Context) error {
	r.logger.Debug("Starting metrics fetch",
		zap.Bool("autoDiscover", r.config.AutoDiscover),
		zap.Bool("useV2API", r.config.UseV2API))

	var metrics pmetric.Metrics

	if r.config.AutoDiscover {
		r.logger.Info("Using auto-discovery mode to fetch metrics")
		// Auto-discover measurements and fetch latest metrics
		if r.config.UseV2API {
			r.logger.Debug("Fetching V2 metrics with discovery")
			metrics = r.fetchV2MetricsWithDiscovery(ctx)
		} else {
			r.logger.Debug("Fetching V1 metrics with discovery")
			metrics = r.fetchV1MetricsWithDiscovery(ctx)
		}
	} else {
		r.logger.Info("Using custom query mode to fetch metrics")
		// Use custom query
		if r.config.UseV2API {
			r.logger.Debug("Fetching V2 metrics with custom query")
			metrics = r.fetchV2Metrics(ctx)
		} else {
			r.logger.Debug("Fetching V1 metrics with custom query")
			metrics = r.fetchV1Metrics(ctx)
		}
	}

	metricCount := metrics.MetricCount()
	r.logger.Info("Metrics fetch completed",
		zap.Int("metricCount", metricCount),
		zap.Int("resourceCount", metrics.ResourceMetrics().Len()))

	if metricCount > 0 {
		r.logger.Debug("Sending metrics to consumer", zap.Int("metricCount", metricCount))
		if err := r.consumer.ConsumeMetrics(ctx, metrics); err != nil {
			r.logger.Error("Failed to send metrics to consumer", zap.Error(err))
			return err
		}
		r.logger.Info("Successfully sent metrics to consumer", zap.Int("metricCount", metricCount))
	} else {
		r.logger.Warn("No metrics found to send to consumer")
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

	// For each measurement, fetch data since last fetch time
	r.logger.Info("Starting to process measurements", zap.Int("totalMeasurements", len(measurements)))

	// Track the latest timestamp across all measurements for next fetch
	var latestTimestamp time.Time

	for i, measurement := range measurements {
		r.logger.Info("Processing measurement",
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
			r.logger.Debug("Executing V1 measurement query (first run - historical)",
				zap.String("measurement", measurement),
				zap.String("query", query),
				zap.Time("startTime", startTime),
				zap.Duration("interval", r.config.Interval))
		} else {
			// Subsequent runs: get data since last fetch
			query = fmt.Sprintf("SELECT * FROM \"%s\" WHERE time > '%s' ORDER BY time ASC",
				measurement, r.lastFetchTime.Format(time.RFC3339))
			r.logger.Debug("Executing V1 measurement query (incremental)",
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
			r.logger.Error("Failed to query V1 measurement",
				zap.String("measurement", measurement),
				zap.Error(err))
			continue
		}

		if response.Error() != nil {
			r.logger.Error("V1 query returned error",
				zap.String("measurement", measurement),
				zap.Error(response.Error()))
			continue
		}

		r.logger.Debug("V1 query executed successfully",
			zap.String("measurement", measurement))

		// Process results for this measurement
		seriesCount := 0
		for _, result := range response.Results {
			for _, series := range result.Series {
				seriesCount++
				r.logger.Debug("Processing V1 series",
					zap.String("measurement", measurement),
					zap.String("seriesName", series.Name),
					zap.Int("seriesIndex", seriesCount),
					zap.Strings("columns", series.Columns),
					zap.Int("pointCount", len(series.Values)))

				// Analyze columns to identify numeric vs string columns
				var numericColumns []string
				var stringColumns []string

				if len(series.Values) > 0 {
					firstRow := series.Values[0]
					for i, value := range firstRow {
						if i < len(series.Columns) {
							columnName := series.Columns[i]
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

				r.logger.Debug("Column analysis for measurement",
					zap.String("measurement", measurement),
					zap.Strings("numericColumns", numericColumns),
					zap.Strings("stringColumns", stringColumns))

				// Create one metric per numeric column
				for _, numericColumn := range numericColumns {
					metricName := fmt.Sprintf("%s_%s", measurement, numericColumn)
					metric := scopeMetrics.Metrics().AppendEmpty()
					metric.SetName(metricName)
					metric.SetDescription(fmt.Sprintf("Metric %s from InfluxDB measurement: %s", numericColumn, measurement))

					// Determine metric type based on the measurement name
					metricTypeInfo := r.determineMetricType(measurement, numericColumn)

					r.logger.Debug("Creating metric for numeric column",
						zap.String("measurement", measurement),
						zap.String("numericColumn", numericColumn),
						zap.String("metricName", metricName),
						zap.String("metricType", string(metricTypeInfo.Type)))

					// Process each point in the series
					pointCount := 0
					for _, point := range series.Values {
						pointCount++
						if len(point) < 2 {
							r.logger.Debug("Skipping V1 point - insufficient data",
								zap.String("measurement", measurement),
								zap.String("numericColumn", numericColumn),
								zap.Int("pointIndex", pointCount),
								zap.Int("pointLength", len(point)))
							continue
						}

						r.logger.Debug("Processing V1 point for column",
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
							r.logger.Warn("Column not found or index out of bounds",
								zap.String("measurement", measurement),
								zap.String("numericColumn", numericColumn),
								zap.Int("columnIndex", columnIndex),
								zap.Int("pointLength", len(point)))
							continue
						}

						// Parse timestamp
						var timestamp time.Time
						if ts, ok := point[0].(string); ok {
							if t, err := time.Parse(time.RFC3339, ts); err == nil {
								timestamp = t
								r.logger.Debug("Parsed V1 timestamp",
									zap.String("measurement", measurement),
									zap.String("numericColumn", numericColumn),
									zap.Time("timestamp", timestamp))
							} else {
								r.logger.Debug("Failed to parse V1 timestamp",
									zap.String("measurement", measurement),
									zap.String("numericColumn", numericColumn),
									zap.String("timestampString", ts),
									zap.Error(err))
							}
						}

						// Parse value from the specific numeric column
						var dataPoint pmetric.NumberDataPoint
						switch metricTypeInfo.Type {
						case MetricTypeCounter:
							dp := metric.SetEmptySum().DataPoints().AppendEmpty()
							metric.Sum().SetIsMonotonic(true)
							dataPoint = dp
						case MetricTypeHistogram:
							dp := metric.SetEmptyHistogram().DataPoints().AppendEmpty()
							// For now, we'll create a simple histogram from a single value
							if val, ok := point[columnIndex].(float64); ok {
								dp.SetSum(val)
								dp.SetCount(1)
								r.logger.Debug("Created V1 histogram data point",
									zap.String("measurement", measurement),
									zap.String("numericColumn", numericColumn),
									zap.Float64("sum", val),
									zap.Uint64("count", 1))
							}
							continue // Skip the regular data point creation for histograms
						default: // MetricTypeGauge or unknown
							dp := metric.SetEmptyGauge().DataPoints().AppendEmpty()
							dataPoint = dp
						}

						// Set value from the specific numeric column
						// Handle nil values by converting to 0, otherwise use the actual value
						var value interface{}
						if point[columnIndex] == nil {
							r.logger.Debug("Converting nil value to 0 for numeric column",
								zap.String("measurement", measurement),
								zap.String("numericColumn", numericColumn))
							value = float64(0)
						} else {
							value = point[columnIndex]
						}

						switch v := value.(type) {
						case float64:
							dataPoint.SetDoubleValue(v)
							r.logger.Debug("Set V1 data point double value",
								zap.String("measurement", measurement),
								zap.String("numericColumn", numericColumn),
								zap.Float64("value", v))
						case int64:
							dataPoint.SetIntValue(v)
							r.logger.Debug("Set V1 data point int value",
								zap.String("measurement", measurement),
								zap.String("numericColumn", numericColumn),
								zap.Int64("value", v))
						case int:
							dataPoint.SetIntValue(int64(v))
							r.logger.Debug("Set V1 data point int value",
								zap.String("measurement", measurement),
								zap.String("numericColumn", numericColumn),
								zap.Int("value", v))
						case bool:
							if v {
								dataPoint.SetIntValue(1)
							} else {
								dataPoint.SetIntValue(0)
							}
							r.logger.Debug("Set V1 data point bool value",
								zap.String("measurement", measurement),
								zap.String("numericColumn", numericColumn),
								zap.Bool("value", v))
						case json.Number:
							// Convert json.Number to float64
							if floatVal, err := v.Float64(); err == nil {
								dataPoint.SetDoubleValue(floatVal)
								r.logger.Debug("Set V1 data point json.Number value",
									zap.String("measurement", measurement),
									zap.String("numericColumn", numericColumn),
									zap.Float64("value", floatVal))
							} else {
								r.logger.Warn("Failed to convert json.Number to float64",
									zap.String("measurement", measurement),
									zap.String("numericColumn", numericColumn),
									zap.String("value", v.String()),
									zap.Error(err))
								continue
							}
						default:
							r.logger.Warn("Unsupported V1 data point value type",
								zap.String("measurement", measurement),
								zap.String("numericColumn", numericColumn),
								zap.Any("value", v),
								zap.String("type", fmt.Sprintf("%T", v)))
							continue
						}

						// Set timestamp
						if !timestamp.IsZero() {
							dataPoint.SetTimestamp(pcommon.Timestamp(timestamp.UnixNano()))
							r.logger.Debug("Set V1 data point timestamp",
								zap.String("measurement", measurement),
								zap.String("numericColumn", numericColumn),
								zap.Time("timestamp", timestamp))
						} else {
							r.logger.Debug("No timestamp available for V1 data point",
								zap.String("measurement", measurement),
								zap.String("numericColumn", numericColumn))
						}

						// Set attributes from string columns (tags)
						attributeCount := 0
						for i, column := range series.Columns {
							if i < len(point) && column != "time" && column != numericColumn {
								// Only add string values as attributes
								if val, ok := point[i].(string); ok {
									dataPoint.Attributes().PutStr(column, val)
									attributeCount++
									r.logger.Debug("Set V1 metric attribute",
										zap.String("measurement", measurement),
										zap.String("numericColumn", numericColumn),
										zap.String("attributeKey", column),
										zap.String("attributeValue", val))
								} else {
									r.logger.Debug("Skipping non-string attribute",
										zap.String("measurement", measurement),
										zap.String("numericColumn", numericColumn),
										zap.String("attributeKey", column),
										zap.Any("attributeValue", point[i]),
										zap.String("valueType", fmt.Sprintf("%T", point[i])))
								}
							}
						}

						r.logger.Debug("Completed V1 data point creation for column",
							zap.String("measurement", measurement),
							zap.String("numericColumn", numericColumn),
							zap.Int("pointIndex", pointCount),
							zap.Int("attributeCount", attributeCount))
					}

					r.logger.Debug("Added V1 metric to resource",
						zap.String("measurement", measurement),
						zap.String("numericColumn", numericColumn),
						zap.String("metricName", metricName),
						zap.String("metricType", string(metricTypeInfo.Type)))
				}
			}
		}

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

		r.logger.Info("V1 measurement processing completed",
			zap.String("measurement", measurement),
			zap.Int("seriesCount", seriesCount))
	}

	// Update the last fetch time for next iteration
	if !latestTimestamp.IsZero() {
		r.lastFetchTime = latestTimestamp
		r.logger.Debug("Updated last fetch time",
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
		r.logger.Error("Failed to describe measurement structure",
			zap.String("measurement", measurement),
			zap.Error(err))
		return
	}

	if response.Error() != nil {
		r.logger.Error("Describe measurement structure query returned error",
			zap.String("measurement", measurement),
			zap.Error(response.Error()))
		return
	}

	// Process results to describe the structure
	for _, result := range response.Results {
		for _, series := range result.Series {
			r.logger.Info("Measurement structure",
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

				r.logger.Info("Measurement data types",
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

				r.logger.Info("Measurement column analysis",
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
	r.logger.Info("Starting V2 metrics discovery and fetch")

	metrics := pmetric.NewMetrics()
	resourceMetrics := metrics.ResourceMetrics().AppendEmpty()
	scopeMetrics := resourceMetrics.ScopeMetrics().AppendEmpty()

	// First, discover all measurements
	r.logger.Info("Discovering V2 measurements...")
	measurements, err := r.discoverV2Measurements(ctx)
	if err != nil {
		r.logger.Error("Failed to discover V2 measurements", zap.Error(err))
		return metrics
	}

	r.logger.Info("V2 measurements discovery completed",
		zap.Int("measurementCount", len(measurements)),
		zap.Strings("measurements", measurements))

	// For each measurement, fetch the latest data point
	r.logger.Info("Starting to process measurements", zap.Int("totalMeasurements", len(measurements)))

	for i, measurement := range measurements {
		r.logger.Info("Processing measurement",
			zap.Int("measurementIndex", i+1),
			zap.Int("totalMeasurements", len(measurements)),
			zap.String("measurement", measurement))

		bucket := r.config.Database
		if r.config.Endpoint.Authentication != nil && r.config.Endpoint.Authentication.Bucket != "" {
			bucket = r.config.Endpoint.Authentication.Bucket
		}

		query := fmt.Sprintf(`from(bucket:"%s") |> range(start: -1h) |> filter(fn: (r) => r["_measurement"] == "%s") |> last()`, bucket, measurement)

		r.logger.Debug("Executing V2 measurement query",
			zap.String("measurement", measurement),
			zap.String("bucket", bucket),
			zap.String("query", query))

		result, err := r.v2QueryAPI.Query(ctx, query)
		if err != nil {
			r.logger.Error("Failed to query V2 measurement",
				zap.String("measurement", measurement),
				zap.String("bucket", bucket),
				zap.Error(err))
			continue
		}
		defer result.Close()

		r.logger.Debug("V2 query executed successfully",
			zap.String("measurement", measurement),
			zap.String("bucket", bucket))

		// Process results for this measurement
		recordCount := 0
		for result.Next() {
			recordCount++
			record := result.Record()

			r.logger.Debug("Processing V2 record",
				zap.String("measurement", record.Measurement()),
				zap.String("field", record.Field()),
				zap.Any("value", record.Value()),
				zap.Time("timestamp", record.Time()),
				zap.Any("tags", record.Values()))

			metric := scopeMetrics.Metrics().AppendEmpty()
			metric.SetName(record.Measurement())

			r.logger.Debug("Created metric",
				zap.String("metricName", record.Measurement()),
				zap.String("field", record.Field()),
				zap.Any("value", record.Value()))

			// Determine metric type based on configuration
			metricTypeInfo := r.determineMetricType(record.Measurement(), record.Field())

			r.logger.Debug("Determined metric type",
				zap.String("metricName", record.Measurement()),
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
						dp := metric.SetEmptySum().DataPoints().AppendEmpty()
						dp.SetDoubleValue(v)
						metric.Sum().SetIsMonotonic(true)
						r.logger.Debug("Created counter data point",
							zap.String("metricName", record.Measurement()),
							zap.String("field", record.Field()),
							zap.Float64("value", v))
					case MetricTypeHistogram:
						dp := metric.SetEmptyHistogram().DataPoints().AppendEmpty()
						dp.SetSum(v)
						dp.SetCount(1)
						r.logger.Debug("Created histogram data point",
							zap.String("metricName", record.Measurement()),
							zap.String("field", record.Field()),
							zap.Float64("sum", v),
							zap.Uint64("count", 1))
					default: // MetricTypeGauge or unknown
						dp := metric.SetEmptyGauge().DataPoints().AppendEmpty()
						dp.SetDoubleValue(v)
						r.logger.Debug("Created gauge data point",
							zap.String("metricName", record.Measurement()),
							zap.String("field", record.Field()),
							zap.Float64("value", v))
					}
				case int64:
					switch metricTypeInfo.Type {
					case MetricTypeCounter:
						dp := metric.SetEmptySum().DataPoints().AppendEmpty()
						dp.SetIntValue(v)
						metric.Sum().SetIsMonotonic(true)
						r.logger.Debug("Created counter data point",
							zap.String("metricName", record.Measurement()),
							zap.String("field", record.Field()),
							zap.Int64("value", v))
					case MetricTypeHistogram:
						dp := metric.SetEmptyHistogram().DataPoints().AppendEmpty()
						dp.SetSum(float64(v))
						dp.SetCount(1)
						r.logger.Debug("Created histogram data point",
							zap.String("metricName", record.Measurement()),
							zap.String("field", record.Field()),
							zap.Float64("sum", float64(v)),
							zap.Uint64("count", 1))
					default: // MetricTypeGauge or unknown
						dp := metric.SetEmptyGauge().DataPoints().AppendEmpty()
						dp.SetIntValue(v)
						r.logger.Debug("Created gauge data point",
							zap.String("metricName", record.Measurement()),
							zap.String("field", record.Field()),
							zap.Int64("value", v))
					}
				case bool:
					dp := metric.SetEmptyGauge().DataPoints().AppendEmpty()
					if v {
						dp.SetIntValue(1)
					} else {
						dp.SetIntValue(0)
					}
					r.logger.Debug("Created boolean gauge data point",
						zap.String("metricName", record.Measurement()),
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
				r.logger.Debug("Set metric timestamp",
					zap.String("metricName", record.Measurement()),
					zap.Time("timestamp", timestamp))
			} else {
				r.logger.Debug("No timestamp available for metric",
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
						r.logger.Debug("Set metric attribute",
							zap.String("metricName", record.Measurement()),
							zap.String("attributeKey", k),
							zap.String("attributeValue", fmt.Sprintf("%v", v)))
					}
				}
			}
			r.logger.Debug("Completed metric creation",
				zap.String("metricName", record.Measurement()),
				zap.String("field", record.Field()),
				zap.String("metricType", string(metricTypeInfo.Type)),
				zap.Int("attributeCount", attributeCount))
		}

		r.logger.Info("V2 measurement processing completed",
			zap.String("measurement", measurement),
			zap.Int("recordsProcessed", recordCount))

		if result.Err() != nil {
			r.logger.Error("Error reading V2 measurement results",
				zap.String("measurement", measurement),
				zap.Error(result.Err()))
		}
	}

	r.logger.Info("V2 metrics discovery and fetch completed",
		zap.Int("totalMeasurements", len(measurements)),
		zap.Int("totalMetrics", metrics.MetricCount()))

	return metrics
}

// discoverV1Measurements discovers all measurements in InfluxDB 1.x
func (r *influxdbReaderReceiver) discoverV1Measurements(ctx context.Context) ([]string, error) {
	r.logger.Info("Starting V1 measurements discovery", zap.String("database", r.config.Database))

	query := "SHOW MEASUREMENTS"
	r.logger.Debug("V1 discovery query", zap.String("query", query))

	q := client.Query{
		Command:  query,
		Database: r.config.Database,
	}

	response, err := r.v1Client.Query(q)
	if err != nil {
		r.logger.Error("Failed to execute V1 discovery query",
			zap.String("database", r.config.Database),
			zap.Error(err))
		return nil, fmt.Errorf("failed to discover measurements: %w", err)
	}

	if response.Error() != nil {
		r.logger.Error("V1 discovery query returned error",
			zap.String("database", r.config.Database),
			zap.Error(response.Error()))
		return nil, fmt.Errorf("show measurements query returned error: %w", response.Error())
	}

	r.logger.Debug("V1 discovery query executed successfully", zap.String("database", r.config.Database))

	var measurements []string
	measurementCount := 0
	for _, result := range response.Results {
		for _, series := range result.Series {
			for _, point := range series.Values {
				if len(point) > 0 {
					if measurement, ok := point[0].(string); ok {
						measurements = append(measurements, measurement)
						measurementCount++
						r.logger.Debug("Discovered V1 measurement",
							zap.String("measurement", measurement),
							zap.Int("measurementIndex", measurementCount))
					}
				}
			}
		}
	}

	r.logger.Info("V1 measurements discovery completed successfully",
		zap.String("database", r.config.Database),
		zap.Int("measurementCount", len(measurements)))

	// Describe the structure of each measurement
	r.logger.Info("Describing measurement structures...")
	for _, measurement := range measurements {
		r.describeMeasurementStructure(ctx, measurement)
	}

	return measurements, nil
}

// discoverV2Measurements discovers all measurements in InfluxDB 2.x
func (r *influxdbReaderReceiver) discoverV2Measurements(ctx context.Context) ([]string, error) {
	r.logger.Info("Starting V2 measurements discovery")

	bucket := r.config.Database
	if r.config.Endpoint.Authentication != nil && r.config.Endpoint.Authentication.Bucket != "" {
		bucket = r.config.Endpoint.Authentication.Bucket
	}

	r.logger.Debug("V2 discovery configuration",
		zap.String("bucket", bucket),
		zap.String("database", r.config.Database))

	query := fmt.Sprintf(`import "influxdata/influxdb/schema" schema.measurements(bucket: "%s")`, bucket)
	r.logger.Debug("V2 discovery query", zap.String("query", query))

	r.logger.Debug("Executing measurement discovery query", zap.String("query", query))

	result, err := r.v2QueryAPI.Query(ctx, query)
	if err != nil {
		r.logger.Error("Failed to execute V2 discovery query",
			zap.String("bucket", bucket),
			zap.Error(err))
		return nil, fmt.Errorf("failed to discover measurements: %w", err)
	}
	defer result.Close()

	r.logger.Debug("V2 discovery query executed successfully", zap.String("bucket", bucket))

	var measurements []string
	recordCount := 0
	for result.Next() {
		recordCount++
		record := result.Record()
		if record.Value() != nil {
			if measurement, ok := record.Value().(string); ok {
				measurements = append(measurements, measurement)
				r.logger.Debug("Discovered measurement",
					zap.String("measurement", measurement),
					zap.Int("recordIndex", recordCount))
			}
		}
	}

	r.logger.Info("V2 discovery query processing completed",
		zap.String("bucket", bucket),
		zap.Int("recordsProcessed", recordCount),
		zap.Int("measurementsFound", len(measurements)))

	if result.Err() != nil {
		r.logger.Error("Error reading V2 discovery results",
			zap.String("bucket", bucket),
			zap.Error(result.Err()))
		return nil, fmt.Errorf("error reading measurement discovery results: %w", result.Err())
	}

	r.logger.Info("V2 measurements discovery completed successfully",
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
	seriesCount := 0
	for _, result := range response.Results {
		for _, series := range result.Series {
			seriesCount++
			r.logger.Debug("Processing V1 series",
				zap.String("seriesName", series.Name),
				zap.Int("seriesIndex", seriesCount),
				zap.Strings("columns", series.Columns),
				zap.Int("pointCount", len(series.Values)))

			metric := scopeMetrics.Metrics().AppendEmpty()
			metric.SetName(series.Name)

			r.logger.Debug("Created V1 metric",
				zap.String("metricName", series.Name),
				zap.Strings("columns", series.Columns))

			// Process each point in the series
			pointCount := 0
			for _, point := range series.Values {
				pointCount++
				if len(point) < 2 {
					r.logger.Debug("Skipping V1 point - insufficient data",
						zap.String("metricName", series.Name),
						zap.Int("pointIndex", pointCount),
						zap.Int("pointLength", len(point)))
					continue
				}

				r.logger.Debug("Processing V1 point",
					zap.String("metricName", series.Name),
					zap.Int("pointIndex", pointCount),
					zap.Any("pointData", point))

				// Parse timestamp
				var timestamp time.Time
				if ts, ok := point[0].(string); ok {
					if t, err := time.Parse(time.RFC3339, ts); err == nil {
						timestamp = t
						r.logger.Debug("Parsed V1 timestamp",
							zap.String("metricName", series.Name),
							zap.Time("timestamp", timestamp))
					} else {
						r.logger.Debug("Failed to parse V1 timestamp",
							zap.String("metricName", series.Name),
							zap.String("timestampString", ts),
							zap.Error(err))
					}
				}

				// Parse value
				if value, ok := point[1].(float64); ok {
					dp := metric.SetEmptyGauge().DataPoints().AppendEmpty()
					dp.SetDoubleValue(value)

					r.logger.Debug("Created V1 gauge data point",
						zap.String("metricName", series.Name),
						zap.Float64("value", value))

					if !timestamp.IsZero() {
						dp.SetTimestamp(pcommon.Timestamp(timestamp.UnixNano()))
						r.logger.Debug("Set V1 data point timestamp",
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
								r.logger.Debug("Set V1 metric attribute",
									zap.String("metricName", series.Name),
									zap.String("attributeKey", column),
									zap.String("attributeValue", val))
							}
						}
					}
					r.logger.Debug("Completed V1 data point creation",
						zap.String("metricName", series.Name),
						zap.Float64("value", value),
						zap.Int("attributeCount", attributeCount))
				} else {
					r.logger.Debug("Skipping V1 point - value not float64",
						zap.String("metricName", series.Name),
						zap.Any("value", point[1]),
						zap.String("valueType", fmt.Sprintf("%T", point[1])))
				}
			}
		}
	}

	r.logger.Info("V1 metrics fetch completed",
		zap.Int("seriesCount", seriesCount),
		zap.Int("totalMetrics", metrics.MetricCount()),
		zap.Int("resourceCount", metrics.ResourceMetrics().Len()))

	return metrics
}
