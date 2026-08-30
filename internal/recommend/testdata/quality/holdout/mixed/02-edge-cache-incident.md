---
title: Edge cache purge incident 復盤
tags: [cdn, reliability]
---
# Edge cache purge incident 復盤

一次首頁更新呼叫 global purge，原本分散在五分鐘內的 origin traffic 在十秒內回源。CDN hit rate 從 96% 掉到 8%，origin autoscaling 還沒完成，p99 latency 先超過 timeout，client retries 又放大流量。

## Timeline

14:02 deploy 完成，release job 發出 wildcard purge。14:03 database connection pool 滿載，首頁與 API 共用的 ingress 開始排隊。14:05 團隊暫停 retry-heavy mobile endpoint，並讓 CDN 對可接受的頁面 serve stale。14:11 hit rate 恢復到 70%，error rate 才開始下降。

## Root cause 不是單一按鈕

Purge scope 過大只是 trigger。模板資產與 API 使用同一 cache namespace、origin 沒有 admission control、runbook 又把 purge 當成低風險步驟，三者共同造成 incident。修正後，release 只 invalidates versioned keys，global purge 需要額外 approval。

## Recovery test

Staging 無法重現真實 edge volume，因此我們建立 synthetic cold-cache drill：限制 origin concurrency，逐步清除一小組 keys，觀察 queue age、hit rate 與 stale response。[[03-breaker-probes|Circuit breaker recovery 需要 probe budget]] 描述下游恢復時的另一個控制面。

這次復盤最重要的改變，是把 cache operation 視為 traffic-shaping change，而不是單純內容更新。
