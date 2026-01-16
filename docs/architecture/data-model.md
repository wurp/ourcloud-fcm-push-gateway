# Push Notification Infrastructure Overview

Platform infrastructure enabling mobile devices to receive notifications when data arrives in OurCloud.

## Problem

Mobile devices cannot maintain persistent P2P connections (Android kills background connections). Push notifications bridge OurCloud's P2P data flow to mobile devices via FCM.

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        OurCloud Network                         │
│                                                                 │
│  ┌──────────┐         ┌─────────┐         ┌──────────┐         │
│  │  Alice   │──data──▶│   DHT   │◀──sync──│   Bob    │         │
│  │ (Desktop)│         └────┬────┘         │ (Mobile) │         │
│  └────┬─────┘              │              └────▲─────┘         │
│       │                    │ read              │               │
│       │ push request       │ consent/endpoint  │               │
│       ▼                    │                   │               │
│  ┌─────────────┐           │            ┌─────┴─────┐          │
│  │Push Gateway │───────────┘            │  Android  │          │
│  │  (Docker)   │                        │    App    │          │
│  └──────┬──────┘                        └─────▲─────┘          │
└─────────┼───────────────────────────────────────────────────────┘
          │ Firebase Admin SDK                  │
          ▼                                     │
     ┌─────────┐                                │
     │   FCM   │────────────────────────────────┘
     │(Google) │         silent push
     └─────────┘
```

## Data Model

### DHT Labels

| Label Path | Content | Purpose |
|------------|---------|---------|
| `/users/{username}/platform/push/endpoints` | `PushEndpointList` | Where to send notifications (all devices) |
| `/users/{username}/platform/push/consents` | `PushConsentList` | Who can send notifications |

Both labels are **unencrypted** (push gateway must read them). The label system guarantees authenticity via signature verification.

**Privacy note:** The consent list reveals who can notify whom. This is a known tradeoff documented for users.

### Protobuf Messages

Located in `src/proto/push_types.proto` (OurCloud project).

```protobuf
// Stored in DHT - list of all user's devices
message PushEndpointList {
  FormatVersion format_version = 1;
  repeated PushEndpoint endpoints = 2;
  int64 updated_at = 3;
}

message PushEndpoint {
  string device_id = 1;       // Unique per device (e.g., Android ID)
  string fcm_token = 2;       // Raw FCM registration token
  string device_name = 3;     // User-friendly name
  int64 registered_at = 4;
  int64 expires_at = 5;
}

// Stored in DHT - authorized senders
message PushConsentList {
  FormatVersion format_version = 1;
  repeated string authorized_senders = 2;  // Usernames
  int64 updated_at = 3;
}

// Push gateway API request
message PushRequest {
  FormatVersion format_version = 1;
  string target_username = 2;
  string sender_username = 3;
  bytes data_id = 4;
  int64 timestamp = 5;
  bytes sender_signature = 6;
}

// Push gateway API response
message PushResponse {
  FormatVersion format_version = 1;
  bool accepted = 2;          // Request accepted for processing
  string request_id = 3;      // For status queries
  int32 error_code = 4;       // 0=ok, 1=no endpoint, 2=not consented, 3=bad signature, 4=internal error
  string error_message = 5;
}

// Status query response
message PushStatusResponse {
  FormatVersion format_version = 1;
  string request_id = 2;
  string status = 3;          // "queued", "sent", "failed", "unknown"
  int64 sent_at = 4;          // If sent
  string error = 5;           // If failed
}

// Payload sent via FCM (batched)
message PushPayload {
  FormatVersion format_version = 1;
  repeated DataNotification notifications = 2;
  int64 timestamp = 3;
}

message DataNotification {
  bytes data_id = 1;
  string sender_username = 2;
}
```

## Push Flow

1. **Alice stores data** for Bob in DHT
2. **Alice calls push gateway** with signed `PushRequest`
3. **Gateway validates** signature using Alice's public key from DHT
4. **Gateway checks** Bob's consent list contains Alice
5. **Gateway queues** notification for batched delivery
6. **Gateway returns** `request_id` (delivery not guaranteed to be confirmed)
7. **Batcher flushes** after window (60s default) or max size (100)
8. **Gateway sends** via Firebase Admin SDK to all Bob's devices
9. **Bob's devices** receive silent FCM notification, fetch data from DHT

## Security Model

| Check | How |
|-------|-----|
| Sender authenticity | Signature on PushRequest verified against sender's public key in DHT |
| Consent list authenticity | Label system guarantees data signed by label owner |
| Endpoint authenticity | Label system guarantees data signed by label owner |
| Delivery authorization | Sender must be in target's consent list |

No signatures needed in stored protobufs—the label system provides authentication.

## Extensibility

- **New apps**: Call ConsentManager and PushClient APIs; no gateway changes needed
- **Alternative providers**: UnifiedPush abstraction on Android allows future ntfy support
- **Kademlia notifications**: Architecture supports future hook into DHT storage layer
