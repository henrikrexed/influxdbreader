# InfluxDB Reader Receiver

This is an OpenTelemetry Collector receiver that connects to an InfluxDB database and pulls metrics periodically. It supports both InfluxDB 1.x and 2.x APIs.

| Status                   | Stability Level | Distributions |
| ------------------------ | --------------- | ------------- |
| ![Alpha](https://img.shields.io/badge/Stability-Alpha-orange) | Alpha | [contrib] |

**Status**: This receiver is in **Alpha** stage and is not yet stable. Breaking changes may occur in future releases.

**Stability**: The receiver is marked as Alpha for metrics. This means:
- The API may change in future releases
- Breaking changes are possible
- Not recommended for production use without thorough testing
- Feedback and contributions are welcome

**Distributions**: Available in the [contrib distribution](https://github.com/open-telemetry/opentelemetry-collector-contrib) of the OpenTelemetry Collector.

### Component Metadata

The receiver includes a `metadata.yaml` file that defines its status and stability information. This metadata is used by the OpenTelemetry Collector to:

- Display component status in documentation
- Generate stability badges and tables
- Provide information about component maturity
- Guide users on production readiness

The metadata follows the [OpenTelemetry Collector Contrib metadata specification](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/cmd/mdatagen/README.md).

### Generating Status Table

You can regenerate the status table from the metadata using:

```bash
make generate-status-table
```

This will read the `receiver/influxdbreaderreceiver/metadata.yaml` file and generate the status table shown above.

## Overview

The InfluxDB Reader Receiver is designed to **read existing metrics from InfluxDB** and convert them into OpenTelemetry format for further processing and export to various backends. This is particularly useful when you have:

- **Existing InfluxDB deployments** with historical or real-time metrics
- **Applications** that write directly to InfluxDB
- **Other monitoring agents** (like Telegraf, Prometheus, etc.) that populate InfluxDB
- **Legacy systems** that need to be integrated into modern observability pipelines

## How It Works

### Core Logic

1. **Periodic Polling**: The receiver connects to InfluxDB at configurable intervals
2. **Data Discovery**: Automatically discovers all measurements in the database
3. **Metrics Retrieval**: Fetches the latest metrics from each measurement
4. **Format Conversion**: Converts InfluxDB data points to OpenTelemetry metrics
5. **Pipeline Integration**: Sends converted metrics through the OpenTelemetry processing pipeline

### Metric Type Handling

**Important**: The receiver can now determine the correct OpenTelemetry metric type based on configuration rules, measurement names, and field patterns. This allows for proper conversion of cumulative counters, gauges, and histograms.

#### Automatic Metric Type Detection

The receiver supports intelligent metric type mapping based on common naming conventions:

- **Counters**: Often end with `_total`, `_count`, or `_bytes` (e.g., `http_requests_total`, `errors_count`)
- **Gauges**: Usually descriptive names like `cpu_usage`, `memory_free`, `temperature`
- **Histograms**: Typically have suffixes like `_bucket`, `_sum`, `_quantile` (e.g., `response_time_bucket`)

The receiver automatically detects these patterns and maps them to the appropriate OpenTelemetry metric types.

#### InfluxDB vs OpenTelemetry Metric Types

| InfluxDB Data | OpenTelemetry Output | Notes |
|---------------|---------------------|-------|
| Cumulative metrics (e.g., counters) | Counter (Cumulative) | Proper counter type with cumulative flag |
| Delta metrics (e.g., rate changes) | Gauge | Current value at polling time |
| Gauge metrics | Gauge | Direct conversion |
| Histograms | Histogram | Proper histogram type with buckets |

#### Configuration-Based Type Mapping

The receiver supports both **simplified** and **advanced** configuration approaches:

##### **Simple Configuration (Recommended)**
```yaml
metric_types:
  # Enable automatic detection based on naming patterns
  auto_detect: true
  
  # Override specific measurements if needed
  overrides:
    memory_free: "gauge"
    cpu_usage: "gauge"
    http_requests_total: "counter"
  
  # Default type when auto-detection fails
  default_type: "gauge"
```

##### **Advanced Configuration (Full Control)**
```yaml
metric_type_mapping:
  default_type: "gauge"
  
  # Specific measurement rules
  measurement_rules:
    - measurement: "cpu_usage"
      metric_type: "gauge"
      is_cumulative: false
      is_monotonic: false
    - measurement: "http_requests_total"
      metric_type: "counter"
      is_cumulative: true
      is_monotonic: true
  
  # Field name pattern rules (regex)
  field_rules:
    - field_pattern: ".*_total$"
      metric_type: "counter"
      is_cumulative: true
      is_monotonic: true
    - field_pattern: ".*_usage$"
      metric_type: "gauge"
      is_cumulative: false
      is_monotonic: false
    - field_pattern: ".*_bucket$"
      metric_type: "histogram"
      is_cumulative: false
      is_monotonic: false
  
  # Measurement name pattern rules (regex)
  pattern_rules:
    - measurement_pattern: ".*_total$"
      metric_type: "counter"
      is_cumulative: true
      is_monotonic: true
```

#### Why This Matters

- **Cumulative Metrics**: With proper metric type mapping, cumulative counters (like `cpu_usage_total`) are converted to OpenTelemetry Sum metrics with the cumulative flag, preserving their counter nature.
- **Delta Metrics**: Delta metrics (like rate changes) are converted to gauges showing the current rate value.
- **Proper Type Preservation**: The receiver now preserves the semantic meaning of your metrics, making downstream processing more accurate.
- **Reduced Processing Overhead**: No need for post-processing to convert gauge values back to counters.

#### Example Scenarios

**Scenario 1: Cumulative Counters**
```yaml
# InfluxDB stores: http_requests_total = 1500 (cumulative)
# Receiver outputs: Counter metric with cumulative=true, monotonic=true
# Result: Proper counter type preserved
```

**Scenario 2: Gauge Metrics**
```yaml
# InfluxDB stores: cpu_usage = 75.2 (current usage percentage)
# Receiver outputs: Gauge metric
# Result: Proper gauge type for current values
```

**Scenario 3: Histogram Data**
```yaml
# InfluxDB stores: response_time_bucket with buckets [0.1, 0.5, 1.0, 2.0]
# Receiver outputs: Histogram metric with proper bucket structure
# Result: Proper histogram type for distribution data
```

#### Recommended Processors

Based on your InfluxDB data types, consider using these OpenTelemetry processors:

**For Cumulative Metrics (Counters)**
```yaml
processors:
  # Convert cumulative gauges to deltas
  cumulativetodelta:
    include:
      metric_names:
        - "cpu_usage_total"
        - "http_requests_total"
        - "disk_io_total"
```

**For Rate Calculations**
```yaml
processors:
  # Convert to rates per second
  transform:
    metric_statements:
      - context: metric
        statements:
          - set(metric.type, "gauge")
          - set(attributes["rate_per_second"], true)
```

**For Filtering by Metric Type**
```yaml
processors:
  # Only process specific metric patterns
  filter:
    metrics:
      include:
        match_type: regexp
        metric_names:
          - ".*_total$"  # Cumulative counters
          - ".*_rate$"   # Rate metrics
```

### Data Flow

```
InfluxDB (existing data) → InfluxDB Reader Receiver → OpenTelemetry Pipeline → Backend Systems
```

### Use Cases

- **Migration**: Migrate from InfluxDB to other observability backends
- **Integration**: Integrate InfluxDB data into OpenTelemetry-based observability stacks
- **Aggregation**: Combine InfluxDB metrics with other telemetry data
- **Transformation**: Apply OpenTelemetry processors to InfluxDB data
- **Routing**: Send InfluxDB data to multiple destinations

## Requirements

### Prerequisites

- **InfluxDB 1.x or 2.x** instance with existing data
- **Network access** to the InfluxDB server
- **Authentication credentials** (if enabled)
- **OpenTelemetry Collector** (built with this receiver)

### Data Requirements

- **Existing measurements** in InfluxDB (the receiver reads, doesn't write)
- **Compatible data format** (time-series metrics)
- **Sufficient permissions** to read from the database/bucket

## Features

- **Periodic Polling**: Pulls metrics from InfluxDB at configurable intervals
- **Dual API Support**: Supports both InfluxDB 1.x and 2.x APIs
- **Flexible Authentication**: Supports username/password and token-based authentication
- **Auto-Discovery**: Automatically discovers all measurements and fetches latest metrics
- **Custom Queries**: Allows custom InfluxQL/Flux queries (optional)
- **Multiple Protocols**: Supports HTTP, HTTPS, and UDP connections
- **Complete Collector**: Built with OCB including OTLP, Prometheus, FileLog, and K8sObjects receivers
- **Advanced Processing**: Includes batch, memory limiter, resource, transform, and filter processors

## Configuration

The receiver supports the following configuration structure:

```yaml
receivers:
  influxdbreader:
    endpoint:
      address: "10.0.0.146:8086"  # InfluxDB server address (host:port)
      protocol: "http"            # Protocol: http, https, udp
      authentication:
        username: "test"          # Username for InfluxDB 1.x
        password: "deded"         # Password for InfluxDB 1.x
        token: "your-token"       # Token for InfluxDB 2.x
        organization: "my-org"    # Organization for InfluxDB 2.x
        bucket: "my-bucket"       # Bucket for InfluxDB 2.x
    database: "mondb"             # Database name (InfluxDB 1.x) or bucket (2.x)
    interval: "10s"               # Polling interval
    auto_discover: true           # Auto-discover measurements (default: true)
    query: "SELECT * FROM /.*/"   # Custom query (optional, used when auto_discover: false)
    timeout: "30s"                # Request timeout
    insecure: false               # Skip TLS verification
    use_v2_api: false             # Use InfluxDB 2.x API (default: false for 1.x)
    prefix: ""                    # Prefix to prepend to all metric names (optional)
```

### Configuration Parameters

#### Endpoint Configuration
- **address** (required): The InfluxDB server address in `host:port` format
- **protocol** (optional): The protocol to use (`http`, `https`, `udp`). Default: `http`
- **authentication** (optional): Authentication configuration

#### Authentication Configuration
- **username** (optional): Username for InfluxDB 1.x authentication
- **password** (optional): Password for InfluxDB 1.x authentication
- **token** (optional): Token for InfluxDB 2.x authentication
- **organization** (optional): Organization name for InfluxDB 2.x
- **bucket** (optional): Bucket name for InfluxDB 2.x

#### General Configuration
- **database** (required): Database name for InfluxDB 1.x or bucket name for 2.x
- **interval** (optional): Polling interval. Default: `30s`
- **auto_discover** (optional): Auto-discover measurements and fetch latest metrics. Default: `true`
- **query** (optional): Custom query to execute (used when auto_discover is false). Default: `SELECT * FROM /.*/`
- **timeout** (optional): Request timeout. Default: `30s`
- **insecure** (optional): Skip TLS verification. Default: `false`
- **use_v2_api** (optional): Use InfluxDB 2.x API. Default: `false`
- **prefix** (optional): Prefix to prepend to all metric names. If empty, no prefix is added. Default: `""`

#### Metric Type Configuration
- **metric_types** (optional): Simplified configuration for automatic metric type detection
  - **auto_detect** (optional): Enable automatic detection based on naming patterns. Default: `false`
  - **overrides** (optional): Map specific measurement names to metric types
  - **default_type** (optional): Default metric type when auto-detection fails. Options: `gauge`, `counter`, `histogram`. Default: `gauge`

- **metric_type_mapping** (optional): Advanced configuration for fine-grained control
  - **default_type** (optional): Default metric type when no rules match. Options: `gauge`, `counter`, `histogram`. Default: `gauge`
  - **measurement_rules** (optional): List of specific measurement name mappings
  - **field_rules** (optional): List of field name pattern mappings (supports regex)
  - **pattern_rules** (optional): List of measurement name pattern mappings (supports regex)

#### Data Type Considerations

**Metric Type Conversion**: All InfluxDB data points are converted to OpenTelemetry Gauge metrics. Consider your data types:

- **Cumulative Counters**: Use `cumulativetodelta` processor to convert to deltas
- **Rate Metrics**: Already in correct format, no conversion needed
- **Gauge Metrics**: Direct conversion, no processing required
- **Histograms**: Converted to individual gauge metrics per bucket

**Polling Strategy**: The `interval` setting affects how you should process cumulative metrics:
- **Short intervals** (e.g., 10s): More frequent deltas, higher precision
- **Long intervals** (e.g., 60s): Less frequent deltas, lower precision but less overhead

## Usage Examples

### InfluxDB 1.x Configuration

```yaml
receivers:
  influxdbreader:
    endpoint:
      address: "localhost:8086"
      protocol: "http"
      authentication:
        username: "admin"
        password: "password123"
    database: "telegraf"
    interval: "30s"
    auto_discover: true  # Automatically discover all measurements
    prefix: "prod"       # Optional: prepend "prod_" to all metric names
```

### InfluxDB 2.x Configuration

```yaml
receivers:
  influxdbreader:
    endpoint:
      address: "localhost:8086"
      protocol: "https"
      authentication:
        token: "your-influxdb-token"
        organization: "my-organization"
        bucket: "my-bucket"
    database: "my-bucket"
    interval: "10s"
    use_v2_api: true
    auto_discover: true  # Automatically discover all measurements
```

### Complete Collector Configuration

#### Simple Configuration
```yaml
receivers:
  influxdbreader:
    endpoint:
      address: "10.0.0.146:8086"
      protocol: "http"
      authentication:
        username: "test"
        password: "deded"
    database: "mondb"
    interval: "10s"
    auto_discover: true

processors:
  batch:
    timeout: 1s
    send_batch_size: 1024
  memory_limiter:
    check_interval: 1s
    limit_mib: 1500
  resource:
    attributes:
      - key: environment
        value: "production"
        action: upsert
```

#### Configuration Comparison

##### **Simple Configuration (Recommended)**
```yaml
receivers:
  influxdbreader:
    # ... basic config ...
    
    # Simple metric type configuration
    metric_types:
      auto_detect: true
      overrides:
        memory_usage: "gauge"
        cpu_usage: "sum"
      default_type: "gauge"
```

##### **Advanced Configuration (Full Control)**
```yaml
receivers:
  influxdbreader:
    # ... basic config ...
    
    # Advanced metric type mapping configuration
    metric_type_mapping:
      default_type: "gauge"
      
      # Specific measurement rules
      measurement_rules:
        - measurement: "cpu_usage"
          metric_type: "counter"
          is_cumulative: true
          is_monotonic: true
        - measurement: "http_requests"
          metric_type: "counter"
          is_cumulative: true
          is_monotonic: true
        - measurement: "memory_usage"
          metric_type: "gauge"
          is_cumulative: false
          is_monotonic: false
      
      # Field name pattern rules (regex)
      field_rules:
        - field_pattern: ".*_total$"
          metric_type: "counter"
          is_cumulative: true
          is_monotonic: true
        - field_pattern: ".*_count$"
          metric_type: "counter"
          is_cumulative: true
          is_monotonic: true
        - field_pattern: ".*_rate$"
          metric_type: "gauge"
          is_cumulative: false
          is_monotonic: false
        - field_pattern: ".*_gauge$"
          metric_type: "gauge"
          is_cumulative: false
          is_monotonic: false
      
      # Measurement name pattern rules (regex)
      pattern_rules:
        - measurement_pattern: ".*_counter$"
          metric_type: "counter"
          is_cumulative: true
          is_monotonic: true
        - measurement_pattern: ".*_gauge$"
          metric_type: "gauge"
          is_cumulative: false
          is_monotonic: false
```


#### Full Configuration with All Components
See `config-full.yaml` for a complete example with:
- **Receivers**: InfluxDB reader, OTLP, Prometheus, FileLog, K8sObjects
- **Processors**: Batch, Memory Limiter, Resource, Transform, Filter
- **Exporters**: OTLP, Debug
- **Extensions**: Health Check, Pprof, ZPages

## Receiver Logic and Data Processing

### How the Receiver Processes Data

The InfluxDB Reader Receiver follows a specific logic to efficiently read and process InfluxDB data:

#### 1. Connection and Authentication
- Establishes connection to InfluxDB using configured protocol (HTTP/HTTPS/UDP)
- Authenticates using provided credentials (username/password or token)
- Validates database/bucket access permissions

#### 2. Measurement Discovery
When `auto_discover: true` (default):
- **InfluxDB 1.x**: Executes `SHOW MEASUREMENTS` to get all measurement names
- **InfluxDB 2.x**: Uses `schema.measurements()` to discover measurements
- Caches the measurement list for efficient processing

#### 3. Data Retrieval Strategy
For each discovered measurement:
- **Incremental Polling**: Uses time-based filtering to avoid missing data between polls
- **First Run**: Fetches historical data from the past interval (e.g., last 30 seconds)
- **Subsequent Runs**: Fetches only new data since the last successful fetch
- **InfluxDB 1.x**: 
  - First run: `SELECT * FROM "measurement_name" WHERE time >= 'start_time' ORDER BY time ASC`
  - Subsequent: `SELECT * FROM "measurement_name" WHERE time > 'last_fetch_time' ORDER BY time ASC`
- **InfluxDB 2.x**: Similar time-based filtering with Flux queries

#### 4. Column Analysis and Metric Creation
For each measurement, the receiver analyzes the data structure:
- **Column Classification**: Identifies numeric columns (float64, int64, int, json.Number) vs string columns
- **Metric Generation**: Creates one OpenTelemetry metric per numeric column
- **Metric Naming**: Uses pattern `measurement_name_column_name` (e.g., `cpu_usage_value`)
- **Attribute Mapping**: String columns become metric attributes (tags)
- **Timestamp Handling**: Uses InfluxDB's time column as metric timestamp

#### 5. Data Conversion
Converts InfluxDB data points to OpenTelemetry metrics:
- **Numeric Columns** → **Metric Values** (one metric per column)
- **String Columns** → **Attributes** (tags)
- **Time Column** → **Metric Timestamp**
- **Measurement + Column Name** → **Metric Name**

#### 5. Pipeline Integration
- Sends converted metrics to the OpenTelemetry processing pipeline
- Applies configured processors (batch, transform, filter, etc.)
- Exports to configured backends

### Auto-Discovery Feature

The receiver supports automatic measurement discovery, which is enabled by default. When `auto_discover: true`:

1. **Measurement Discovery**: The receiver runs `SHOW MEASUREMENTS` (InfluxDB 1.x) or `schema.measurements()` (InfluxDB 2.x) to discover all available measurements

2. **Incremental Data Fetching**: For each discovered measurement, it uses time-based filtering:
   - **First Run**: Fetches historical data from the past interval to avoid missing metrics
   - **Subsequent Runs**: Fetches only new data since the last successful fetch
   - **Query Examples**:
     - InfluxDB 1.x: `SELECT * FROM "measurement_name" WHERE time >= 'start_time' ORDER BY time ASC`
     - InfluxDB 2.x: Similar time-based filtering with Flux queries

3. **Column Analysis**: For each measurement, analyzes the data structure:
   - **Numeric Columns**: Identifies float64, int64, int, and json.Number types as potential metric values
   - **String Columns**: Identifies string types as potential attributes
   - **Metric Creation**: Creates one OpenTelemetry metric per numeric column

4. **Metric Naming Convention**: Uses the pattern `measurement_name_column_name`
   - Example: A measurement named `cpu_usage` with a column `value` becomes metric `cpu_usage_value`
   - Example: A measurement named `disk_stats` with columns `read_bytes` and `write_bytes` creates two metrics: `disk_stats_read_bytes` and `disk_stats_write_bytes`
   - **Prefix Support**: If a `prefix` is configured, it will be prepended to all metric names (e.g., `prod_cpu_usage_value` with prefix `prod`)

This approach ensures comprehensive metric collection without missing data between polling intervals.

### Performance Considerations

- **Polling Interval**: Configure based on your data update frequency (default: 30s)
- **Incremental Fetching**: Only fetches new data since the last successful poll, reducing data transfer
- **Column Analysis**: Analyzes measurement structure once per measurement to optimize metric creation
- **Memory Usage**: Processes one measurement at a time to control memory consumption
- **Network Efficiency**: Uses time-based filtering to minimize query results and network overhead
- **Metric Generation**: Creates metrics efficiently by processing all numeric columns in a single pass

### Metric Type Classification

The receiver automatically classifies metrics into appropriate OpenTelemetry metric types:

#### Automatic Classification Rules
- **Counters**: Fields ending with `_total`, `_count`, `_requests_total`, `_errors_total`, `_operations_total`, `_events_total`, `_packets_total`, `_connections_total`, `_sessions_total`
- **Gauges**: All other numeric fields (default)
- **Histograms**: Fields ending with `_bucket`, `_sum`, `_quantile`

#### Conservative Approach
The receiver uses a conservative classification strategy to avoid incorrect metric type assignment:
- Only fields with explicit counter patterns are classified as counters
- Prevents common issues like `_bytes` fields being incorrectly classified as counters
- Reduces backend errors related to unsupported metric types

#### Data Type Handling
The receiver handles various InfluxDB data types robustly:
- **Numeric Types**: float64, int64, int, json.Number
- **String Types**: Converted to metric attributes
- **Null Values**: Converted to 0 for numeric fields to prevent metric creation failures
- **Boolean Values**: Converted to 0/1 for numeric representation
- **Timestamp Handling**: Uses InfluxDB's time column as metric timestamp with proper parsing

### Receiver Telemetry Metrics

The InfluxDB Reader Receiver produces its own telemetry metrics to monitor its performance and health. These metrics are sent through the OpenTelemetry pipeline and can be used to monitor the receiver's operation.

#### Available Telemetry Metrics

| Metric Name | Type | Description | Unit |
|-------------|------|-------------|------|
| `otelcol_receiver_influxdbreader_measurements_discovered` | Counter | Number of measurements discovered by the receiver | 1 |
| `otelcol_receiver_influxdbreader_metrics_converted` | Counter | Number of metrics successfully converted from InfluxDB | 1 |
| `otelcol_receiver_influxdbreader_metrics_dropped` | Counter | Number of metrics dropped due to errors or invalid data | 1 |
| `otelcol_receiver_influxdbreader_queries_executed` | Counter | Number of queries executed against InfluxDB | 1 |
| `otelcol_receiver_influxdbreader_queries_errors` | Counter | Number of query errors encountered | 1 |

#### Metric Details

**`otelcol_receiver_influxdbreader_measurements_discovered`**
- **Purpose**: Tracks how many InfluxDB measurements the receiver has discovered
- **Use Case**: Monitor the scope of data being processed
- **Expected Behavior**: Should increase over time as new measurements are discovered
- **Troubleshooting**: If this doesn't increase, check InfluxDB connectivity and permissions

**`otelcol_receiver_influxdbreader_metrics_converted`**
- **Purpose**: Counts successfully converted metrics from InfluxDB to OpenTelemetry format
- **Use Case**: Monitor the volume of data being processed
- **Expected Behavior**: Should increase with each successful polling cycle
- **Troubleshooting**: If this is low compared to discovered measurements, check data format compatibility

**`otelcol_receiver_influxdbreader_metrics_dropped`**
- **Purpose**: Counts metrics that were dropped due to errors or invalid data
- **Use Case**: Monitor data quality and processing errors
- **Expected Behavior**: Should remain low in normal operation
- **Troubleshooting**: High values indicate data format issues or conversion problems

**`otelcol_receiver_influxdbreader_queries_executed`**
- **Purpose**: Tracks the number of queries executed against InfluxDB
- **Use Case**: Monitor database load and query frequency
- **Expected Behavior**: Should increase with each polling cycle
- **Troubleshooting**: If this doesn't increase, check polling configuration and connectivity

**`otelcol_receiver_influxdbreader_queries_errors`**
- **Purpose**: Counts query errors encountered during operation
- **Use Case**: Monitor database connectivity and query health
- **Expected Behavior**: Should remain low in normal operation
- **Troubleshooting**: High values indicate database connectivity issues or query problems

#### Telemetry Metric Configuration

The telemetry metrics are automatically generated and sent through the OpenTelemetry pipeline. They use **non-monotonic counters** to ensure compatibility with various backends that may not support monotonic cumulative sums.

**Aggregation Temporality**: The telemetry metrics are compatible with both Delta and Cumulative aggregation temporality, with the format determined by the collector's telemetry configuration.

#### Example Telemetry Dashboard Queries

**Grafana/Prometheus:**
```promql
# Rate of metrics being converted
rate(otelcol_receiver_influxdbreader_metrics_converted_total[5m])

# Error rate
rate(otelcol_receiver_influxdbreader_queries_errors_total[5m])

# Discovery rate
rate(otelcol_receiver_influxdbreader_measurements_discovered_total[5m])

# Conversion efficiency
otelcol_receiver_influxdbreader_metrics_converted_total / (otelcol_receiver_influxdbreader_measurements_discovered_total)
```

**Monitoring Alerts:**
```yaml
# High error rate alert
- alert: InfluxDBReaderHighErrorRate
  expr: rate(otelcol_receiver_influxdbreader_queries_errors_total[5m]) > 0.1
  for: 2m
  labels:
    severity: warning
  annotations:
    summary: "InfluxDB Reader has high error rate"

# No metrics being converted
- alert: InfluxDBReaderNoMetrics
  expr: rate(otelcol_receiver_influxdbreader_metrics_converted_total[5m]) == 0
  for: 5m
  labels:
    severity: critical
  annotations:
    summary: "InfluxDB Reader is not converting any metrics"
```

#### Resource Attributes

All telemetry metrics include the following resource attributes:
- `service.name`: "influxdbreader"
- `service.version`: "0.2.8"
- `service.instance.id`: "influxdbreader-receiver"

#### Scope Information

Telemetry metrics are emitted with the following scope:
- `scope.name`: "influxdbreader"
- `scope.version`: "0.2.8"

### Manual Query Mode

If you prefer to use custom queries, set `auto_discover: false` and provide a `query` parameter:

```yaml
receivers:
  influxdbreader:
    # ... other config ...
    auto_discover: false
    query: "SELECT mean(value) FROM cpu_usage WHERE time > now() - 5m GROUP BY host"
```

## Installation

### Quick Start with Docker

```bash
# Build and run with Docker Compose (includes InfluxDB for testing)
make docker-build
docker-compose up -d

# Or run just the collector with the receiver
docker run --rm -v $(pwd)/config.yaml:/etc/otelcol/config.yaml influxdbreader:latest

# For specific platforms
make docker-build-arm64
docker run --rm --platform linux/arm64 -v $(pwd)/config.yaml:/etc/otelcol/config.yaml influxdbreader:latest

# For development
make docker-build-dev
docker-compose -f docker-compose.yml -f docker-compose.override.yml up -d
```

### Testing Data Generation

**Note**: The deployment examples include **Telegraf** which is used **only for testing purposes** to generate sample metrics in InfluxDB. In production, you would typically have:

- **Your applications** writing metrics directly to InfluxDB
- **Existing monitoring agents** (Prometheus, other Telegraf instances, etc.)
- **Historical data** already present in InfluxDB

#### Why Telegraf is Included in Examples

1. **Demonstration**: Shows the complete data flow from data generation to collection
2. **Testing**: Provides continuous, realistic metrics for testing the receiver
3. **Validation**: Ensures the receiver works correctly with real InfluxDB data
4. **Development**: Allows developers to test without setting up external data sources

#### Production vs Testing

**For Testing/Development:**
```yaml
# Include Telegraf to generate test data
telegraf:
  image: telegraf:1.25-alpine
  # ... configuration to write to InfluxDB
```

**For Production:**
```yaml
# Remove Telegraf - use your existing data sources
# Your applications → InfluxDB → InfluxDB Reader Receiver
```

### Manual Installation

1. Clone this repository
2. Install development tools:
   ```bash
   make install-tools
   ```
3. Build the collector with the receiver:
   ```bash
   # Build with Docker (recommended)
   make docker-build
   ```
4. Use the receiver in your OpenTelemetry Collector configuration

### Deployment Options

The project provides multiple deployment options in the `deployments/` directory:

#### Docker Compose (Recommended for Testing)
- **Location**: `deployments/docker/`
- **Use Case**: Development, testing, and demonstration
- **Includes**: InfluxDB, Telegraf (for test data), Jaeger, Prometheus, Grafana
- **Quick Start**: `cd deployments/docker && ./deploy.sh build-start`

#### Kubernetes (Recommended for Production)
- **Location**: `deployments/k8s/`
- **Use Case**: Production deployments, cloud environments
- **Includes**: InfluxDB, Telegraf (optional), proper resource management
- **Quick Start**: `cd deployments/k8s && ./deploy.sh deploy`

#### Standalone Collector
- **Use Case**: Integration with existing infrastructure
- **Command**: `docker run --rm -v config.yaml:/etc/otelcol/config.yaml influxdbreader:latest`

### Choosing the Right Deployment

| Scenario | Recommended Deployment | Reason |
|----------|----------------------|---------|
| **Testing/Development** | Docker Compose | Easy setup, includes test data generation |
| **Production** | Kubernetes | Scalability, resource management, monitoring |
| **Existing Infrastructure** | Standalone | Minimal footprint, easy integration |
| **Migration Project** | Kubernetes | Production-ready, supports scaling |
| **Demo/POC** | Docker Compose | Quick setup, visual monitoring tools |

### Development Setup

```bash
# Complete development environment setup
make dev-setup

# Run tests
make test

# Run with test configuration
make run-test
```

## Platform Support

The InfluxDB Reader Receiver supports multiple platforms:

- **Linux AMD64** (x86_64) - Default platform
- **Linux ARM64** (aarch64) - For ARM-based servers and devices
- **Linux ARM32** (armv7) - For older ARM devices

> **Note**: Cross-platform builds (e.g., building ARM64 on AMD64) require Docker Buildx and may need additional setup like QEMU emulation. Single-platform builds work reliably on the native platform.

### Platform-Specific Builds

```bash
# Build for specific platform
make docker-build-amd64    # Linux x86_64
make docker-build-arm64    # Linux ARM64  
make docker-build-arm32    # Linux ARM32

# Build multi-platform image (amd64 + arm64)
make docker-build-multi

# Custom platform
make docker-build PLATFORM=linux/arm64
```

## OCB Manifest

The project includes a comprehensive OCB (OpenTelemetry Collector Builder) manifest (`ocb.yaml`) that builds a complete collector with all components through Docker:

### Receivers
- **influxdbreader** - Custom InfluxDB reader receiver
- **otlpreceiver** - OTLP protocol receiver (gRPC/HTTP)
- **prometheusreceiver** - Prometheus metrics scraping
- **filelogreceiver** - File-based log collection
- **k8sobjectsreceiver** - Kubernetes objects monitoring

### Processors
- **batchprocessor** - Efficient batching of telemetry data
- **memorylimiterprocessor** - Memory usage control
- **resourceprocessor** - Resource attribute management
- **transformprocessor** - Data transformation capabilities
- **filterprocessor** - Data filtering and routing

### Exporters
- **otlpexporter** - OTLP protocol exporter
- **debugexporter** - Debug output for development

### Extensions
- **healthcheckextension** - Health monitoring endpoint
- **pprofextension** - Profiling capabilities
- **zpagesextension** - Debugging and diagnostics

## Development

### Prerequisites
- Go 1.21 or later
- OpenTelemetry Collector dependencies
- Docker (for containerized builds)

### Building

```bash
# Build Docker image with OCB (default: linux/amd64)
make docker-build

# Build for specific platforms
make docker-build-amd64    # Linux x86_64
make docker-build-arm64    # Linux ARM64
make docker-build-arm32    # Linux ARM32

# Build multi-platform image (amd64 + arm64)
make docker-build-multi

# Build development Docker image
make docker-build-dev

# Build with custom platform
make docker-build PLATFORM=linux/arm64
```

### Testing

The project includes comprehensive unit tests and integration tests.

```bash
# Run all tests
make test

# Run tests with coverage
make test-coverage

# Run integration tests (requires InfluxDB)
make integration

# Run with Docker Compose (includes InfluxDB)
docker-compose up -d
```

### Docker

```bash
# Build production Docker image
make docker-build

# Build development Docker image
make docker-build-dev

# Run with different configurations
make docker-run-test
make docker-run-simple
make docker-run-full

# Run development container with shell
make docker-run-dev

# Push to registry
make docker-push
```

#### Docker Images

- **Production Image** (`Dockerfile`): Optimized for production with minimal dependencies
- **Development Image** (`Dockerfile.dev`): Includes debugging tools and utilities
- **Multi-stage Build**: Uses OCB to build the collector with all components

#### Docker Compose

```bash
# Run with production setup
docker-compose up -d

# Run with development setup (includes override)
docker-compose -f docker-compose.yml -f docker-compose.override.yml up -d
```

#### Running Tests

```bash
# Run all tests
make test

# Run tests with verbose output
make test-verbose

# Run tests with coverage report
make test-coverage

# Run integration tests (requires InfluxDB)
make integration
```

#### Test Structure

- **Unit Tests**: Test individual components in isolation
  - `config_test.go` - Configuration validation tests
  - `factory_test.go` - Receiver factory tests
  - `influxdbreader_receiver_test.go` - Main receiver logic tests

- **Integration Tests**: Test the complete receiver lifecycle
  - `integration_test.go` - End-to-end tests (requires InfluxDB)

- **Test Helpers**: Common test utilities
  - `test_helpers.go` - Mock implementations and helper functions

#### Test Coverage

The tests cover:
- Configuration validation and defaults
- Receiver lifecycle (start/stop)
- Authentication configurations
- Error handling
- Metrics conversion
- Context cancellation

#### Running Specific Tests

```bash
# Run only configuration tests
go test -v ./receiver/influxdbreaderreceiver -run TestConfig

# Run only factory tests
go test -v ./receiver/influxdbreaderreceiver -run TestFactory

# Run only integration tests
go test -v ./receiver/influxdbreaderreceiver -run TestIntegration
```

## Production Considerations

### Stability Level Warning

⚠️ **Important**: This receiver is currently in **Alpha** stability level. This means:

- **Breaking changes** may occur in future releases
- **API modifications** are possible
- **Not recommended** for production use without thorough testing
- **Backup strategies** should be in place for any data processing

Consider this when planning production deployments.

### When Moving to Production

1. **Remove Telegraf**: The deployment examples include Telegraf for testing. In production, remove it and use your existing data sources.

2. **Configure Metric Processing**: Based on your InfluxDB data types, configure appropriate processors:
   - **Cumulative counters**: Use `cumulativetodelta` processor or configure metric type mapping
   - **Rate metrics**: No conversion needed
   - **Gauge metrics**: Direct processing
   - **Histograms**: Consider aggregation strategies
   - **Metric Type Mapping**: Use the new `metric_type_mapping` configuration for automatic type detection

3. **Configure Authentication**: Use proper authentication and TLS for InfluxDB connections.

4. **Resource Limits**: Set appropriate resource limits based on your data volume and polling frequency.

5. **Monitoring**: Monitor the receiver's performance and resource usage.

6. **Backup Strategy**: Ensure your InfluxDB data is properly backed up.

### Production Configuration Example

```yaml
receivers:
  influxdbreader:
    endpoint:
      address: "your-influxdb-server:8086"
      protocol: "https"
      authentication:
        username: "your-username"
        password: "your-secure-password"
    database: "your-production-db"
    interval: "60s"  # Adjust based on your needs
    auto_discover: true
    timeout: "30s"
    insecure: false  # Use TLS in production

processors:
  batch:
    timeout: 5s
    send_batch_size: 1000
  memory_limiter:
    check_interval: 1s
    limit_mib: 2000
  resource:
    attributes:
      - key: environment
        value: "production"
        action: upsert

exporters:
  otlp:
    endpoint: "your-otel-backend:4317"
    tls:
      insecure: false
```

### Data Source Alternatives

Instead of Telegraf, your InfluxDB might be populated by:

- **Application Metrics**: Your applications writing directly to InfluxDB
- **Prometheus**: Using Prometheus remote write to InfluxDB
- **Other Agents**: Existing monitoring agents in your infrastructure
- **Historical Data**: Legacy systems or data migration projects

## License

This project is licensed under the Apache License 2.0.
