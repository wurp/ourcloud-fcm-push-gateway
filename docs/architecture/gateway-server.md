# OurCloud FCM Push Gateway

Standalone HTTP service that receives push requests from OurCloud nodes and delivers notifications via FCM.

## Project Setup

New git repository: `ourcloud-fcm-push-gateway`

Dependencies:
- OurCloud Go client (for DHT reads)
- Firebase Admin SDK
- Protobuf (uses OurCloud proto definitions)

## HTTP API

### POST /push

Accepts push request, validates, queues for batched delivery.

**Request:** `PushRequest` protobuf
**Response:** `PushResponse` protobuf

Delivery is **not guaranteed to be confirmed**. The `request_id` allows status queries, but status may remain "unknown" indefinitely (FCM doesn't always confirm delivery).

### GET /status/{request_id}

Query status of a previously submitted request.

**Response:** `PushStatusResponse` protobuf

Status values: `queued`, `sent`, `failed`, `unknown`

### GET /health

Returns `{"status":"ok"}` when healthy.

## Handler Logic

```go
func (s *Server) HandlePush(ctx context.Context, req *pb.PushRequest) (*pb.PushResponse, error) {
    // 1. Validate request format
    if err := validateRequest(req); err != nil {
        return errorResponse(4, err.Error())
    }

    // 2. Get sender's public key from DHT, verify signature
    senderAuth, err := s.dht.GetUserAuth(req.SenderUsername)
    if err != nil {
        return errorResponse(3, "cannot verify sender")
    }
    if !verifySenderSignature(req, senderAuth.PublicSigningKey) {
        return errorResponse(3, "invalid signature")
    }

    // 3. Check target's consent list (label system verifies ownership)
    if !s.isAuthorized(ctx, req.TargetUsername, req.SenderUsername) {
        return errorResponse(2, "sender not in consent list")
    }

    // 4. Get target's endpoints
    endpoints, err := s.getEndpoints(ctx, req.TargetUsername)
    if err != nil || len(endpoints.Endpoints) == 0 {
        return errorResponse(1, "no endpoints registered")
    }

    // 5. Queue for batched delivery (persisted)
    requestID := s.batcher.Queue(req.TargetUsername, endpoints, &pb.DataNotification{
        DataId:         req.DataId,
        SenderUsername: req.SenderUsername,
    })

    return &pb.PushResponse{Accepted: true, RequestId: requestID}
}
```

## Batcher

Collects notifications per target user, sends in batches to reduce notification frequency and battery drain.

**Configuration:**
- `PUSH_BATCH_WINDOW`: Time before flush (default: 60s)
- `PUSH_BATCH_MAX_SIZE`: Max notifications before forced flush (default: 100)

**Persistence:** Queued batches are persisted to disk (or Redis/SQLite). On server restart, pending batches are reloaded and processed.

```go
type Batcher struct {
    store        BatchStore          // Persistent storage
    queues       map[string]*batch   // In-memory for active batches
    batchWindow  time.Duration
    maxBatchSize int
    sender       *FCMSender
}

type batch struct {
    targetUsername string
    endpoints      *pb.PushEndpointList
    notifications  []*pb.DataNotification
    requestIDs     []string
    timer          *time.Timer
}

func (b *Batcher) Queue(target string, endpoints *pb.PushEndpointList, notif *pb.DataNotification) string {
    requestID := generateRequestID()

    b.mu.Lock()
    defer b.mu.Unlock()

    queue, exists := b.queues[target]
    if !exists {
        queue = &batch{
            targetUsername: target,
            endpoints:      endpoints,
        }
        queue.timer = time.AfterFunc(b.batchWindow, func() { b.flush(target) })
        b.queues[target] = queue
    }

    queue.notifications = append(queue.notifications, notif)
    queue.requestIDs = append(queue.requestIDs, requestID)

    // Persist immediately
    b.store.Save(target, queue)

    if len(queue.notifications) >= b.maxBatchSize {
        queue.timer.Stop()
        go b.flush(target)
    }

    return requestID
}

func (b *Batcher) flush(target string) {
    // ... build PushPayload, send to all endpoints via FCM
    // Update status for all requestIDs: "sent" or "failed"
    // Remove from persistent store
}
```

## FCM Sender

Uses Firebase Admin SDK to send data messages.

```go
type FCMSender struct {
    client *messaging.Client
}

func (s *FCMSender) Send(ctx context.Context, endpoints *pb.PushEndpointList, payload *pb.PushPayload) error {
    payloadBytes, _ := proto.Marshal(payload)

    for _, endpoint := range endpoints.Endpoints {
        msg := &messaging.Message{
            Token: endpoint.FcmToken,
            Data: map[string]string{
                "payload": base64.StdEncoding.EncodeToString(payloadBytes),
            },
            Android: &messaging.AndroidConfig{
                Priority: "high",
            },
        }

        _, err := s.client.Send(ctx, msg)
        if err != nil {
            // Log but continue to other devices
            log.Printf("FCM send failed for device %s: %v", endpoint.DeviceId, err)
        }
    }
    return nil
}
```

## Configuration

```yaml
# config.yaml
server:
  port: 8080

firebase:
  credentials_file: /etc/pushserver/firebase-credentials.json
  project_id: ourcloud-push

dht:
  bootstrap_nodes:
    - /ip4/x.x.x.x/tcp/4001/p2p/QmXXX

batch:
  window_seconds: 60
  max_size: 100
  storage_path: /var/lib/pushserver/batches
```

## Deployment

```dockerfile
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o pushserver ./cmd/pushserver

FROM alpine:3.19
COPY --from=builder /app/pushserver /usr/local/bin/
COPY config.yaml /etc/pushserver/
EXPOSE 8080
CMD ["pushserver", "-config", "/etc/pushserver/config.yaml"]
```

## File Structure

```
ourcloud-fcm-push-gateway/
├── cmd/pushserver/main.go
├── internal/
│   ├── handler.go
│   ├── batcher.go
│   ├── sender.go
│   ├── consent.go
│   └── store.go
├── config.yaml
├── Dockerfile
└── go.mod
```

## Tests

### Unit Tests

| Component | Test Cases |
|-----------|------------|
| Consent check | sender in list, sender not in list, empty list |
| Signature verification | valid sig, wrong key, tampered request, missing sig |
| Batcher | queue first item starts timer, max size triggers flush, persistence survives restart |

### Integration Tests

| Test | Setup | Expected |
|------|-------|----------|
| Valid push | Bob consents Alice | Accepted, FCM called |
| No consent | Bob's list empty | Error code 2 |
| Bad signature | Tampered request | Error code 3 |
| No endpoint | Bob has no devices | Error code 1 |
| Status query | After queue | Returns "queued" |
| Status after send | After flush | Returns "sent" |
