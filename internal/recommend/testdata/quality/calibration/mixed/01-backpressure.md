---
title: Backpressure 不等於增加 buffer
---
# Backpressure 不等於增加 buffer

Queue 從 100 調到 10,000 可以延後阻塞，卻不會增加 downstream throughput。若 producer 長期比 consumer 快，大 buffer 只是把延遲和記憶體使用藏起來，直到 recovery 更困難。

## 先決定 overload contract

有些工作可以 reject，有些可以 coalesce，批次資料則可能寫入 durable queue。選擇應由資料價值與 deadline 決定，而不是所有 channel 都使用同一個常數。監控 queue age、enqueue wait 和 completion rate，比只看 length 更有意義。

## Cancellation 仍要能穿過滿佇列

送入 channel 的程式若只做 blocking send，context 取消也無法退出。用 select 同時等待 send 與 `ctx.Done()`，並確認 consumer 離開時 producer 也能收尾。完整的 lifecycle 可參考 [[00-go-pipeline|Go pipeline 的取消邊界]]。
