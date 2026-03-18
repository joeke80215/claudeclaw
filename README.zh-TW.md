<p align="center">
  <img src="images/claudeclaw-banner.svg" alt="ClaudeClaw Banner" />
</p>
<p align="center">
  <img src="images/claudeclaw-wordmark.png" alt="ClaudeClaw Wordmark" />
</p>

<p align="center">
  <img src="https://awesome.re/badge.svg" alt="Awesome" />
  <a href="https://github.com/moazbuilds/ClaudeClaw/stargazers">
    <img src="https://img.shields.io/github/stars/moazbuilds/ClaudeClaw?style=flat-square" alt="GitHub Stars" />
  </a>
  <a href="https://github.com/moazbuilds/ClaudeClaw/commits/master">
    <img src="https://img.shields.io/github/last-commit/moazbuilds/ClaudeClaw?style=flat-square" alt="Last Commit" />
  </a>
  <a href="https://github.com/moazbuilds/ClaudeClaw/issues">
    <img src="https://img.shields.io/github/issues/moazbuilds/ClaudeClaw?style=flat-square" alt="Open Issues" />
  </a>
  <a href="https://x.com/moazbuilds">
    <img src="https://img.shields.io/badge/X-%40moazbuilds-000000?style=flat-square&logo=x" alt="X @moazbuilds" />
  </a>
</p>

<p align="center"><b>一個輕量、開源的 OpenClaw 替代方案，內建於你的 Claude Code 之中。</b></p>

<p align="center">
  <a href="README.md">English</a> | 繁體中文
</p>

ClaudeClaw 將你的 Claude Code 變成一個永不休息的個人助理。它以背景常駐程式（daemon）執行，按排程自動處理任務、透過 Telegram 和 Discord 回覆訊息、轉譯語音指令，並可整合任何你需要的服務。

> 注意：請勿將 ClaudeClaw 用於入侵銀行系統或任何違法活動。謝謝。

## 為什麼選擇 ClaudeClaw？

| 項目 | ClaudeClaw | OpenClaw |
| --- | --- | --- |
| Anthropic 會找上你嗎 | 不會 | 會 |
| API 開銷 | 直接使用你的 Claude Code 訂閱 | 惡夢 |
| 安裝與設定 | 約 5 分鐘 | 惡夢 |
| 部署方式 | 在任何裝置或 VPS 安裝 Claude Code 即可執行 | 惡夢 |
| 隔離模型 | 基於資料夾隔離，按需配置 | 預設全域存取（安全性惡夢） |
| 穩定性 | 簡潔可靠的代理系統 | Bug 惡夢 |
| 功能範圍 | 輕量化，只有你真正需要的功能 | 超過 60 萬行程式碼的惡夢 |
| 安全性 | 一般 Claude Code 使用等級 | 惡夢 |
| 成本效益 | 高效使用 | 惡夢 |
| 記憶體制 | 使用 Claude 內建記憶系統 + `CLAUDE.md` | 惡夢 |

## 5 分鐘快速上手

```bash
claude plugin marketplace add moazbuilds/claudeclaw
claude plugin install claudeclaw
```

接著開啟一個 Claude Code 工作階段並執行：

```
/claudeclaw:start
```

設定精靈會引導你完成模型選擇、心跳排程、Telegram、Discord 和安全層級的設定，隨後你的常駐程式就會上線，並附帶一個 Web 儀表板。

## 功能特色

### 自動化

- **心跳（Heartbeat）：** 定期自動簽到，可設定間隔時間、安靜時段（quiet hours），以及自訂提示詞。
- **排程任務（Cron Jobs）：** 支援時區感知的 5 欄位 cron 語法，可排定重複或單次任務，確保準時執行。

### 通訊整合

- **Telegram：** 支援文字、圖片與語音訊息。
- **Discord：** 支援私訊、伺服器中的 @ 提及與回覆、斜線指令（slash commands）、語音訊息，以及圖片附件。
- **時間感知：** 訊息附帶時間前綴，協助 AI 理解回覆延遲和日常行為模式。

### 穩定性與控制

- **GLM 備援：** 主要模型達到使用上限時，自動切換至 GLM 模型繼續運作。
- **Web 儀表板：** 即時管理排程任務、監控執行狀態、檢閱日誌。
- **安全層級：** 四種存取等級，從唯讀到完全系統存取。
- **模型切換：** 依工作負載隨時切換使用的模型。

### 語音辨識

- **Whisper 整合：** 透過 whisper.cpp 進行本地端語音轉文字。
- **跨平台支援：** 提供 Linux（x64/ARM64）、macOS（x64/ARM64）、Windows（x64）的預編譯二進位檔。
- **自訂 API：** 可透過 `stt.baseUrl` 設定，將語音辨識路由至 OpenAI 相容 API。

### 技能系統（Skills）

- **自訂技能：** 在 `skills/<skill-name>/SKILL.md` 中定義 Markdown 格式的技能，附帶 YAML 前置資料（frontmatter）。
- **三層搜尋：** 自動探索專案層級、全域層級，以及外掛套件層級的技能。
- **內建技能：** `create-skill`（技能建立精靈）、`install-skill`（從 skills.sh / GitHub 安裝）、`telegram-react`（Telegram 表情回應）。

## 指令參考

### 常駐程式控制

```bash
# 啟動常駐程式
bun run src/index.ts start

# 開發模式（含 Web UI 與熱重載）
bun run dev:web

# 附帶 Web 儀表板啟動
bun run src/index.ts start --web

# 單次執行提示詞
bun run src/index.ts start --prompt "你的指令"

# 執行啟動觸發器
bun run src/index.ts start --trigger

# 查看狀態
bun run src/index.ts status

# 停止常駐程式
bun run src/index.ts stop

# 備份並重設工作階段
bun run src/index.ts clear
```

### 單次訊息發送

```bash
# 發送提示詞至運行中的常駐程式
bun run src/index.ts send "你的指令"

# 發送並轉發結果至 Telegram
bun run src/index.ts send "你的指令" --telegram

# 發送並轉發結果至 Discord
bun run src/index.ts send "你的指令" --discord
```

### 機器人獨立模式

```bash
# 以獨立模式啟動 Telegram 機器人
bun run src/index.ts telegram

# 以獨立模式啟動 Discord 機器人
bun run src/index.ts discord
```

### 透過 Claude Code 技能介面

```
/claudeclaw:start                    # 啟動常駐程式
/claudeclaw:stop                     # 停止常駐程式
/claudeclaw:status                   # 查看狀態
/claudeclaw:config show              # 顯示目前設定
/claudeclaw:config heartbeat         # 設定心跳排程
/claudeclaw:config telegram          # 設定 Telegram
/claudeclaw:config model             # 切換模型
/claudeclaw:jobs list                # 列出排程任務
/claudeclaw:jobs create              # 建立新任務
/claudeclaw:logs                     # 檢閱日誌
/claudeclaw:help                     # 查看說明
```

## 架構概覽

### 技術堆疊

- **執行環境：** Bun（TypeScript，ESM 模組）
- **目標語法：** ESNext，bundler 模組解析
- **Go 重寫：** 進行中（位於 `go-rewrite/`），使用 Go 1.24.7

### 目錄結構

```
claudeclaw/
├── src/                          # TypeScript 主程式碼
│   ├── index.ts                  # CLI 進入點與指令路由
│   ├── runner.ts                 # 主要常駐迴圈
│   ├── config.ts                 # 設定管理
│   ├── cron.ts                   # Cron 表達式匹配
│   ├── jobs.ts                   # 排程任務解析
│   ├── sessions.ts               # Claude Code 工作階段管理
│   ├── skills.ts                 # 技能探索與路由
│   ├── whisper.ts                # 語音轉文字
│   ├── commands/                 # CLI 指令處理器
│   │   ├── start.ts              #   常駐程式初始化
│   │   ├── stop.ts               #   常駐程式關閉
│   │   ├── telegram.ts           #   Telegram 機器人
│   │   ├── discord.ts            #   Discord 機器人
│   │   └── send.ts               #   單次提示詞執行
│   └── ui/                       # Web 儀表板
│       ├── server.ts             #   HTTP 伺服器（預設 127.0.0.1:4632）
│       ├── services/             #   REST API 端點
│       └── page/                 #   單頁式前端
├── go-rewrite/                   # Go 重寫版本
│   ├── cmd/claudeclaw/main.go    #   進入點
│   └── internal/                 #   各模組對應 src/ 結構
├── prompts/                      # 系統提示詞
│   ├── BOOTSTRAP.md              #   初次設定引導
│   ├── SOUL.md                   #   人格與行為規範
│   ├── IDENTITY.md               #   可自訂的身份模板
│   └── heartbeat/HEARTBEAT.md    #   定期簽到模板
├── commands/                     # 技能指令定義（Markdown）
├── skills/                       # 自訂技能模組
├── .claude-plugin/               # 外掛套件中繼資料
└── images/                       # 圖片與截圖
```

### 執行時期狀態

所有執行時期資料存放於 `.claude/claudeclaw/`（已加入 `.gitignore`）：

| 檔案 | 說明 |
| --- | --- |
| `settings.json` | 完整設定（模型、時區、心跳、Telegram、Discord、安全層級等） |
| `state.json` | 心跳與排程任務的執行狀態 |
| `daemon.pid` | 常駐程式的 Process ID |
| `jobs/*.md` | 使用者建立的排程任務（YAML frontmatter + Markdown） |
| `logs/` | 執行日誌（JSON 格式） |
| `session.json` | Claude Code 工作階段快取 |

### 核心設計模式

- **串列佇列（Serial Queue）：** 所有提示詞依序執行，避免同一 Claude Code 工作階段發生並行衝突。
- **速率限制偵測：** 自動偵測「you've hit your limit」等訊息，觸發 GLM 模型備援。
- **熱重載（Hot Reload）：** 每 30-60 秒重新檢查設定與排程任務，無需重啟。
- **持久記憶：** 透過 `CLAUDE.md` 中的受管理區塊（HTML 註解分隔）保存跨工作階段的資訊。

### 安全機制

四種安全層級：

| 層級 | 說明 |
| --- | --- |
| `locked` | 無工具可用 |
| `strict` | 僅允許白名單中的工具 |
| `moderate`（預設） | 封鎖黑名單中的工具 |
| `unrestricted` | 所有工具皆可使用 |

執行範圍透過 `CLAUDE.md` 中的安全約束區塊進行目錄級隔離。

## 排程任務格式

排程任務以 Markdown 檔案儲存，使用 YAML 前置資料定義排程：

```markdown
---
schedule: "0 9 * * *"
recurring: true
notify: true
---

你的提示詞內容寫在這裡。
```

| 欄位 | 類型 | 說明 |
| --- | --- | --- |
| `schedule` | string | 標準 5 欄位 cron 表達式 |
| `recurring` | boolean | 是否重複執行（`false` 表示一次性任務） |
| `notify` | boolean | 執行完成後是否通知至 Telegram/Discord |

## Web 儀表板

啟動時加上 `--web` 旗標即可開啟 Web 儀表板（預設 `http://127.0.0.1:4632`）。

功能包含：
- 即時常駐程式狀態與倒數計時
- 排程任務建立、編輯、刪除
- 設定管理
- 執行日誌檢閱

### API 端點

| 方法 | 路徑 | 說明 |
| --- | --- | --- |
| GET | `/api/health` | 健康檢查 |
| GET | `/api/state` | 常駐程式狀態 |
| GET | `/api/settings` | 目前設定 |
| GET | `/api/logs` | 執行日誌 |
| POST | `/api/settings/heartbeat` | 更新心跳設定 |
| POST | `/api/jobs` | 建立排程任務 |
| DELETE | `/api/jobs/<name>` | 刪除排程任務 |

## 使用 Go 版本

Go 重寫版本位於 `go-rewrite/`，產生單一二進位檔，無需 Bun 或 Node 等執行環境。

### 編譯

```bash
cd go-rewrite
go build -o claudeclaw ./cmd/claudeclaw
```

### 獨立執行

Go 二進位檔與 TypeScript CLI 指令完全對應：

```bash
./claudeclaw start              # 啟動常駐程式（互動式設定精靈）
./claudeclaw start --web        # 附帶 Web 儀表板啟動
./claudeclaw status             # 查看常駐程式狀態
./claudeclaw send "你的指令"     # 發送單次提示詞
./claudeclaw telegram           # 啟動 Telegram 機器人
./claudeclaw discord            # 啟動 Discord 機器人
./claudeclaw --stop             # 停止常駐程式
./claudeclaw --clear            # 備份並重設工作階段
```

### 在 Claude Code 中使用（類似 `/claudeclaw:start`）

若要以 Go 二進位檔作為 Claude Code 斜線指令的後端，將啟動步驟中的 Bun 呼叫替換為 Go 二進位檔路徑即可：

1. **編譯二進位檔**，放置於專案可存取的位置（如專案根目錄或 `go-rewrite/`）：
   ```bash
   cd go-rewrite && go build -o claudeclaw ./cmd/claudeclaw
   ```

2. **從專案目錄啟動常駐程式**：
   ```bash
   mkdir -p .claude/claudeclaw/logs
   nohup ./go-rewrite/claudeclaw start --web > .claude/claudeclaw/logs/daemon.log 2>&1 &
   ```

3. **在 Claude Code 中恢復共享工作階段**：
   ```bash
   claude --resume $(cat .claude/claudeclaw/session.json | grep -o '"sessionId":"[^"]*"' | cut -d'"' -f4)
   ```

所有斜線指令（`/claudeclaw:status`、`/claudeclaw:stop` 等）運作方式不變——它們讀取相同的 `.claude/claudeclaw/` 狀態目錄。Go 二進位檔與 TypeScript 版本共用完全相同的設定與狀態格式，因此你可以隨時自由切換。

### 為什麼選擇 Go？

| | TypeScript (Bun) | Go |
| --- | --- | --- |
| 依賴 | 需要 Bun + Node | 單一約 11 MB 二進位檔 |
| 部署 | 先安裝 Bun | 複製二進位檔即可執行 |
| 效能 | V8 執行環境開銷 | 原生執行 |
| 交叉編譯 | 不適用 | `GOOS=linux GOARCH=arm64 go build` |

## 截圖

### Claude Code 資料夾狀態列
![Claude Code 資料夾狀態列](images/bar.png)

### Web 儀表板
![Web 儀表板管理介面](images/dashboard.png)

## 常見問題

<details open>
  <summary><strong>ClaudeClaw 能做到 XX 嗎？</strong></summary>
  <p>
    如果 Claude Code 能做到，ClaudeClaw 就能做到。ClaudeClaw 在此基礎上額外提供了排程任務、心跳機制，以及 Telegram/Discord 橋接功能。你也可以為 ClaudeClaw 新增自訂技能和工作流程。
  </p>
</details>

<details open>
  <summary><strong>這個專案違反 Anthropic 服務條款嗎？</strong></summary>
  <p>
    不會。ClaudeClaw 是在 Claude Code 生態系統內的本地使用方式。它直接包裝 Claude Code，不需要第三方 OAuth 或其他外部認證流程。如果你自己寫腳本做同樣的事情，效果是一樣的。
  </p>
</details>

<details open>
  <summary><strong>Anthropic 會因為你開發 ClaudeClaw 而提告嗎？</strong></summary>
  <p>
    希望不會。
  </p>
</details>

<details open>
  <summary><strong>你準備好改名了嗎？</strong></summary>
  <p>
    如果 Anthropic 介意的話，我可能會改名為 OpenClawd。還不確定。
  </p>
</details>

## 貢獻者

感謝所有讓 ClaudeClaw 變得更好的貢獻者。

<a href="https://github.com/moazbuilds/claudeclaw/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=moazbuilds/claudeclaw" />
</a>
