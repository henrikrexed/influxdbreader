#!/bin/bash

# Kubernetes deployment script for InfluxDB Reader OpenTelemetry Collector
# This script deploys the complete stack including InfluxDB, Telegraf, and the custom collector

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
NAMESPACES=("influxdb" "telegraf" "otel-collector")
DEPLOYMENT_FILES=(
    "influxdb-deployment.yaml"
    "telegraf-deployment.yaml"
    "otelcol-influxdbreader-deployment.yaml"
    "ingress.yaml"
)

# Function to print colored output
print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Function to check if kubectl is available
check_kubectl() {
    if ! command -v kubectl &> /dev/null; then
        print_error "kubectl is not installed or not in PATH"
        exit 1
    fi
    
    if ! kubectl cluster-info &> /dev/null; then
        print_error "Cannot connect to Kubernetes cluster"
        exit 1
    fi
    
    print_success "kubectl is available and connected to cluster"
}

# Function to create namespaces
create_namespaces() {
    print_status "Creating namespaces..."
    for namespace in "${NAMESPACES[@]}"; do
        if kubectl get namespace "$namespace" &> /dev/null; then
            print_warning "Namespace $namespace already exists"
        else
            kubectl create namespace "$namespace"
            print_success "Created namespace: $namespace"
        fi
    done
}

# Function to deploy resources
deploy_resources() {
    print_status "Deploying resources..."
    
    for file in "${DEPLOYMENT_FILES[@]}"; do
        if [ -f "$file" ]; then
            print_status "Applying $file..."
            kubectl apply -f "$file"
            print_success "Applied $file"
        else
            print_error "File $file not found"
            exit 1
        fi
    done
}

# Function to wait for deployments to be ready
wait_for_deployments() {
    print_status "Waiting for deployments to be ready..."
    
    # Wait for InfluxDB
    print_status "Waiting for InfluxDB deployment..."
    kubectl wait --for=condition=available --timeout=300s deployment/influxdb -n influxdb
    
    # Wait for Telegraf
    print_status "Waiting for Telegraf deployment..."
    kubectl wait --for=condition=available --timeout=300s deployment/telegraf -n telegraf
    
    # Wait for OpenTelemetry Collector
    print_status "Waiting for OpenTelemetry Collector deployment..."
    kubectl wait --for=condition=available --timeout=300s deployment/otel-collector -n otel-collector
    
    print_success "All deployments are ready"
}

# Function to show deployment status
show_status() {
    print_status "Deployment status:"
    echo
    
    for namespace in "${NAMESPACES[@]}"; do
        echo "=== Namespace: $namespace ==="
        kubectl get pods,svc,deployments -n "$namespace"
        echo
    done
    
    echo "=== Services ==="
    kubectl get svc --all-namespaces | grep -E "(influxdb|telegraf|otel-collector)"
    echo
    
    echo "=== Ingress ==="
    kubectl get ingress --all-namespaces
    echo
}

# Function to show logs
show_logs() {
    print_status "Recent logs from OpenTelemetry Collector:"
    kubectl logs -n otel-collector deployment/otel-collector --tail=20
    echo
    
    print_status "Recent logs from InfluxDB:"
    kubectl logs -n influxdb deployment/influxdb --tail=10
    echo
    
    print_status "Recent logs from Telegraf:"
    kubectl logs -n telegraf deployment/telegraf --tail=10
}

# Function to clean up
cleanup() {
    print_warning "Cleaning up deployments..."
    
    for file in "${DEPLOYMENT_FILES[@]}"; do
        if [ -f "$file" ]; then
            kubectl delete -f "$file" --ignore-not-found=true
        fi
    done
    
    for namespace in "${NAMESPACES[@]}"; do
        kubectl delete namespace "$namespace" --ignore-not-found=true
    done
    
    print_success "Cleanup completed"
}

# Function to show usage
usage() {
    echo "Usage: $0 [COMMAND]"
    echo
    echo "Commands:"
    echo "  deploy     - Deploy the complete stack"
    echo "  status     - Show deployment status"
    echo "  logs       - Show recent logs"
    echo "  cleanup    - Clean up all deployments"
    echo "  help       - Show this help message"
    echo
    echo "Examples:"
    echo "  $0 deploy"
    echo "  $0 status"
    echo "  $0 logs"
    echo "  $0 cleanup"
}

# Main script logic
case "${1:-help}" in
    deploy)
        print_status "Starting deployment..."
        check_kubectl
        create_namespaces
        deploy_resources
        wait_for_deployments
        show_status
        print_success "Deployment completed successfully!"
        ;;
    status)
        show_status
        ;;
    logs)
        show_logs
        ;;
    cleanup)
        cleanup
        ;;
    help|*)
        usage
        ;;
esac
