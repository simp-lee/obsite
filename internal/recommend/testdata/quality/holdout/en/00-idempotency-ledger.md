---
title: An Idempotency Ledger Needs an Outcome
tags: [distributed-systems, payments]
---
# An Idempotency Ledger Needs an Outcome

Storing an idempotency key prevents two workers from starting the same payment, but a row containing only “seen” cannot answer a retry after the first worker crashes. The ledger must distinguish in progress, completed, and failed outcomes, and it must preserve the response needed by the caller.

## Claim with an ownership window

The first request creates a key scoped to the operation and customer. A worker holds a lease while processing; another request can return the completed result or wait briefly, but it cannot silently launch a second side effect. Expired ownership requires reconciliation with the payment provider before a new attempt.

## Retention follows retry reality

Keys live longer than client retry windows and delayed messages. Cleanup records metrics and never reuses a key for a different payload. Message order after the operation is a separate concern, covered by [[01-message-order|Ordering Events per Aggregate]].
