---
title: Concurrency Limits Should Follow Service Time
---
# Concurrency Limits Should Follow Service Time

A fixed worker count is safe only for a narrow dependency latency. When database or object-store service time triples, the same concurrency creates three times as many in-flight resources and can deepen the slowdown.

## Adjust from completed work

A controller observes recent service time and errors, then changes the active limit in small steps. Queue length alone is not enough: a long queue may reflect a burst, while rising service time shows that more parallel work is no longer helping. Minimum and maximum limits keep the controller within tested bounds.

## Recovery should be slower than collapse

Reduce concurrency quickly when latency and throttling rise; increase it gradually after several healthy windows. Record every limit change with its inputs so operators can distinguish control behavior from manual intervention.

The queue incident in [[02-queue-saturation|A Queue Can Hide Saturation]] shows why adding workers during dependency throttling can be harmful.
