---
title: 翻譯 review 要看 semantic diff
---
# 翻譯 review 要看 semantic diff

Source paragraph 改了三行，translation pull request 不一定也只改三行。Reviewer 先比較 meaning：條件是否變嚴、例外是否新增、責任人是否改變，再處理語氣與詞彙。

## Mark risky sentences

包含 MUST、時間限制、資料刪除或 security boundary 的句子列為 high risk，由 domain owner 複核。Code identifiers 保持原樣，第一次出現時補中文解釋。純排版變更則不必觸發整篇重譯。

## Record intentional divergence

某些地區流程不同時，不要偷偷改 translation；加上明確 locale note 與 owner。Cross-language navigation 的 canonical source 規則收在 [[04-cross-language-moc|documentation map]]。
