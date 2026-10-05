# CLI 定位与品牌体验规划 —— `relayfirst` 到底给谁用？

> **状态：计划（未开工）。** §1–§5 是设计与原则；**§6 是任务级实施计划**（2026-10-06：要开工，先计划）。
>
> 相关：`TASKS.md` §11.2（UX-1）、`MVP.md` §1 原则②、`§1.1`（目标用户）、判据 ①、
> `terminal-experience-plan.md`（节点侧）。

---

## 1. 问题：`relayfirst` 是给 agent 用，还是给人用？

你的问题原话："矿工 CLI relayfirst，都是 Agent 用，有用嘛？"

**答案：两者都是，而且必须都保住。** 这不是折中，是两条**不同的接口**。

| 使用者 | 用 `relayfirst` 做什么 | 需要什么 |
|---|---|---|
| **人**（初始配置） | 生成/导入身份、`config set`、首次挖矿、看分数、导出回执 | **可读输出、品牌、指引** |
| **agent / 编排** | 反复调用 `mine`、`verify`、`session …`、`status` | **稳定 JSON、退出码、可脚本化、无装饰** |

### 1.1 为什么"人"这一侧不能丢 —— 判据 ① 明文要求

`MVP.md` §11 判据 ①：

> **陌生人 10 分钟内产出第一份积分** —— 从 npx 到看到分数，**全程无人工协助**

**"陌生人"是人。** 一个"只给 agent 用"的 CLI **拿不到判据 ①**。
`MVP.md` §1.1 也明说目标用户是**雇佣兵**（要立刻知道"我拿到多少"）。

### 1.2 为什么"agent"这一侧也不能丢

MCP（`relayfirst-mcp`）走 stdio **给 agent**；但**签名永远在 CLI**（§9.2）。
所以 agent 场景下 CLI 是**被调用的工具**，必须是**可脚本化的**：
稳定 JSON、明确退出码、无 ANSI、无交互。

---

## 2. 设计原则：**协议层给 agent，UI 层给人**

**同一条命令，两种输出，由 TTY 决定** —— 与节点同一套判据（见 `terminal-experience-plan.md` §0.2）。

```text
stdout 是终端   →  人类可读（品牌/颜色/表格/logo）
stdout 不是终端 →  机器可读（JSON，字节稳定）——今天的输出
```

**硬要求（验收会测）**：

| # | 要求 |
|---|---|
| U1 | **非 TTY 输出保持今天的 JSON**，键与顺序不变（脚本/agent 不受影响） |
| U2 | **管道里 grep 不到 ANSI**（`\033`） |
| U3 | 退出码语义**不变**（现在是"发现"类结果退出 0，见 `session verify` / `anchor check`） |
| U4 | **不新增强制交互**：任何命令在无 TTY 下都必须能跑完，不得等待输入 |
| U5 | JSON 路径可**强制**：`--json` 覆盖 TTY 判定（CI 里显式要 JSON） |

> ⚠️ **U1 是承重的**：`relayfirst verify` 的输出、`session verify` 的 `summary` 等
> **已被测试与文档依赖**。改它们 = 破坏判据 ② 的可复核性。**非 TTY 一律不动。**

---

## 3. 人这一侧的体验设计（若开工）

### 3.1 `relayfirst`（无参数 / `--help`）：首屏

- 大 logo + 一句话定位 + **三步起步**（`id` → `config set` → `mine`）。
- 现状：只打 usage 文本。目标：TTY 下打 logo + 起步指引；非 TTY 打原 usage。

### 3.2 `relayfirst mine`：首启与运行

- 首启（TTY）：logo + "你正在挖矿 as `agent:…`" + 当前 epoch 预算。
- 运行（TTY）：**单行实时刷新**（points / receipts / epoch / anchors），
  **不是刷屏**（现在是每轮一行，长跑会滚屏）。
- 非 TTY：**保持今天的行为**（逐轮一行 + `--once` 的 JSON）。

### 3.3 `relayfirst status`：仪表盘

- TTY：**表格化/彩色**仪表盘（points epoch/lifetime、receipts、credited、anchors）。
- 非 TTY：**今天那份 JSON**（`agents` 数组等），字节不变。

### 3.4 依赖与理由（按 `MVP.md §8.0`）

| 候选 | 类别 | 取舍 |
|---|---|---|
| **纯标准库**（`os.ModeCharDevice` 判 TTY + 手写 ANSI/表格） | ② | ✅ **推荐起步**：零新依赖，够用 |
| `charmbracelet/bubbletea` + `lipgloss` | ② | 真 TUI（实时面板）需要它；**纯 Go，不违反 A3**，但属**新依赖**，需记录理由 |
| `charmbracelet/lipgloss`（仅样式） | ② | 比 bubbletea 轻；若只做彩色文本可只用它 |

**建议**：**v0 用零依赖**（TTY 分支 + 手写 ANSI）拿到 80% 效果；
**只有**确认需要"实时面板/进度条动画"时才引 bubbletea。

---

## 4. 反模式（不要做）

| ❌ 不要 | 为什么 |
|---|---|
| 让 TTY 判定影响**正确性** | 输出装饰不能改变 JSON、退出码或任何语义 |
| 强制交互（"按任意键继续"） | 破坏 agent、CI、管道（违反 U4） |
| 在 stdout 混入进度条/ANSI | 污染管道（违反 U2） |
| 给 `verify` / `session verify` 加"好看的总结"取代 JSON | 判据 ② 依赖那份 JSON（违反 U1） |
| 给 `relayfirst-mcp` 加任何装饰 | 它是协议流，不是终端输出 |

---

## 5. 与 MVP 范围的关系（重要）

**本节是"体验"，不是"正确性"。**

- `MVP.md` §1 原则 ②：**"叙事 > 协议完整性"** → 首屏/仪表盘**服务叙事**，**算 MVP 精神内**。
- 但它**不服务十条判据里任何一条的正确性**；它是**传播/第一印象**投资。
- **优先级**：**不得插队到 BLK-2（真实消费方）之前**。BLK-2 是上线硬前置，
  UI 是上线后的优化。

**一句话**：`relayfirst` **既给 agent 也给人** —— **协议层不能因 UI 而变**，
**UI 层不能因协议而消失**。两者用 **TTY 分支**同时满足。

---

## 6. 实施计划（2026-10-06 定：要开工，先计划）

> **状态：计划（未开工）。** 节点侧已落地（见 [`terminal-experience-plan.md`](terminal-experience-plan.md) §7），
> 本计划复用同一套判据（TTY 分支、非 TTY 冻结）。

### 6.1 前置：抽出共享层 `internal/term`（避免两份实现）

节点的 `isTTY`/`paint` 现在在 `cmd/relayfirst-node/banner.go`。**CLI 也要同一套** —— 复制会漂移。

| 项 | 内容 |
|---|---|
| 新包 | `internal/term`：`IsTTY(*os.File) bool`、`Paint(s, colour, on) string`、颜色常量、**一个最小的 `Live` 单行刷新器** |
| 依赖 | **仅标准库**（`os.ModeCharDevice` + 手写 ANSI） |
| 安全 | **零密码学** —— 节点可安全引用（导入图门禁仍过） |
| 迁移 | `cmd/relayfirst-node/banner.go` 改用 `internal/term`（**行为不变**，由现有测试守） |
| 验收 | `go list -deps ./cmd/relayfirst-node` **仍不含** `eip712`/`receipt`（门禁）；两二进制共用一份 TTY 判定 |

### 6.2 首屏：`relayfirst`（无参数）与 `--help`

| 场景 | 输出 |
|---|---|
| **TTY** | logo + 一句话定位 + **三步起步**：`id` → `config set source …` → `mine` |
| **非 TTY** | **保持今天的 usage 文本**（逐字节） |

**命令表面**：`relayfirst`（无参数）与 `--help` **同一画面**（今天就是如此：无参数 `fmt.Print(usage)`）。

### 6.3 `relayfirst status`：仪表盘

| 场景 | 输出 |
|---|---|
| **TTY** | 彩色/对齐面板：`points lifetime / epoch`、`receipts`、`credited`、`anchors`、每 agent 一行 |
| **非 TTY** | **今天那份 JSON**（`epoch`/`agents[]`/…），**键与顺序不变** |

**冻结**：`status --json`（若加）与**非 TTY** 默认都走 JSON。**`agents[]` 的结构不得改**（脚本/文档依赖）。

### 6.4 `relayfirst mine`：首启 + 单行实时刷新

| 场景 | 输出 |
|---|---|
| **首启（TTY）** | logo + `mining as <agentId>` + `epoch` + `verdicts` + `sources`（**沿用今天已有字段**，只是排版） |
| **运行（TTY）** | **单行原地刷新**：`epoch N · points epoch X · lifetime Y · tasks T · anchors A`（**不滚屏**） |
| **非 TTY** | **每轮一行**（今天的 `liveProgress.Report` 行为，**字节不变**）+ `--once` 的 JSON |

**为什么"原地刷新"是重点**：今天每轮打一行，长跑会**滚屏淹没**。原地刷新要 `\r` + 清行 ——
**标准库可做**（单行），不需 bubbletea。

**风险**：`mine` 会写**日志 + 进度**到同一流。必须：**进度只写 stderr 且仅 TTY**；`stdout` 留空或只留 `--once` 的 JSON。

### 6.5 冻结清单（**绝不改**，判据②与脚本依赖）

| 冻结项 | 为什么 |
|---|---|
| `relayfirst verify <file>` 的 JSON | 判据 ② 的可复核输出 |
| `session verify` 的 `summary` | 文档与测试依赖 |
| `anchor check` / `anchor root` 的 JSON | 离线验证路径 |
| 任何命令**非 TTY** 时的输出 | agent / CI / 管道 |
| 所有**退出码语义** | 现在是"发现类结果退出 0"（见 `session verify`/`anchor check`） |

**规则**：**装饰只加在 TTY 分支**；`--json`（在需要处）**强制 JSON**。

### 6.6 任务分解

| id | 任务 | 依赖 | 验收 |
|---|---|---|---|
| **UX1b-1** | `internal/term`（IsTTY/Paint/Live）+ 节点迁移到它 | — | 节点行为不变（现有测试）；导入图门禁仍过 |
| **UX1b-2** | `relayfirst` 首屏（TTY logo + 三步；非 TTY usage 不变） | UX1b-1 | 非 TTY 输出逐字节等于今天 |
| **UX1b-3** | `relayfirst status` TTY 仪表盘 | UX1b-1 | 非 TTY JSON 键/序不变（测试锁定） |
| **UX1b-4** | `relayfirst mine` 首启 banner | UX1b-1 | 非 TTY 启动输出不变 |
| **UX1b-5** | `relayfirst mine` 单行原地刷新（TTY） | UX1b-4 | 非 TTY 仍每轮一行；TTY 不滚屏 |
| **UX1b-6** | 冻结回归测试：对冻结清单逐个断言"非 TTY == 今天" | UX1b-2..5 | 任一冻结项被装饰污染 → FAIL |

### 6.7 依赖与"不做"

- **v0 零新依赖**（标准库，同节点）。**只有**当确认需要**多行面板/动画**时才引 `bubbletea`
  （纯 Go，不违反 A3，但属新依赖，按 `MVP.md §8.0` 记理由）。
- **不做**：交互式 REPL；给 `verify`/`session verify`/`mcp` 加装饰；`mine` 写 stdout 进度。

### 6.8 与范围的关系

**仍是体验（非正确性）**，**不得插队 BLK-2**。若排进工期，属 `UX-1` 的一部分（已在 `TASKS.md §11.2` 登记）。
