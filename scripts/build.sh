#!/bin/bash
# Build all binaries for the push gateway project

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
OUT_DIR="${1:-$PROJECT_ROOT/bin}"

mkdir -p "$OUT_DIR"

echo "=== Building binaries ==="
cd "$PROJECT_ROOT"

echo "Building pushserver..."
go build -o "$OUT_DIR/pushserver" ./cmd/pushserver

echo "Building ourcloud-stub..."
go build -o "$OUT_DIR/ourcloud-stub" ./cmd/stubs/ourcloud-stub

echo "Building fcm-stub..."
go build -o "$OUT_DIR/fcm-stub" ./cmd/stubs/fcm-stub

echo ""
echo "Build complete. Binaries in $OUT_DIR:"
ls -la "$OUT_DIR/"
