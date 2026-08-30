---
title: Go pipeline 的取消邊界
tags: [go, concurrency]
---
# Go pipeline 的取消邊界

在 Go 裡把 parser、transformer、writer 串成 channels 很容易；真正困難的是下游失敗後，誰負責讓上游停止。只關閉最後一個 channel 不會通知仍在送資料的 goroutine，最後常留下 leak。

## Context 傳遞的是生命週期

每個 stage 都 select `ctx.Done()`，但 cancel function 只由擁有整個 operation 的呼叫者執行。worker 發現錯誤時把第一個 error 送到有界的結果通道，coordinator 取消 context，再等待所有 goroutines 結束。這個順序避免 error channel 自己成為阻塞點。

## Close 的 ownership 要單一

producer 關閉自己唯一寫入的 channel；多個 worker 不搶著 close shared output，而由等待它們的 goroutine 關閉。測試除了比對結果，也要在 cancellation 後等待完成訊號。[[01-backpressure|Backpressure 不等於增加 buffer]] 討論 pipeline 還活著但已經跟不上輸入時的策略。
