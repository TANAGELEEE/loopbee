# 設計：模型與思考等級、多 agent 編排、流程圖

狀態：草稿，待審閱（2026-10-16）。本文件只描述設計，尚未實作。

支援的 agent CLI：Codex CLI、Claude Code、pi、OpenCode。Gemini CLI 的支援另案移除，不在本設計範圍內。

本文件分三個階段，每個階段可以單獨上線：

| 階段 | 目標 | 對現有 Loop 的影響 |
|---|---|---|
| 一 | 每個 Loop 和每個 agent 步驟都能選模型和思考等級；選項來自本機安裝的 CLI | 無；沒設定時沿用 CLI 預設 |
| 二 | 同一個 Loop 裡混用不同 agent；審查步驟可以把工作退回指定步驟，循環直到通過 | 無；舊 Loop 的行為不變 |
| 三 | Run 頁和編輯器的流程圖改成真正的流程圖：每個節點顯示 agent、模型、思考等級，畫出退回的箭頭 | 只改顯示 |

閱讀順序：先看「目標情境」，再看各階段的「資料模型」和「行為」。實作細節和測試放在各階段最後。

## 目標情境

以下流程來自使用者的需求，用來檢驗三個階段的設計是否足夠：

1. pi（模型 A）寫出實作方案。
2. pi（模型 B）審查方案；不通過就退回第 1 步，帶著審查意見重寫，直到通過。
3. 另一個 agent 依方案寫程式。
4. 再一個 agent 審查程式；不通過就退回第 3 步修改，直到通過。
5. 通過後，自動合併到測試環境。
6. 由 QA agent 在測試環境驗收；有問題時把問題整理好，退回第 3 步修正。
7. 修正後再經過程式審查、合併到測試環境，由 QA agent 複測，直到通過。

| 能力 | 現在 | 階段一 | 階段二 | 階段三 |
|---|---|---|---|---|
| 每一步指定模型和思考等級 | 不支援 | 支援 | 支援 | 顯示 |
| 每一步指定不同 agent | 不支援（一個 Loop 一個 agent） | 不支援 | 支援 | 顯示 |
| 審查不通過時退回指定步驟 | 不支援（審核關卡退回會結束 Run） | 不支援 | 支援 | 顯示退回箭頭與次數 |
| 前一步的報告交給後一步 | 只交接指令步驟的輸出 | 不變 | 支援 | 不變 |
| 自動合併到測試環境後再接驗收 | 不支援（交付固定在 Run 結尾） | 不支援 | 支援 | 顯示 |
| 驗收不通過時退回寫程式的步驟並複測 | 不支援 | 不支援 | 支援 | 顯示退回箭頭與次數 |

## 現況摘要

以下是設計依據的現有行為，出處為目前的 `main`。

- **Loop 與步驟**：`Loop.Agent` 決定所有 agent 步驟使用的 CLI；`Loop.Model` 是一個手動輸入的字串，空白時用 CLI 預設。`Step` 沒有自己的 agent、模型或權限。
- **輪次**：Run 依序執行步驟。「驗證」步驟失敗時，整輪從第一步重來，失敗輸出交給下一輪的 agent，最多 `MaxRounds` 輪。
- **審核關卡**：人工核准後繼續；退回時 Run 以 `rejected` 結束，不會回到任何步驟。
- **步驟間交接**：同一輪中，指令步驟（`run`、`verify`）的輸出會附加到之後 agent 步驟的提示裡。agent 步驟的最後報告存成 `round-NN-step-NN.report.md`，但不會交給後面的步驟。
- **交付**：`Loop.PullRequest` 在最後一個步驟（通常是審核關卡）之後推送分支並開 PR，可選「檢查通過後合併」。交付之後不能再接步驟。
- **安全規則**：agent 改過的檔案，要在人工審核關卡核准後，指令步驟才能執行它們（LESSONS「Code the agent wrote never runs as you before review」）。
- **權限等級**：每個 adapter 只列出自己 CLI 能強制執行的等級。pi 和 OpenCode 沒有沙箱，只有 `read-only` 和 `agent-config`。
- **用量**：每個 agent 步驟記錄 CLI 回報的模型和 token（`StepRecord.Usage`）。
- **流程圖**：`internal/ui/web/flow.js` 有兩個元件。`renderFlow` 把步驟畫成一條直線；`renderTopology` 固定畫「觸發 → loopbee 核心 → Loop 的 agent」，旁邊列出其他偵測到的 agent。

## 階段一：模型與思考等級

### 目標

- 建立和編輯 Loop 時，可以從本機 CLI 支援的模型和思考等級中挑選，不用記名稱。
- Loop 設預設值，個別 agent 步驟可以覆寫。
- 起草的 agent 知道有哪些選項，能依任務難易提出建議。
- Run 頁顯示每一步要求的和 CLI 實際回報的模型、思考等級。

### 各 CLI 的現況

以下是 2026-10-16 在開發者的 Mac 上查 `--help` 的結果。標「待確認」的項目，實作前要再查證。

| CLI | 指定模型 | 列出模型 | 思考等級 |
|---|---|---|---|
| Claude Code 2.1.296 | `--model <別名或完整名稱>` | 沒有指令；help 只舉別名為例 | `--effort low/medium/high/xhigh/max` |
| Codex CLI 0.162.1 | `--model <MODEL>` | `codex --help` 沒有列出模型的子指令；是否有其他來源待確認 | 透過 `-c model_reasoning_effort=…`；可用的值待確認 |
| pi 1.2.0 | `--model <pattern>`，也接受 `provider/id:<thinking>` | `pi --list-models [search]`，表格含 provider、model、是否支援 thinking | `--thinking off/minimal/low/medium/high/xhigh/max` |
| OpenCode v2.0.20 | `-m provider/model#variant` | `opencode models`，每行一個 `provider/model`（開發者的 Mac 上有 84 個）。它透過 OpenCode 的背景服務取得，第一次呼叫可能在服務啟動前回傳空白；`--standalone` 改用獨立的服務 | 以模型的 `#variant` 表示；每個模型有哪些 variant 待確認 |

### 資料模型

| 欄位 | 型別 | 說明 |
|---|---|---|
| `Loop.Model` | string | 既有。預設模型；空白為 CLI 預設 |
| `Loop.Effort` | string | 新增。預設思考等級；空白為 CLI 預設 |
| `Step.Model` | string | 新增，僅 agent 步驟。空白時沿用 `Loop.Model` |
| `Step.Effort` | string | 新增，僅 agent 步驟。空白時沿用 `Loop.Effort` |
| `StepRecord.Model`、`StepRecord.Effort` | string | 新增。這一步要求的模型和等級；實際模型仍記在 `Usage.Model` |

adapter registry（`internal/core/adapter.go`）新增：

| 欄位 | 說明 |
|---|---|
| `Efforts []string` | 這個 CLI 接受的思考等級，依低到高排列；空白表示不支援 |
| `EffortArgs func(level string) []string` | 把等級轉成 CLI 參數，例如 Claude Code 的 `--effort high`、pi 的 `--thinking high` |
| `ListModels func(ctx, base []string, env []string) ([]Model, error)` | 列出模型；不支援時為 nil |
| `ModelHint []string` | 列不出模型時提供的常用別名，例如 Claude Code 的 `opus`、`sonnet`（取自 CLI 說明，不自行編造） |

`Model` 包含 `ID`、`Label`、`Efforts`（這個模型支援的等級，未知時沿用 adapter 的 `Efforts`）。

### 行為

- **偵測**：`DetectAgents` 偵測 CLI 時順便列出模型，結果放進 `State.Agents[].Models`，連同取得時間。列出模型有逾時限制，失敗時不影響偵測結果，只記下原因。設定 › Agents 提供「重新列出模型」。
- **呼叫**：agent 步驟依序取用 `Step.Model`、`Loop.Model`、CLI 預設；思考等級同理。等級透過 `EffortArgs` 轉成參數。
- **驗證**：
  - 思考等級必須在該 adapter 的 `Efforts` 之內，否則儲存時拒絕並列出可用等級。
  - 模型維持自由輸入，只檢查格式（現有規則）。列表只是建議：CLI 可能支援列表沒有列出的模型。
  - 模型不在列表上時，編輯器顯示提醒，不擋儲存。
- **起草提示**：告訴設計者每個 agent 可用的模型（列表過長時只列前段並說明可自由輸入）、思考等級，以及選擇原則：簡單、重複的工作用較便宜的模型和較低等級；審查、規劃、困難的修正用較強的模型和較高等級。schema 加入 `effort` 和步驟層級的 `model`、`effort`。
- **用量**：`Usage.Model` 照舊記 CLI 回報的模型。CLI 不回報思考等級，所以等級只顯示「要求的值」，不顯示成實際值。

### 介面

- **編輯器 › Agent 區塊**：
  - 「模型」改成可輸入的下拉選單（`<input list>` 搭配 `<datalist>`），選項來自偵測結果，列不出時顯示別名。
  - 「思考等級」是下拉選單，選項是「CLI 預設」加上該 agent 的等級。
  - 換 agent 時，等級不在新 agent 的列表裡就清空，並提示。
- **編輯器 › 每個 agent 步驟**：同樣兩個欄位，預設顯示「沿用 Loop（模型 · 等級）」。
- **對話預覽**：事實欄加上「模型 · 思考等級」。
- **Run 頁**：步驟列與拓撲圖的 agent 卡片顯示「agent · 模型 · 等級」。實際回報的模型和要求的不同時，兩者都顯示。
- **設定 › Agents**：每個 agent 列出可用模型、思考等級和取得時間。

### 測試

- `TestEveryAdapterMeetsTheContract` 擴充：宣告 `Efforts` 的 adapter 必須有 `EffortArgs`，且每個等級都產生參數。
- `TestBuildAgentArgs` 擴充：各 adapter 的模型與等級參數，包含步驟覆寫 Loop 預設。
- 假 CLI 加上列模型的輸出（pi 的表格、OpenCode 的清單），測試解析與逾時。
- 儲存時拒絕不支援的等級，錯誤列出可用等級。
- 瀏覽器 E2E：編輯器的選單、步驟覆寫、Run 頁顯示。
- `scripts/real-smoke.sh`：每個已安裝的 CLI 用一個非預設等級跑一次，確認參數被接受、用量照常記錄。

## 階段二：多 agent 與退回循環

### 目標

- 每個 agent 步驟可以使用不同的 agent、模型、思考等級和權限等級。
- 新增「審查」步驟：由 agent 判斷通過或要求修改；要求修改時退回指定的前一個步驟，帶著審查意見重做，循環到通過或達到次數上限。
- 前面步驟的報告可以交給後面的步驟。
- 交付（推送分支、開 PR、檢查通過後合併）變成一個步驟，之後可以接驗收步驟。

### 資料模型

`Step` 新增：

| 欄位 | 型別 | 適用 | 說明 |
|---|---|---|---|
| `ID` | string | 全部 | 穩定識別碼，供其他步驟引用；名稱可以改，ID 不變。舊 Loop 載入時自動補上 |
| `Agent` | string | agent、審查 | 空白時沿用 `Loop.Agent` |
| `Sandbox` | string | agent、審查 | 空白時沿用 `Loop.Sandbox`；必須是該 agent 能強制執行的等級，且不超過裝置上限 |
| `Inputs` | []string | agent、審查 | 要交給這一步的前面步驟 ID；它們最新一次的報告會附加到提示裡 |
| `SendBackTo` | string | 審查 | 要求修改時退回的步驟 ID，必須在這一步之前 |
| `MaxAttempts` | int | 審查 | 這個審查最多退回幾次；預設值待確認（建議 3） |

步驟種類新增：

| 種類 | 說明 |
|---|---|
| `review` | agent 審查。結果合約新增狀態 `changes_requested`，摘要就是審查意見 |
| `deliver` | 推送分支並開 PR，可選檢查通過後合併；取代 `Loop.PullRequest`。舊 Loop 載入時，在最後補上一個等效的 `deliver` 步驟 |

`StepRecord` 新增 `Attempt`（這一步在這一輪的第幾次執行）和 `Agent`。

### 行為

- **執行順序**：同一輪內用「目前步驟」指標依序執行。審查回報 `changes_requested` 時，指標移回 `SendBackTo`，從那一步重新往下執行；中間的步驟也會重跑。
- **審查意見**：重做的步驟會在提示裡收到「審查意見：…（第 N 次退回）」。上一次的報告也一起附上，讓 agent 修改，而不是從頭寫。
- **次數上限**：同一個審查退回超過 `MaxAttempts` 次時，Run 以 `blocked` 結束，摘要寫明是哪個審查、最後一次的意見；通知照現有規則送出。
- **與輪次的關係**：輪次規則不變。「驗證」失敗時整輪重來，審查的退回次數也重新計算。審查循環是一輪之內的小循環。
- **時間上限**：所有步驟（含重做）共用 `WallMinutes`；退回循環不會超出這個上限。
- **報告交接**：`Inputs` 指定的步驟，取它在這一輪最新一次的報告，附在提示的「前面步驟的成果」段落，長度有上限，超出的部分會截斷並註明。指令步驟的輸出照現有規則交接。
- **工作區**：所有步驟共用同一個工作目錄或 worktree 與分支。agent 寫入的步驟結束後，loopbee 照現有規則提交。
- **不同 agent 的權限**：每一步依自己的 agent 檢查權限等級。pi 和 OpenCode 只能用於不需要寫檔的步驟（規劃、審查、驗收），它們的成果以報告交接。
- **交付步驟**：在 `deliver` 的位置執行現有的推送、開 PR、檢查通過後合併邏輯，成功後繼續執行後面的步驟。
- **重複交付**：退回會讓 `deliver` 再執行一次。loopbee 推送分支上的新提交，開新的 PR 或更新原本的 PR，檢查通過後再次合併到同一個目標分支。前一次的 PR 已經合併時，新 PR 只包含之後的提交；合併方式（squash、merge commit 或先 rebase）待確認。

### 自動合併到測試環境與 QA 驗收

這個流程可以自動交付到測試環境：只有 `deliver` 步驟可以使用 `pullRequest.mode: "automatic"`，不能放在 Loop 層級。必須明確指定非預設分支的測試目標（例如 `base: "test"`），並設定 `merge: "after-checks"`。儲存庫的預設分支永遠不能作為 `automatic` 的目標，即使人工核准了路徑變更也不例外；交付到預設分支應使用 `after-review`。

`reviewPaths` 僅適用於 `automatic`。變更命中其中任一路徑模式時，交付前暫停，等待人工核准；未命中的變更可直接交付。模式從儲存庫根目錄起算，以 `/` 分隔，不能使用絕對路徑或 `..` 路徑片段。`*`、`?` 只比對單一路徑片段；獨立的 `**` 比對零個或多個路徑片段。ConfigMap YAML 的位置必須先由使用者確認，再加入 `reviewPaths`；不可自行把所有 YAML 都視為需要人工核准。

合併和部署由 Git 平台與專案既有的 CI/CD 完成：

1. **合併**由 loopbee 的 `deliver` 步驟完成，不是由 agent 執行 `git merge`。agent 沒有 Git 權限（LESSONS「Agents do not write Git metadata; loopbee commits for them」）；loopbee 推送分支、開 PR，等 PR 的檢查全部通過後合併到測試分支（例如 `test`）。
2. **部署到測試環境**由專案原本的 CI/CD 在合併後完成，loopbee 不負責部署。SQL migration 也可以透過這條 CI/CD 流程自動交付；若其路徑命中 `reviewPaths`，仍須先取得人工核准。
3. **等待部署完成**：在 `deliver` 之後放一個 `run` 步驟，執行使用者在 Loop 定義裡寫好的指令，例如反覆查詢測試環境的版本，直到出現剛合併的提交。這個指令不是 agent 寫的，所以可以直接執行。
4. **QA 驗收**是一個 `review` 步驟，`sendBackTo` 指向寫程式的步驟。QA agent 回報 `changes_requested` 時，摘要就是整理好的問題清單；寫程式的 agent 在提示裡收到這份清單後修正。
5. **複測**：修正後依序重跑程式審查、`deliver`、等待部署、QA 驗收，直到 QA 通過或達到 `MaxAttempts`。

除了命中 `reviewPaths` 的變更，如果合併或部署是在使用者電腦上執行指令，而且會執行 agent 改過的檔案（例如本機部署腳本或 migration），仍須先經人工核准。`automatic` 和 agent 審查都不會放寬本機 `run`／`verify` 的安全限制；被提示注入的 agent 也可能讓另一個 agent 的審查通過。這條規則不變（LESSONS「Code the agent wrote never runs as you before review」）。CI 設定的拒絕規則也不變：`.github/` 下的變更即使經人工核准，loopbee 仍不會自動推送。

QA agent 要連到測試環境，需要 `workspace-network` 等級，或使用 agent 自己的 MCP 工具（例如瀏覽器）。

### 目標情境的定義範例

```json
{
  "name": "方案到驗收",
  "workspace": "worktree", "baseRef": "origin/main", "branch": "loopbee/feature-{date}",
  "agent": "codex", "sandbox": "workspace-write",
  "steps": [
    { "id": "plan", "kind": "act", "name": "寫實作方案", "agent": "pi", "sandbox": "read-only",
      "model": "<模型 A>", "effort": "high", "prompt": "…" },
    { "id": "plan-review", "kind": "review", "name": "審查方案", "agent": "pi", "sandbox": "read-only",
      "model": "<模型 B>", "effort": "xhigh", "inputs": ["plan"], "sendBackTo": "plan", "maxAttempts": 3, "prompt": "…" },
    { "id": "code", "kind": "act", "name": "依方案實作", "agent": "codex", "inputs": ["plan"], "prompt": "…" },
    { "id": "code-review", "kind": "review", "name": "審查程式", "agent": "claude", "sandbox": "read-only",
      "inputs": ["plan", "code"], "sendBackTo": "code", "maxAttempts": 3, "prompt": "…" },
    { "id": "deliver", "kind": "deliver", "name": "合併到 test（檢查通過後）", "pullRequest": { "mode": "automatic", "base": "test", "merge": "after-checks", "reviewPaths": ["deploy/k8s/**"] } },
    { "id": "deployed", "kind": "run", "name": "等待 test 環境部署完成", "argv": ["sh", "scripts/wait-for-test-deploy.sh"] },
    { "id": "qa", "kind": "review", "name": "QA 驗收 test 環境", "agent": "claude", "sandbox": "workspace-network",
      "inputs": ["plan"], "sendBackTo": "code", "maxAttempts": 3, "prompt": "…" }
  ]
}
```

`<模型 A>`、`<模型 B>` 是佔位符，實際值取自階段一的模型列表。範例假設 `test` 不是預設分支，而且使用者已確認 ConfigMap YAML 位於 `deploy/k8s/**`；命中這個範圍時，交付步驟會等待人工核准。`scripts/wait-for-test-deploy.sh` 代表專案自己的等待部署指令，必須保持未被 agent 修改，並在 `allowedTools` 中允許 `sh`。

QA 退回時的執行順序：`qa` → `code`（帶著 QA 的問題清單）→ `code-review` → `deliver` → `deployed` → `qa`。程式審查也可能在這之中把 `code` 再退回，兩個審查各自計算退回次數。

### 起草與編輯

- **起草提示**：說明 `review`、`sendBackTo`、`inputs`、`deliver` 的用法和安全限制，並提醒：寫檔的步驟只能用有沙箱的 agent。
- **編輯器**：
  - 每個步驟可以選 agent、權限、模型、等級。
  - 審查步驟多一個「退回到」選單，只列出前面的 agent 步驟。
  - 「交給這一步」用勾選方塊列出前面的步驟。
- **`beectl` SKILL 與 schema**：同步更新。

### 相容性

- 舊 Loop 沒有新欄位，行為不變。載入時補上步驟 ID，不升版本。
- **`Loop.PullRequest` 保留，不轉成 `deliver` 步驟。** 兩者的時機不同：`Loop.PullRequest` 在所有輪次結束、驗證通過後交付一次；`deliver` 步驟在流程中途，每一輪跑到它都會交付。多輪的 Loop（例如「修到測試通過，最多三輪」）轉換後會變成每輪都開 PR，行為改變。轉換也會改寫每一個已儲存的定義，並讓所有匯出檔都變成舊版無法讀取的格式。所以兩者並存：只要在結尾交付一次，用 Loop 層級的設定；要在中途交付（例如合併到 test 再做 QA），用 `deliver` 步驟。同一個 Loop 只能用其中一種，編輯器兩者共用同一組 PR 控制項。
- **匯出的 ZIP**：舊版（v0.1.4 以前）用寬鬆方式讀取 manifest，而且不驗證匯入的 Loop，讀到新欄位會直接丟掉，不會報錯。例如審查步驟的 `sendBackTo`、步驟較低的權限都會遺失，匯入後的 Loop 執行起來和原本不同，權限甚至可能變高。因此只要定義用到舊版不認識的欄位（`review`／`deliver` 步驟，步驟的 `agent`、`sandbox`、`inputs`、`model`、`effort`，或 Loop 的 `effort`），匯出檔的格式版本就是 2，舊版會整份拒絕（「unsupported archive version」）。只有步驟 ID 的匯出檔仍是版本 1，舊版忽略 ID 不會遺失任何行為。新版讀得了版本 1 和 2；遇到更新的版本，會說明要先更新 loopbee。
- **團隊快照**：舊版的檢視者同樣寬鬆讀取，看不到新欄位，新的步驟種類只顯示原始名稱。檢視者是唯讀的，不會執行這些 Loop，所以只是顯示不完整；團隊成員應更新到同一個版本。
- 定義匯入（`ParseDefinition`）、HTTP API 和 `beectl` 對不認識的欄位一律拒絕；它們和 loopbee 本身是同一個版本，新欄位都認得。

### 測試

- **執行器**：
  - 假 agent 審查先回報兩次 `changes_requested` 再通過：檢查重跑順序、`Attempt`、審查意見出現在重做步驟的提示裡。
  - 超過 `MaxAttempts`：Run 為 `blocked`，摘要正確，通知送出。
  - 混用 agent：每一步用自己的 CLI、模型、等級，用量分別記錄。
  - 權限：步驟的等級超出它的 agent 或裝置上限時，儲存被拒絕。
  - `Inputs`：報告依 ID 交接，長度上限生效。
  - `deliver` 之後的步驟會執行；交付失敗時不執行。
  - 安全：agent 審查之後、人工關卡之前的指令步驟，仍拒絕執行 agent 改過的檔案（擴充 `TestHostileAgentCannotEscapeThroughLoopbee`）。
  - 相容：舊 Loop 的 `PullRequest` 照常交付（`TestIssueIntakeToPullRequestAfterReview`）；用到新欄位的匯出檔是格式版本 2（`TestArchiveFormatFollowsWhatOlderVersionsWouldDrop`）。
- **瀏覽器 E2E**：在編輯器建立含審查的流程，執行到退回再通過。
- **`real-smoke.sh`**：新增 `review` 情境「規劃 → 審查退回一次 → 通過」，寫方案和審查各用一個不同的 agent（因此是不同模型）。

## 階段三：流程圖

### 目標

- 一眼看出流程的結構：步驟順序、哪個審查會退回哪一步、人工關卡和交付在哪裡。
- 每個節點顯示 agent · 模型 · 思考等級；執行時顯示第幾次嘗試、狀態、耗時和 token。
- 編輯器、對話預覽和 Run 頁使用同一個元件，前兩者顯示計畫，Run 頁顯示實際執行情況。

### 版面

- 步驟由左至右排成一列；寬度不夠時換到下一列，或改成直式。行動裝置寬度一律直式。
- 節點依種類區分外觀：agent、審查、指令、驗證、人工關卡、交付。
- 審查的退回畫成從審查節點指回目標節點的虛線弧線，標上「退回 N 次」。
- 「驗證失敗重來整輪」用外框表示目前是第幾輪。
- 拓撲卡片改成列出這個 Loop 實際用到的每個 agent，各自標出模型和等級，取代目前「一個 agent ＋其他 agent」的固定版面。

結構限制：階段二的流程是「一條步驟序列，加上往回的退回邊」，沒有平行分支，所以版面只要處理單列加回邊，不需要通用的圖形排版。是否需要平行步驟（例如兩個審查同時進行）待確認，不在本設計範圍內。

### 節點內容

| 狀態 | 顯示 |
|---|---|
| 計畫中（編輯器、預覽） | 種類、名稱、agent · 模型 · 等級（沿用 Loop 預設時加註） |
| 執行中 | 以上，加上「第 N 次」、已耗時；目前節點醒目標示，沿用現有的動畫 |
| 已完成 | 狀態、耗時、token、CLI 回報的模型（與要求不同時兩者都顯示） |
| 被退回 | 審查意見摘要，點開看全文和每一次的報告 |

點選節點後，右側面板顯示這一步每一次嘗試的報告、diff 和用量，沿用現有的「選中節點」面板。

### 實作限制

- 不引入前端框架（AGENTS.md）；在 `flow.js` 用 HTML 和 inline SVG 畫，弧線用 SVG path。
- 頁面 CSP 禁止 inline style；尺寸和顏色用 data 屬性加 CSSOM 設定（LESSONS「The CSP forbids inline styles」）。
- 元件的尺寸、間距、顏色放在 `loop.css`，不在 `app.css` 覆寫設計系統；新增的 class 名稱先在 `loop.css` 搜尋，避免撞名（LESSONS「A component's class must not reuse a design-system class」）。
- 動畫遵守「減少動態效果」設定與 Loopbee 的動畫開關。

### 測試

- `tests/screens.mjs`：新增含審查退回的流程截圖，淺色與深色、中英文。
- 瀏覽器 E2E：節點文字（agent · 模型 · 等級）、退回次數、目前節點、點選後的面板；960 px 與手機寬度不溢出。
- 7 個以上步驟時的換列與捲動。

## 待確認

1. Codex CLI 的思考等級可用值，以及除了手動輸入之外，有沒有其他取得模型列表的方式。
2. OpenCode 每個模型有哪些 variant（`opencode models` 不列出 variant），以及列模型時要不要固定用 `--standalone`。
3. `deliver` 重複合併到同一個目標分支時的合併方式。
4. 審查步驟的 `MaxAttempts` 預設值。
5. 是否需要平行步驟。

## 建議的推進方式

- 階段一獨立上線。它只新增選項，不改變現有 Loop 的行為，且能先累積各 CLI 的實測結果。
- 階段二與階段三一起上線。只有階段二沒有階段三時，退回循環在 Run 頁看不出來；只有階段三沒有階段二時，流程圖只是換外觀。
- 每個階段結束前跑一次 `scripts/real-smoke.sh`。
