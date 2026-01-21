#!/bin/bash
# Integration test runner
# Starts stub services and runs integration tests against the real binary.
#
# Prerequisites: Run scripts/build.sh first

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
BIN_DIR="$PROJECT_ROOT/bin"

# Ports
OURCLOUD_PORT=50052
FCM_PORT=9099
GATEWAY_PORT=8085

# PIDs for cleanup
OURCLOUD_PID=""
FCM_PID=""
GATEWAY_PID=""

cleanup() {
    echo "Cleaning up..."
    [ -n "$GATEWAY_PID" ] && kill "$GATEWAY_PID" 2>/dev/null || true
    [ -n "$FCM_PID" ] && kill "$FCM_PID" 2>/dev/null || true
    [ -n "$OURCLOUD_PID" ] && kill "$OURCLOUD_PID" 2>/dev/null || true
    rm -f /tmp/pushserver-integration-test.db
    echo "Cleanup complete"
}

trap cleanup EXIT

# Check binaries exist
for bin in pushserver ourcloud-stub fcm-stub; do
    if [ ! -x "$BIN_DIR/$bin" ]; then
        echo "ERROR: $BIN_DIR/$bin not found. Run scripts/build.sh first."
        exit 1
    fi
done

echo "=== Starting stub services ==="

echo "Starting OurCloud stub on port $OURCLOUD_PORT..."
"$BIN_DIR/ourcloud-stub" -port "$OURCLOUD_PORT" -config "$SCRIPT_DIR/fixtures.json" &
OURCLOUD_PID=$!
sleep 0.5

echo "Starting FCM stub on port $FCM_PORT..."
"$BIN_DIR/fcm-stub" -port "$FCM_PORT" -project test-project &
FCM_PID=$!
sleep 0.5

echo ""
echo "=== Starting push gateway ==="
cd "$SCRIPT_DIR"
"$BIN_DIR/pushserver" -config "$SCRIPT_DIR/config.yaml" &
GATEWAY_PID=$!
sleep 1

# Wait for gateway to be ready
echo "Waiting for gateway to be ready..."
for i in {1..10}; do
    if curl -s "http://localhost:$GATEWAY_PORT/health" > /dev/null 2>&1; then
        echo "Gateway is ready"
        break
    fi
    if [ $i -eq 10 ]; then
        echo "ERROR: Gateway failed to start"
        exit 1
    fi
    sleep 0.5
done

echo ""
echo "=== Running integration tests ==="
cd "$PROJECT_ROOT"
go test -v ./test/integration/... -tags=integration

echo ""
echo "=== Integration tests passed ==="
