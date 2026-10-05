# 节点 TUI + 实时 Dashboard —— 交互架构方案

> **用户诉求（2026-10-06）**：① 现在**没有命令行可交互**；② 要一个**不抽象**的可视架构（参考 Pi Agent / Claude Code）；③ 节点的**实时数据 Dashboard**。
>
> **状态：规划（未开工）。** 本文是**方案**，供你选。属 `UX-1`（`TASKS.md §11.2`）。
>
> 相关：[`terminal-experience-plan.md`](terminal-experience-plan.md)（已实现的 CLI）、
> `internal/node/ws.go`（现有 hub）、`ARCHITECTURE.md §4.4`（transport 信号非协议对象）。

---

## 1. 先厘清：三种"界面"，成本与适用完全不同

| 形态 | 长什么样 | 谁用 | 成本 | 依赖 |
|---|---|---|---|---|
| **A. 命令 + TTY 彩色**（已做） | `report`/`inspect` 打字即出 | 运维、脚本 | ✅ 已交付 | 无 |
| **B. 交互 REPL / TUI**（你要的①） | 进一个"控制台"，敲命令、看面板 | 人坐在终端前 | 中 | `bubbletea` |
| **C. 实时 Dashboard**（你要的③） | 常驻面板，数据自己跳 | 人盯着一台跑着的节点 | 中 | `bubbletea` + 事件源（§4） |

**关键区分**：A 是**一次性**（跑完退出）；B/C 是**常驻交互**（`q` 退出）。

---

## 2. 你问的①："没有命令行可 input"

**确实没有** —— 节点**故意**是"服务器 + 一次性子命令"，不是 shell。

**要不要加交互控制台？** 有一条硬约束：

> **节点在 `docker -d` / systemd / CI 里没有 TTY。默认进交互模式 = 节点起不来。**

所以交互模式**只能**：
- 出现在**明确调用时**（`relayfirst-node tui`），**绝不**是默认；
- **或**在 `serve` 检测到 TTY 时**按一个键**进入（不自动进）。

**方案**：新增 `relayfirst-node tui`（子命令），**默认行为一字不改**。

---

## 3. 你要的②：可视架构（参考 Pi Agent / Claude Code）

### 3.1 参考它们的什么

| 产品 | 值得抄的 | 不要抄的 |
|---|---|---|
| **Claude Code** | 顶部状态栏 + 主输出区 + 底部输入行（**三分区**）；`/` 触发命令 | 它是**对话**，节点不是 |
| **Pi Agent** | 常驻头图 + 实时状态 + 清晰按键提示 | 其信息密度假设是单任务 |
| **k9s / lazydocker** | **Tab 切视图**、`:` 命令、`q` 退出、`?` 帮助 | 键盘绑定要有**一致语义** |

### 3.2 建议的可视架构（ASCII 线框）

```text
┌─ RELAY node · :8080 · verifies:false ──────────────────── live ●━━━━━━ 1s ─┐
│                                                                            │
│  ┌ Overview ─┬ Agents ─┬ Tasks ─┬ Stream ─┬ Config ─┐      ← Tab 切换     │
│                                                                            │
│   messages      1,234     agents        17     epoch-ahead: 0             │
│   observations    430     cards          9     uptime:  12m04s            │
│   tasks            12      inbox peers    3     stores: wal               │
│                                                                            │
│   ┌ throughput (60s) ─────────────────────────────┐                        │
│   │ ▂▃▅▇▆▅▃▂▁▂▃▅▆▇▅▃▂▁▂▃▄▅▆▅▄▃▂▁▂▃  peak 42/s     │   ← 迷你柱状        │
│   └───────────────────────────────────────────────┘                        │
│                                                                            │
├────────────────────────────────────────────────────────────────────────────┤
│  [Tab] section  [↑↓] select  [Enter] detail  [/] filter  [?] help  [q] quit │
└────────────────────────────────────────────────────────────────────────────┘
```

**五个视图（Tab）**：

| 视图 | 显示什么 | 数据 |
|---|---|---|
| **Overview** | 计数 + uptime + 吞吐迷你图 | 轮询 `/well-known` + 本地计时 |
| **Agents** | card 目录（agentId / updatedAt / relay set） | `GET /agents` |
| **Tasks** | 任务板（taskId / requester / subject / claims / expiresAt） | `GET /tasks` |
| **Stream** | **实时**到达的消息（kind / agentId / bytes / 时间） | **§4 的事件源** |
| **Config** | listen / storage / public-url / version / max-payload | 本地 |

**统一按键**：`Tab`（或 `1..5`）切视图 · `↑↓` 选行 · `Enter` 详情 · `/` 过滤 · `?` 帮助 · `q` 退出。

---

## 4. 你要的③：实时 Dashboard 的**事件源**（这是设计的核心难点）

**实测现状**：节点的 hub **只按 `agentId` 扇出**（`internal/node/ws.go`）。
`GET /ws/messages/{agentId}` 需要一个 **agentId** —— **没有"全节点活动流"**。
所以 Dashboard 的 Stream 视图**今天无源可接**。

### 三条路，各有取舍

| 方案 | 做法 | 优点 | 缺点 / 风险 |
|---|---|---|---|
| **S1 只轮询**（最省） | Dashboard 每 1s 拉 `/well-known`（计数） | **零节点改动** | 拿不到"逐条消息"；只有计数变化，**不是流** |
| **S2 新增全节点流**（推荐） | 新增 `GET /ws/events`（**无 agentId**）：hub 增加一类"全量订阅" | 真·实时 Stream；节点改动**小**（hub 多一个条件） | 节点多一个端点；需定"是否暴露所有活动"（见下） |
| **S3 本地 tail** | Dashboard 读 **stderr 日志**或 DB 轮询 | 零节点改动 | 日志是**开发工具**不是数据面；DB 轮询有锁争用 |

### 4.1 S2 的**隐私/信任问题**（必须先裁决）

节点是**公开端点**，但"全节点活动流"会**暴露所有人的 traffic 元数据**（谁在给谁发、何时、多大）。
Nostr relay **不**这样做 —— 订阅是**按 filter**（作者/kind），**不是**"看我转发的一切"。

**因此 S2 只能给"运维者"，不能给公众**：

```text
GET /ws/events  →  默认【关闭】
  开启方式：--admin-ws（或仅监听 loopback 时开）
  且必须文档写明：它暴露节点级元数据
```

**更干净的替代**：**`relayfirst-node tui` 直接 in-process 读 hub** ——
**不发** admin WS，而是 **TUI 与 serve 在同一进程**（`--tui` 与 serve 同启）时直接订阅 hub。
这样**没有新公开端点**，元数据不出进程。**推荐这条。**

### 4.2 建议的最终形状

```text
relayfirst-node --listen :8080 --storage ./node.db --dashboard
   → serve + 同进程 Dashboard（TTY 时）；非 TTY 时 --dashboard 无效（只 serve）
   → Dashboard 直接读：hub（实时）+ store（计数）
   → 不新增公开端点，不上报任何元数据
```

---

## 5. 依赖与理由（按 `MVP.md §8.0`）

| 依赖 | 类 | 用途 | 取舍 |
|---|---|---|---|
| **`charmbracelet/bubbletea`** | ② | TUI 事件循环、键盘、渲染 | **纯 Go，不违反 A3**；成熟（Claude Code 类工具常用）。**唯一新依赖。** |
| **`charmbracelet/lipgloss`** | ② | 样式/布局 | 随 bubbletea 一起，通常同用 |
| **`charmbracelet/bubbles`** | ② | 表格/列表/spinner 组件 | 可选；可只用 lipgloss 手写 |

**理由**：真 TUI 需要**事件循环 + 差量渲染 + 键盘绑定**，手写等于重造 bubbletea。**这是一处值得的依赖**。

**约束**：**只能 TTY**；**不得**成为默认路径；**不得**让节点在无 TTY 时行为变化。

---

## 6. 架构分层（避免把 UI 塞进 `internal/node`）

```text
internal/node         ← 不变（仍是哑的 HTTP + hub）。**不加任何 TUI 代码。**
cmd/relayfirst-node   ← 装配层：serve / report / status / inspect / tui
internal/term         ← 共享：IsTTY / Paint / （未来的）Live 单行刷新
internal/tui          ← 新增：bubbletea 模型 + 视图（只读 hub 与 store）
```

**为什么 `internal/tui` 独立**：① 节点导入图**仍不含 UI 依赖**（门禁不受影响）；
② TUI 崩了**不影响 serve**；③ 未来 `relayfirst`（矿工 CLI）也能复用。

**门禁影响**：`relayfirst-node` 会**新链接** `bubbletea`（纯 Go，**不含** `eip712`/`receipt`）→ 现有导入图门禁**仍过**（它只禁签名代码）。需在门禁里**如实注明**新增了 UI 依赖。

---

## 7. 任务分解（若开工）

| id | 任务 | 依赖 | 验收 |
|---|---|---|---|
| **T1** | `internal/term`（IsTTY/Paint/Live）+ 节点迁移 | — | 节点行为不变；导入图门禁仍过 |
| **T2** | `internal/tui` 骨架 + Overview 视图（轮询计数） | T1 | `relayfirst-node tui` 在 TTY 起；非 TTY 明确报错不空跑 |
| **T3** | Agents / Tasks / Config 视图（读 store） | T2 | 数据与 `inspect` 一致 |
| **T4** | **Stream 视图**：同进程订阅 hub（§4.2） | T2 | 新消息即时上屏；**不开公开端点** |
| **T5** | `--dashboard`（serve + 同进程 TUI）；非 TTY 时忽略 | T4 | `docker -d` 下**行为不变**（无 TTY 不开 TUI） |
| **T6** | 键盘/帮助/退出一致性 + 冻结回归（非 TTY 不变） | T2..5 | `serve` 输出与非 TUI 完全一致 |

---

## 8. 反模式

| ❌ 不要 | 为什么 |
|---|---|
| 默认进 TUI | `docker -d`/systemd 起不来 |
| 新增**公开**"全节点流" | 暴露所有人 traffic 元数据（Nostr 不这么做） |
| TUI 依赖进 `internal/node` | 破坏"节点最小/哑"与导入图隔离 |
| TUI 崩了影响 serve | 界面是装饰，中继是关键路径 |
| 在非 TTY 里渲染 TUI | 输出垃圾到日志 |

---

## 9. 裁决记录

| # | 问题 | 决定 | 日期 |
|---|---|---|---|
| **Q2** | 实时源 | ✅ **同进程读 hub**（`serve --dashboard`）；**不加公开端点**，元数据不出进程 | 2026-10-06 |
| **Q3** | 依赖 | ✅ **同意引入 `bubbletea`**（+`lipgloss`）；唯一新依赖，纯 Go，不破导入图隔离 | 2026-10-06 |
| **Q1** | 形态（B / C） | ⏳ **待定** —— 用户要先讨论一件重要的事 | — |
| **Q4** | 触发方式 | ⏳ 待定（倾向 `serve --dashboard` 同启） | — |

> **已定两项改变了实现方式**：Stream 视图**不发** admin WS（§4.1 的风险项被消除）；
> `internal/tui` 依赖 `bubbletea`。**开工仍待 Q1（形态）与 Q4。**
