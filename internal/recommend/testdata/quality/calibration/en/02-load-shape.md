---
title: Load Tests Need a Traffic Shape
tags: [performance, reliability]
---
# Load Tests Need a Traffic Shape

A test that sends ten thousand identical requests per second measures a benchmark endpoint, not necessarily the service. Real traffic has read/write ratios, large and small tenants, cacheable bursts, scheduled jobs, and clients that retry when latency rises.

## Model arrivals and work

Start with production distributions that are safe to aggregate: endpoint share, payload size bands, tenant concentration, and request deadlines. Reproduce diurnal ramps and one plausible burst. Open-loop arrivals reveal queue growth more honestly than workers that wait for each response before sending the next request.

## Define failure before running

Write down latency and error budgets, resource ceilings, and the stopping condition. A system that reaches throughput while building a thirty-minute queue has not passed. Record retry traffic separately so apparent demand does not conceal amplification.

Cache behavior can dominate the result. [[03-cache-collapse|Preventing a Cache Miss from Becoming a Stampede]] covers one burst pattern worth exercising.
