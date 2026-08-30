---
title: A Queue Can Hide Saturation Until It Is Too Late
tags: [reliability, performance]
---
# A Queue Can Hide Saturation Until It Is Too Late

During a campaign launch, an image-processing service kept returning successful uploads while its queue grew from two minutes to four hours. CPU stayed below sixty percent because workers were blocked on a downstream object store. The API looked healthy; the promised completion time was already impossible.

## Reconstruct the hour

At 10:00, arrivals doubled but completions remained flat. At 10:12, clients began polling more often, adding read pressure. At 10:25, operators increased queue consumers, which opened more downstream connections and worsened object-store throttling. The useful graph combined arrival rate, completion rate, age of oldest item, and dependency latency.

## Protect the promise, not the enqueue call

The service now rejects or defers work when predicted completion exceeds its product deadline. Admission uses queue age and recent service time, while a separate limit caps active downstream requests. Clients receive an explicit delayed status rather than a misleading immediate success.

## Verify recovery behavior

Draining the queue after load falls can still overload the dependency. Recovery raises concurrency gradually and watches completion rate instead of worker count. A load exercise includes polling traffic and a deliberately slow object store. [[03-adaptive-concurrency|Concurrency Limits Should Follow Service Time]] explains the controller used after this incident.

The case changed the team’s primary objective from “never reject uploads” to “give an honest, bounded completion outcome.” That decision reduced peak acceptance but made both overload and recovery observable.
