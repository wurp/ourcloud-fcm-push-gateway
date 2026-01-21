FROM golang:1.24-alpine AS builder

WORKDIR /build

# Install git for module downloads and gcc/musl-dev for CGO (required by SQLite)
RUN apk add --no-cache git gcc musl-dev

# Copy source code (expects vendored dependencies or proper module setup)
COPY . .

# Download dependencies
RUN go mod download

# Build the binary (CGO required for SQLite)
RUN CGO_ENABLED=1 GOOS=linux go build -o pushserver ./cmd/pushserver

FROM alpine:3.19

WORKDIR /app

# Install ca-certificates for HTTPS
RUN apk add --no-cache ca-certificates

# Copy binary from builder
COPY --from=builder /build/pushserver /app/pushserver

# Create directory for config and data
RUN mkdir -p /etc/pushserver /var/lib/pushserver/batches

EXPOSE 8080

ENTRYPOINT ["/app/pushserver"]
CMD ["-config", "/etc/pushserver/config.yaml"]
