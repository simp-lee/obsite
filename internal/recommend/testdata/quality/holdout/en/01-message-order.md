---
title: Ordering Events per Aggregate
---
# Ordering Events per Aggregate

A broker can preserve order inside one partition while the application still observes an invalid sequence. If events for the same account use different partition keys, or a retry publishes an older transition later, global arrival order offers no useful guarantee.

## Carry an aggregate version

Each state transition increments a version. Consumers accept the next version, ignore an exact duplicate, and hold or reject a gap according to a bounded policy. This makes the expected sequence explicit without requiring every account to share one global queue.

## Gaps need an exit

A missing version may arrive late, be permanently lost, or have failed validation. Track gap age and provide a way to fetch current state or replay a narrow range. Infinite buffering simply moves an outage into memory.

Operation identity from [[00-idempotency-ledger|An Idempotency Ledger Needs an Outcome]] prevents duplicate effects; aggregate versions protect transition order.
