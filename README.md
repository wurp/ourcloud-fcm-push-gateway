# OurCloud FCM Push Gateway

Push notification gateway bridging OurCloud DHT to Firebase Cloud Messaging.

## Prerequisites

- Go 1.24+
- Local clone of `friendly-backup-reboot` at `../friendly-backup-reboot` (for ourcloud-client and ourcloud-proto dependencies)

## Build

```
go build ./...
```

## Test

```
go test ./...
```

Verbose output:
```
go test -v ./...
```

## Run

Requires a running OurCloud node (default: localhost:50051) and a config file:

```
go run ./cmd/pushserver -config config.yaml
```

See `config.yaml.example` for configuration options.

## Project Structure

- `cmd/pushserver/` - Main entry point
- `internal/config/` - Configuration loading
- `internal/ourcloud/` - OurCloud DHT client wrapper and signature verification
