---
title: Cross-language documentation map
tags: [moc, documentation]
---
# Cross-language documentation map

這張 map 用 reader task 組織中英混合文件。目標不是每頁都有完整翻譯，而是讓不同語言背景的讀者找到同一個 operational truth。

## API compatibility

- [[00-protobuf-evolution|wire compatibility／線格式相容性]]：field number、version matrix。
- [[01-consumer-contract|consumer behavior／消費端行為]]：missing values、new enums、deployment order。

## Incident controls

- [[02-edge-cache-incident|cache purge 復盤]]：origin traffic 與 stale policy。
- [[03-breaker-probes|breaker probe budget]]：half-open recovery。

## Review path

每個入口標記 canonical source、last reviewed date 和 owner。翻譯若落後，頁面顯示差異範圍而不是假裝同步。[[05-translation-diff|翻譯 review 要看 semantic diff]] 提供檢查方法。
