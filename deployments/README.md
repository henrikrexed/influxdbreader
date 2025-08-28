# Deployment Guide

This directory contains deployment configurations for both Docker Compose and Kubernetes environments.

## Directory Structure

```
deployments/
├── docker/                    # Docker Compose deployments
│   ├── docker-compose.yml     # Main Docker Compose stack
│   ├── docker-compose.override.yml  # Override configuration
│   └── prometheus.yml         # Prometheus configuration
├── k8s/                       # Kubernetes deployments
│   ├── otelcol-influxdbreader-deployment.yaml  # Main collector deployment
│   ├── influxdb-deployment.yaml                # InfluxDB deployment
│   ├── telegraf-deployment.yaml                # Telegraf deployment
│   ├── ingress.yaml                           # Ingress configuration
│   └── deploy.sh                              # Deployment script
└── README.md                  # This file
```

## Docker Compose Deployment

### Prerequisites

- Docker and Docker Compose installed
- Built the `influxdbreader:latest` image using the Makefile

### Quick Start

1. **Build the image:**
   ```bash
   make docker-build
   # or with Podman
   make docker-build CONTAINER_RUNTIME=podman
   ```

2. **Start the stack:**
   ```bash
   cd deployments/docker
   docker-compose up -d
   ```

3. **Access the services:**
   - **InfluxDB 1.x:** http://localhost:8086 (admin/password)
   - **InfluxDB 2.x:** http://localhost:8087 (admin/password)
   - **Jaeger UI:** http://localhost:16686
   - **Prometheus:** http://localhost:9090
   - **Grafana:** http://localhost:3000 (admin/admin)

### Services Included

- **InfluxDB 1.x & 2.x:** Time-series databases
- **Telegraf:** Metrics collection agent
- **OpenTelemetry Collector:** Custom collector with InfluxDB reader receiver
- **Jaeger:** Distributed tracing
- **Prometheus:** Metrics storage and querying
- **Grafana:** Metrics visualization

### Configuration

The Docker Compose setup uses the following configuration files:
- `config-simple.yaml`: OpenTelemetry Collector configuration
- `prometheus.yml`: Prometheus scraping configuration
- `scripts/telegraf.conf`: Telegraf configuration

## Kubernetes Deployment

### Prerequisites

- Kubernetes cluster (local or remote)
- `kubectl` configured and connected
- Built the `influxdbreader:latest` image and pushed to a registry
- NGINX Ingress Controller (optional, for ingress)

### Quick Start

1. **Build and push the image:**
   ```bash
   # Build with custom version
   make docker-build VERSION=v1.0.0
   
   # Tag for your registry
   docker tag influxdbreader:v1.0.0 your-registry/influxdbreader:v1.0.0
   
   # Push to registry
   docker push your-registry/influxdbreader:v1.0.0
   ```

2. **Update the deployment:**
   Edit `deployments/k8s/otelcol-influxdbreader-deployment.yaml` and update the image reference:
   ```yaml
   image: your-registry/influxdbreader:v1.0.0
   ```

3. **Deploy using the script:**
   ```bash
   cd deployments/k8s
   ./deploy.sh deploy
   ```

### Manual Deployment

If you prefer to deploy manually:

```bash
# Create namespaces
kubectl create namespace influxdb
kubectl create namespace telegraf
kubectl create namespace otel-collector

# Deploy InfluxDB
kubectl apply -f influxdb-deployment.yaml

# Deploy Telegraf
kubectl apply -f telegraf-deployment.yaml

# Deploy OpenTelemetry Collector
kubectl apply -f otelcol-influxdbreader-deployment.yaml

# Deploy Ingress (optional)
kubectl apply -f ingress.yaml
```

### Deployment Script Commands

The `deploy.sh` script provides several commands:

```bash
./deploy.sh deploy    # Deploy the complete stack
./deploy.sh status    # Show deployment status
./deploy.sh logs      # Show recent logs
./deploy.sh cleanup   # Clean up all deployments
./deploy.sh help      # Show help
```

### Services Included

- **InfluxDB:** Time-series database with persistent storage
- **Telegraf:** Metrics collection agent
- **OpenTelemetry Collector:** Custom collector with all components
- **Ingress:** External access to services

### Configuration

The Kubernetes deployment uses ConfigMaps for configuration:
- `otel-collector-config`: OpenTelemetry Collector configuration
- `influxdb-config`: InfluxDB configuration
- `telegraf-config`: Telegraf configuration

## Configuration Examples

### OpenTelemetry Collector Configuration

The collector is configured with:
- **Receivers:** influxdbreader, otlp, prometheus, filelog
- **Processors:** batch, memory_limiter, transform, cumulativetodelta, filter, resource
- **Exporters:** otlp, otlphttp, debug

### InfluxDB Reader Receiver Configuration

```yaml
influxdbreader:
  endpoint:
    address: "influxdb-service:8086"
    protocol: "http"
    authentication:
      username: "admin"
      password: "password"
  database: "telegraf"
  interval: "30s"
  auto_discover: true
  use_v2_api: false
  timeout: "30s"
```

## Monitoring and Troubleshooting

### Health Checks

- **OpenTelemetry Collector:** http://localhost:13133/ (Docker) or service port (K8s)
- **InfluxDB:** http://localhost:8086/ping (Docker) or service port (K8s)

### Logs

**Docker Compose:**
```bash
docker-compose logs influxdbreader
docker-compose logs influxdb1
docker-compose logs telegraf
```

**Kubernetes:**
```bash
kubectl logs -n otel-collector deployment/otel-collector
kubectl logs -n influxdb deployment/influxdb
kubectl logs -n telegraf deployment/telegraf
```

### Metrics

- **Prometheus:** Scrapes metrics from the collector at `/metrics`
- **Grafana:** Visualizes metrics from Prometheus and InfluxDB

## Customization

### Environment Variables

You can customize the deployment by setting environment variables:

**Docker Compose:**
```bash
export INFLUXDB_PASSWORD=your-password
export OTEL_LOG_LEVEL=debug
docker-compose up -d
```

**Kubernetes:**
Edit the deployment YAML files to modify environment variables, resource limits, or configuration.

### Adding Components

To add new components to the OpenTelemetry Collector:

1. Update `ocb.yaml` to include the new component
2. Rebuild the image: `make docker-build`
3. Update the deployment configuration
4. Redeploy

## Security Considerations

- **Authentication:** InfluxDB uses basic authentication
- **Network:** Services are isolated in their own networks/namespaces
- **Secrets:** Kubernetes deployments use ConfigMaps for configuration (consider using Secrets for sensitive data in production)
- **TLS:** Configure TLS for production deployments

## Production Recommendations

1. **Use Secrets** for sensitive configuration
2. **Enable TLS** for all communications
3. **Set resource limits** appropriate for your workload
4. **Use persistent storage** for InfluxDB
5. **Configure monitoring** and alerting
6. **Set up backup** strategies for InfluxDB data
7. **Use a proper registry** for container images
8. **Configure horizontal pod autoscaling** for the collector
