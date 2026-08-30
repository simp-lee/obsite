---
title: Protobuf schema evolution 要看 wire compatibility
tags: [api, distributed-systems]
---
# Protobuf schema evolution 要看 wire compatibility

把 field 從 `string` 改成 `int64` 看起來只是型別更精確，wire type 卻已不同。Rolling deployment 期間，新舊 producer 與 consumer 會交錯存在，單看最新 generated code 無法證明相容。

## Field number 永遠比名稱重要

刪除欄位後要 reserve number 和舊名稱，避免未來重用。新增欄位提供語意安全的 default，consumer 也不能把 unknown fields 當成錯誤。需要改型別時，建立新 field，雙寫並觀察讀取，再停止舊 field。

## Test a version matrix

保存前一版與下一版 message fixtures，測試 old writer/new reader、new writer/old reader 和 round trip。若資料會經過 JSON gateway，還要檢查 field name 與 enum 表示。[[01-consumer-contract|Consumer contract 不只驗 schema]] 補充行為層的相容性。
