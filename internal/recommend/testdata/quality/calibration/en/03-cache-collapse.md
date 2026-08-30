---
title: Preventing a Cache Miss from Becoming a Stampede
tags: [performance]
---
# Preventing a Cache Miss from Becoming a Stampede

When a popular key expires, hundreds of requests can discover the miss together and all call the same database. The cache was meant to protect the dependency, yet synchronized expiration turns it into a load multiplier.

## Coordinate refreshes

Single-flight locking lets one request refresh while peers wait or receive a slightly stale value. The lock needs a deadline and ownership token so a crashed refresher does not block the key indefinitely. Adding random jitter to expiration spreads unrelated keys over time.

## Decide when stale is acceptable

A catalog description may be served stale for a minute; a revoked permission may not. Encode that distinction in cache policy rather than a global fallback. Monitor refresh latency, stale responses, lock contention, and origin requests per key class.

A shaped load test should include expiration waves and slow origins, not only a warm-cache steady state. See [[02-load-shape|Load Tests Need a Traffic Shape]].
