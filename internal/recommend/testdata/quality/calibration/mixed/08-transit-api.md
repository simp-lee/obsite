---
title: Transit API 和站牌現場要一起驗證
---
# Transit API 和站牌現場要一起驗證

Developer portal 說 stop_id 是 stable identifier，現場卻可能在道路施工後移動站牌、合併月台或改變方向標示。只測 JSON schema，無法知道資料是否仍幫助乘客找到正確位置。

## Pick one journey

選一條包含轉乘的真實路線，記錄 API 的 stop name、coordinates、direction 與 realtime status。到現場後從下車點步行到下一站，觀察座標落在哪一側、站名是否一致，以及 wheelchair route 是否需要繞行。

## Report the mismatch precisely

問題單附 stop_id、timestamp、照片方向和預期修正，不上傳可識別乘客。若 realtime 缺值，區分車輛沒有回報與路線本來沒有服務。把 API 欄位與現場檢查項目排在同一頁，工程師和觀察者都能直接使用。
