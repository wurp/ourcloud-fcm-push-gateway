# OurCloud FCM Push Gateway

## Commands

```bash
# Build all binaries (pushserver, stubs) → bin/
./scripts/build.sh

# Run unit tests
go test ./...

# Run integration tests (requires build first)
./test/integration/run.sh

# Run specific package tests
go test -v ./internal/batcher/...
```

## Test Data

Integration test fixtures are in `test/integration/fixtures.json`. This defines test users, their consent lists, and FCM endpoints. The OurCloud stub loads this file and serves it via gRPC.
