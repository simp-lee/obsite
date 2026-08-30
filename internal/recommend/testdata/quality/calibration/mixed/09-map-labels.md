---
title: 雙語地圖 label 的閱讀順序
tags: [maps, localization]
---
# 雙語地圖 label 的閱讀順序

把中文與 English 全部塞進同一個 map label，資訊可能完整卻難以掃讀。字級、換行和方向資訊的優先順序，應由使用場景決定。

## Start from a decision point

在轉彎或月台入口，旅客先需要方向與路線號，再需要完整地名。兩種語言保持相同分組，不要讓中文站名和英文出口說明錯位。長名稱要測試實際看板寬度，而不是只看設計稿。

## Validate with movement

請第一次到訪者沿路尋找一個目的地，記錄停頓與回頭位置。API 座標正確仍可能對應到難以辨識的入口；前一篇 transit field check 提供資料與現場比對方法。
