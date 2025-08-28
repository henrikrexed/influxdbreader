#!/bin/bash

# Wait for InfluxDB to be ready
echo "Waiting for InfluxDB to be ready..."
until curl -s http://localhost:8086/ping; do
  sleep 1
done

# Create database and user
echo "Setting up InfluxDB 1.x..."
influx -host localhost -port 8086 -username admin -password password -execute "
CREATE DATABASE testdb;
CREATE USER testuser WITH PASSWORD 'testpass';
GRANT ALL ON testdb TO testuser;
"

# Insert some sample data
echo "Inserting sample data..."
influx -host localhost -port 8086 -username admin -password password -database testdb -execute "
INSERT cpu,host=server1,region=us-west usage=0.64,idle=0.36
INSERT cpu,host=server2,region=us-east usage=0.78,idle=0.22
INSERT memory,host=server1,region=us-west used=8192,free=2048
INSERT memory,host=server2,region=us-east used=16384,free=4096
INSERT disk,host=server1,region=us-west,device=sda used=512000,free=1024000
INSERT disk,host=server2,region=us-east,device=sda used=1024000,free=2048000
"

echo "InfluxDB 1.x setup complete!"



