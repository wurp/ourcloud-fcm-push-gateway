# Design Decision: FCM-Only Initially, UnifiedPush Later

## Context

The push gateway delivers notifications to mobile devices. Currently targeting Android via Firebase Cloud Messaging (FCM). UnifiedPush is an open standard that supports alternative distributors (ntfy, etc.) for users who don't want Google dependencies.

## Decision

Implement FCM-only delivery initially. Design for UnifiedPush support as a future addition.

## Rationale

**FCM and UnifiedPush have different delivery mechanisms:**

| Provider | Mechanism |
|----------|-----------|
| FCM | Firebase Admin SDK with device tokens |
| UnifiedPush (ntfy, etc.) | HTTP POST to a URL |

Abstracting over both now would require guessing at the right interface before having concrete requirements. The risk of building the wrong abstraction outweighs the cost of later refactoring.

**Refactor scope when adding UnifiedPush:**

1. `PushEndpoint` proto: add endpoint type and URL fields
2. Persistence schema: store endpoint type alongside target
3. Sender layer: dispatch to FCM SDK or HTTP POST based on type
4. Batcher: may need to batch by endpoint type as well as target

This is localized to the delivery path and doesn't affect the validation pipeline or API.

## UnifiedPush Integration Notes

When implementing UnifiedPush support:

- Android app already uses UnifiedPush connector API internally (per requirements.md)
- Embedded distributor bridges to FCM currently
- User-installed distributors (ntfy) would provide HTTP endpoint URLs
- Endpoint format in DHT would indicate type (FCM token vs URL)
- Gateway reads endpoint type and dispatches accordingly

## References

- [requirements.md](../requirements.md) - FR-5: Android UnifiedPush Integration
- [requirements.md](../requirements.md) - Extensibility: Adding Alternative Distributors
