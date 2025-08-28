#!/bin/bash

# Docker Compose deployment script for InfluxDB Reader OpenTelemetry Collector

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

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

# Function to check if Docker is available
check_docker() {
    if ! command -v docker &> /dev/null; then
        print_error "Docker is not installed or not in PATH"
        exit 1
    fi
    
    if ! docker info &> /dev/null; then
        print_error "Cannot connect to Docker daemon"
        exit 1
    fi
    
    print_success "Docker is available"
}

# Function to check if Docker Compose is available
check_docker_compose() {
    if ! command -v docker-compose &> /dev/null; then
        print_error "Docker Compose is not installed or not in PATH"
        exit 1
    fi
    
    print_success "Docker Compose is available"
}

# Function to check if the image exists
check_image() {
    if ! docker image inspect influxdbreader:latest &> /dev/null; then
        print_warning "Image influxdbreader:latest not found"
        print_status "Please build the image first: make docker-build"
        exit 1
    fi
    
    print_success "Image influxdbreader:latest found"
}

# Function to start the stack
start_stack() {
    print_status "Starting Docker Compose stack..."
    docker-compose up -d
    print_success "Stack started successfully"
}

# Function to stop the stack
stop_stack() {
    print_status "Stopping Docker Compose stack..."
    docker-compose down
    print_success "Stack stopped successfully"
}

# Function to restart the stack
restart_stack() {
    print_status "Restarting Docker Compose stack..."
    docker-compose restart
    print_success "Stack restarted successfully"
}

# Function to show status
show_status() {
    print_status "Docker Compose stack status:"
    docker-compose ps
    echo
    
    print_status "Service logs (last 10 lines):"
    docker-compose logs --tail=10
}

# Function to show logs
show_logs() {
    local service=${1:-""}
    
    if [ -n "$service" ]; then
        print_status "Showing logs for service: $service"
        docker-compose logs -f "$service"
    else
        print_status "Showing logs for all services:"
        docker-compose logs -f
    fi
}

# Function to clean up
cleanup() {
    print_warning "Cleaning up Docker Compose stack..."
    docker-compose down -v --remove-orphans
    print_success "Cleanup completed"
}

# Function to build and start
build_and_start() {
    print_status "Building and starting the stack..."
    
    # Check if we're in the right directory
    if [ ! -f "docker-compose.yml" ]; then
        print_error "docker-compose.yml not found. Please run this script from the deployments/docker directory."
        exit 1
    fi
    
    # Build the image if it doesn't exist
    if ! docker image inspect influxdbreader:latest &> /dev/null; then
        print_status "Building influxdbreader image..."
        cd ../..
        make docker-build
        cd deployments/docker
    fi
    
    # Start the stack
    start_stack
    show_status
}

# Function to show usage
usage() {
    echo "Usage: $0 [COMMAND] [OPTIONS]"
    echo
    echo "Commands:"
    echo "  start       - Start the Docker Compose stack"
    echo "  stop        - Stop the Docker Compose stack"
    echo "  restart     - Restart the Docker Compose stack"
    echo "  status      - Show stack status"
    echo "  logs [SERVICE] - Show logs (all services or specific service)"
    echo "  cleanup     - Clean up stack and volumes"
    echo "  build-start - Build image and start stack"
    echo "  help        - Show this help message"
    echo
    echo "Examples:"
    echo "  $0 start"
    echo "  $0 logs influxdbreader"
    echo "  $0 build-start"
    echo "  $0 cleanup"
    echo
    echo "Services:"
    echo "  influxdb1, influxdb2, telegraf, influxdbreader, jaeger, prometheus, grafana"
}

# Main script logic
case "${1:-help}" in
    start)
        print_status "Starting deployment..."
        check_docker
        check_docker_compose
        check_image
        start_stack
        show_status
        ;;
    stop)
        stop_stack
        ;;
    restart)
        restart_stack
        ;;
    status)
        show_status
        ;;
    logs)
        show_logs "$2"
        ;;
    cleanup)
        cleanup
        ;;
    build-start)
        build_and_start
        ;;
    help|*)
        usage
        ;;
esac
