---
title: A Transactional Outbox Has a Cleanup Cost
---
# A Transactional Outbox Has a Cleanup Cost

The outbox pattern closes a dangerous gap: business data and the intent to publish an event commit in one transaction. It does not make delivery free. A relay can publish twice, old rows accumulate, and a stuck partition can hide behind an apparently healthy writer.

## Consumers still need identity

Each message carries a stable event ID. Consumers record or otherwise recognize processed IDs at the same boundary as their own state change. Ordering is defined per aggregate where required, not assumed across the whole table.

## Retention follows evidence

Delete outbox rows only after the broker acknowledgment and the chosen recovery window. Monitor age of the oldest unpublished row, relay attempts, and table growth. Partitioning by creation time can simplify cleanup, but dropping a partition must not remove events still awaiting publication.

The resulting event stream may later rebuild projections, as described in [[00-event-log|Rebuilding State from an Event Log]].
