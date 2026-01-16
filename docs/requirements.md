# Push Notification Infrastructure

Platform infrastructure enabling mobile devices to receive notifications when data arrives in OurCloud.

## Overview

Mobile devices cannot maintain persistent P2P connections due to OS restrictions (Android kills background connections). Push notifications bridge OurCloud's P2P data flow to mobile devices via Firebase Cloud Messaging (FCM).

This is **platform infrastructure**, not tied to any specific application. The social app is the first consumer, but backup and future apps use it independently.

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

The push gateway (`ourcloud-fcm-push-gateway`) is a standalone service that:
- Receives push requests from OurCloud nodes
- Validates sender signatures and consent
- Delivers notifications via Firebase Admin SDK

## Business Requirements

### BR-1: Mobile User Notification

Mobile users receive timely notifications when relevant data arrives at their nodes.

**Rationale:** Without push notifications, mobile users must manually refresh or wait for periodic polling, degrading user experience for real-time applications.

**Acceptance Criteria:**
- Android users receive notifications within 30 seconds of data arrival
- Notification latency is consistent regardless of device sleep state
- Works on devices with Google Play Services

### BR-2: User Consent Control

Users control who can send them push notifications via an explicit consent list.

**Rationale:** Prevents spam and unwanted notifications. Users must explicitly authorize senders.

**Acceptance Criteria:**
- Users maintain an explicit consent list of authorized senders
- Only authorized senders can trigger push notifications
- Users can add/remove senders from consent list
- Consent list is app-agnostic (managed at platform level)

### BR-3: Platform-Agnostic Infrastructure

Push infrastructure is not tied to any specific application.

**Rationale:** Multiple OurCloud applications (Social, Backup, future apps) need push capabilities. Infrastructure must be reusable without coupling.

**Acceptance Criteria:**
- Push system has no dependencies on social app code
- Social app calls platform push APIs, doesn't implement push logic
- New applications can use push without infrastructure changes
- Consent list is separate from app-specific data (e.g., friends list)

### BR-4: Future Extensibility

Architecture supports future notification triggers beyond direct pushes.

**Rationale:** Future requirement to notify nodes when data lands via Kademlia routing (not just direct pushes).

**Acceptance Criteria:**
- Architecture documented for extensibility
- No hardcoded assumptions about notification sources
- Clear extension points identified

## Functional Requirements

### FR-1: Push Endpoint Registration

Devices register push endpoints with the OurCloud DHT.

- Endpoint stored at `/users/{username}/platform/push/endpoints`
- Contains FCM device registration token for each registered device
- Label system guarantees authenticity (no separate signature needed)
- Multiple devices per user supported

### FR-2: Push Consent List Management

Users maintain an explicit list of authorized senders at platform level.

- Consent list stored at `/users/{username}/platform/push/consents`
- Contains usernames authorized to send push notifications
- Label system guarantees authenticity
- Applications call platform API to add/remove senders
- Initialized from friends list on first login (for social app)

### FR-3: Push Gateway API

Push gateway exposes HTTP API for nodes to request notifications.

```
POST /push         - Submit push request (returns request ID)
GET /status/{id}   - Query delivery status
```

- Gateway validates sender signature on request
- Gateway retrieves consent list from DHT
- Gateway validates sender is in consent list
- Delivery confirmation is best-effort (may never be confirmed)

### FR-4: Push Delivery

Push gateway delivers notifications via FCM.

- Uses Firebase Admin SDK directly (no intermediate gateway)
- Payload contains data IDs (device fetches actual content from DHT)
- Silent notification (data-only, no visible alert by default)
- Apps decide whether to show user-visible notification
- Notifications batched per target user to reduce frequency

### FR-5: Android UnifiedPush Integration

Android app uses UnifiedPush API as abstraction layer.

- App uses UnifiedPush connector API internally
- Embedded distributor bridges to FCM
- FCM device registration token stored in DHT (gateway looks up where to deliver)
- Gateway uses Firebase server credentials (from config) to send via Admin SDK
- Abstraction allows future support for alternative providers (ntfy)

### FR-6: Push Trigger Point

Push notifications are sent from `PutToPeer` at platform level.

- Fire-and-forget, parallel with DHT put
- Application code does not call push directly
- Failures are silent (device syncs on next app open)

## Non-Functional Requirements

### NFR-1: Reliability

Push delivery succeeds when device is reachable.

**Target:** 99% delivery success rate for reachable devices

### NFR-2: Latency

Notification delivered within acceptable timeframe.

**Target:** P95 latency < 10 seconds from push request to device receipt

**Note:** FCM itself may add variable latency, especially in Doze mode.

### NFR-3: Scalability

Push gateway handles expected load.

**Initial Target:** 1000 push requests per minute

**Scale Path:** Horizontal scaling via multiple gateway instances (stateless design with persistent batch queue).

### NFR-4: Security

Only authorized senders can trigger notifications.

**Controls:**
- Sender signature validation on push requests
- Consent list verification before delivery
- Label system guarantees consent list authenticity
- No user data in push payload (only data IDs)
- FCM credentials secured server-side only

### NFR-5: Graceful Degradation

Push failures do not affect primary functionality.

**Behavior:**
- Data storage succeeds even if push fails
- Push gateway timeouts handled gracefully
- Missing consent = no push (not an error)
- Missing endpoint = no push (not an error)

## Security Model

### Authentication Chain

1. **Push request arrives at gateway**
   - Gateway verifies sender signature using sender's public key from DHT

2. **Gateway reads consent list from DHT**
   - Label system guarantees data was signed by label owner (target user)

3. **Gateway reads endpoints from DHT**
   - Label system guarantees data was signed by label owner (target user)

4. **Gateway sends via FCM**
   - Firebase Admin SDK handles delivery authentication

### Why No Signatures in Stored Data?

The OurCloud label system already provides authentication. When you read `/users/bob/platform/push/consents`, the DHT verifies this label was signed by Bob. Adding signature fields to the protobuf would be redundant.

Only the push request (which is not stored as a label) needs explicit signature verification.

### Privacy Considerations

The consent list and endpoint list are stored **unencrypted** because the push gateway must read them. This means:

- The consent list reveals who can notify whom (social graph leakage)
- Push endpoints could potentially be correlated with other data

This is a known tradeoff. Users should be informed that their push consent relationships are visible to DHT participants.

## Constraints

### C-1: FCM Dependency

Push delivery depends on Google Firebase Cloud Messaging. Devices without Play Services cannot receive FCM push notifications.

**Mitigation:** UnifiedPush abstraction in Android app allows future support for alternative distributors (ntfy) without app changes.

### C-2: No Real-Time Guarantee

FCM does not guarantee instant delivery. Notifications may be delayed by FCM infrastructure, especially in Doze mode.

**Mitigation:** Silent notifications with app-driven sync ensure data is current when user opens app.

### C-3: No Delivery Confirmation Guarantee

FCM delivery receipts are not always available. The gateway returns a request ID for status queries, but status may remain "unknown" indefinitely.

**Mitigation:** Application logic should not depend on push delivery confirmation. Treat push as optimization, not guarantee.

## Extensibility

### Adding New Applications

1. Application calls `ConsentManager.AddConsent()` when it wants to allow push from a user
2. Application uses `PutToPeer` to send data (push happens automatically)
3. No push infrastructure changes needed

### Adding Alternative Distributors (ntfy)

1. User installs ntfy distributor app on Android
2. UnifiedPush connector auto-discovers external distributor
3. Endpoint changes to ntfy format
4. Gateway would need update to support ntfy protocol (future work)

### Adding Kademlia-Routed Notifications

Future extension - not in initial scope:

1. Define when nodes should be notified about data landing on them
2. Hook into DHT storage layer
3. Call push gateway when data lands
4. Consent model may need extension (consent by data pattern vs. sender)

## Out of Scope (Initial Release)

1. **iOS support** - APNs integration deferred
2. **Kademlia-routed data notifications** - Architecture supports, implementation deferred
3. **Push analytics dashboard** - Basic logging only initially
4. **Rate limiting** - Trusted network initially
5. **Alternative distributors (ntfy)** - App abstraction ready, backend deferred

## Dependencies

| Dependency | Type | Description |
|------------|------|-------------|
| Firebase Project | External | FCM server credentials |
| OurCloud DHT | Internal | Storage for endpoints and consents |
| UserAuth Resolution | Internal | Public key lookup for signature verification |

## Glossary

| Term | Definition |
|------|------------|
| Push Endpoint | FCM device registration token identifying where to send notifications for a device |
| FCM Device Registration Token | Unique token assigned by FCM to a specific app instance on a specific device |
| Push Consent List | Platform-level list of usernames authorized to send push |
| Silent Notification | Data-only push that doesn't show visible alert |
| UnifiedPush | Open standard for push notification delivery |
| FCM | Firebase Cloud Messaging (Google's push service) |
| Embedded Distributor | Android component that bridges UnifiedPush API to FCM |
