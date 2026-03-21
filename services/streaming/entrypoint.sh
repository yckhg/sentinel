#!/bin/sh
set -e

mkdir -p /tmp/hls

# Start Go API server in background
./streaming-api &

# Start nginx in foreground
exec nginx -g 'daemon off;'
