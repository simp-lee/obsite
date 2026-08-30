---
title: Kubernetes rollout runbook 要寫觀察點
tags: [kubernetes, operations]
---
# Kubernetes rollout runbook 要寫觀察點

只列出 `kubectl rollout status` 的 runbook，最多證明 Deployment controller 完成換 Pod。它沒有回答新版本是否處理真實 traffic、readiness 是否太寬鬆，或舊 Pod 結束時有沒有丟 request。

## Before rollout

記錄 image digest、config revision、預期 replica 數和 rollback owner。確認 PodDisruptionBudget 不會和節點維護互相卡住，也確認 database migration 已進入相容階段。

## During rollout

每個 batch 比較新舊版本的 success rate、p99 latency 和 dependency errors。若只看 aggregate，少量 canary 問題會被舊版本流量稀釋。停止條件寫成數值與時間窗，值班者不必現場猜測。

## After rollout

等待 termination grace period 完整走過，再查 dropped connections 與 queue lag。[[03-slo-window|SLO window 如何影響發布判斷]] 說明為何短期 rollout signal 不能直接替代長期 error budget。
