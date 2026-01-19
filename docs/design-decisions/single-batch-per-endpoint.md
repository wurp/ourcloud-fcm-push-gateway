# Design Decision: Single Batch Per Endpoint, Block During Flush

## Context

The batching system queues notifications per endpoint and flushes when batch window expires or max size is reached. Two questions:
1. What happens if new notifications arrive while a flush is in progress?
2. What happens if FCM is down and flush fails?

## Decision

1. Single batch per endpoint with per-endpoint lock. If notification arrives during flush, block briefly (configurable, default 100ms) waiting for flush to complete. Log warning when blocking occurs.
2. On flush failure, discard the batch and log an error. Do not retry.

## Rationale

**Blocking during flush:** Network calls take tens of milliseconds, so overlapping requests are expected occasionally. Brief blocking is simpler than managing multiple concurrent batches. Warning logs help monitor frequency.

**No retry:** Push is best-effort (per requirements). Retrying on FCM failure would cause unbounded accumulation if FCM is down for an extended period. Simpler to fail fast:
- Flush succeeds → done
- Flush fails → log error, discard batch, move on
- New notifications start fresh batches

## Alternative: Multiple Batches Per Endpoint

If blocking warnings appear frequently, consider allowing multiple concurrent batches:
- Add `batch_id INTEGER NOT NULL` to batches table (programmatically assigned, not autoincrement)
- Primary key becomes `(endpoint_type, endpoint_id, batch_id)`
- New batch_id assigned when current batch hits max size
- Remove blocking, allow concurrent flushes

## Monitoring

- **WARNING:** Notification blocked waiting for flush to complete
- **ERROR:** Block timeout exceeded (notification dropped)
- **ERROR:** Flush failed, batch discarded
