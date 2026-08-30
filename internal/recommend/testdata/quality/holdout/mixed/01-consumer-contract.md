---
title: Consumer contract 不只驗 schema
---
# Consumer contract 不只驗 schema

Event 通過 schema registry，不代表 consumer 能正確處理。欄位可能 optional 卻在業務上必填，enum 新值也可能落入沒有監控的 default branch。

## Capture behavior examples

Contract test 應包含正常、缺值、unknown enum、duplicate delivery 和 out-of-order cases。Producer 提供 fixtures 與語意說明，consumer 在自己的 CI 執行，避免中央測試替所有服務猜行為。

## Deployment follows dependency direction

能容忍新資料的 consumer 先發布，producer 再開始寫入；移除舊欄位則反向確認所有讀者。Wire-level 變更規則見 [[00-protobuf-evolution|Protobuf schema evolution]]，本篇聚焦 application semantics。
