FROM golang:1.21-alpine AS builder

WORKDIR /build

# Install git for module downloads
RUN apk add --no-cache git

# Copy source code (expects vendored dependencies or proper module setup)
COPY . .

# Download dependencies
RUN go mod download

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -o pushserver ./cmd/pushserver

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
