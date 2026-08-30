---
title: Circuit breaker recovery 需要 probe budget
---
# Circuit breaker recovery 需要 probe budget

Circuit breaker 從 open 進入 half-open 時，如果所有 instance 同時送 probe，剛恢復的 dependency 會再次被打垮。Recovery policy 需要一個共享或近似共享的 probe budget。

## Limit attempts, not only callers

每個時間窗只允許少量 requests 通過，其餘維持 fast failure。Probe 要代表真實 operation，但避開不可安全重試的副作用。成功一次不足以 close breaker，應觀察連續結果與 service time。

## Add jitter to state transitions

Instances 使用不同 jitter，並記錄 breaker state、probe count 和 rejection reason。人工 override 有明確 expiry。Edge cache incident 的 cold-origin recovery 也需要這種漸進流量，案例見 [[02-edge-cache-incident|Edge cache purge incident 復盤]]。
