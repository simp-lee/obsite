---
title: 中英工程詞彙導航圖
aliases: [Bilingual Engineering MOC]
tags: [moc, documentation]
---
# 中英工程詞彙導航圖

這張 MOC 不追求把每個 English term 翻成唯一中文，而是記錄團隊在什麼 context 使用哪種說法，並連到可以看見實際決策的文章。

## Concurrency

- [[00-go-pipeline|pipeline cancellation／管線取消]]：goroutine lifecycle、channel ownership。
- [[01-backpressure|backpressure／背壓]]：buffer、queue age 與 overload contract。

## Reliability

- [[02-rollout-runbook|rollout／滾動發布]]：Kubernetes 觀察點與停止條件。
- [[03-slo-window|error budget／錯誤預算]]：不同 SLO windows 的用途。

## Editing rule

新詞條要附一個完整句子與來源連結；只列「官方翻譯」而沒有使用情境的項目不進主導航。[[05-terms-review|術語表要記錄 disagreement]] 說明 review 流程。
