# CLI 定位与品牌体验规划 —— `relayfirst` 到底给谁用？

> **状态：规划（未开工）。**本文只做设计。
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
