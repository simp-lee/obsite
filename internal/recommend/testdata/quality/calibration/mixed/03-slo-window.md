---
title: SLO window 如何影響發布判斷
tags: [sre]
---
# SLO window 如何影響發布判斷

A 30-day availability SLO 適合管理 error budget，卻不適合單獨判斷剛上線五分鐘的新版本。長 window 會把尖銳 regression 稀釋；太短 window 又會被小樣本擺動。

## 用兩種時間尺度

發布時同時看短 window burn rate 和較長 confirmation window。短窗快速發現問題，長窗排除單一 burst。兩者都以 user outcome 為分母，而不是只看 Pod health。

## Budget 是決策輸入

剩餘 budget 很少時，可以降低 rollout speed 或暫停非必要變更；budget 充足也不代表允許明顯 regression。判斷仍需對照版本與 dependency 狀態。具體觀察流程放在 [[02-rollout-runbook|Kubernetes rollout runbook]]。
