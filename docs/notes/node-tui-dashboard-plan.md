# 节点 Dashboard / TUI —— 最优方案（含节点安全）

> **用户诉求（2026-10-06）**：① 要有可交互/可视的界面；② 参考 Pi Agent / Claude Code 但**不抽象**；
> ③ 节点**实时数据 Dashboard**；④ **必须考虑节点安全**；⑤ 节点只跑一个，**GUI 显示它在运作**即可，
> Human 操作界面是**另一个模块**、**另一个终端**打开。
>
> **状态：规划（未开工）。** 属 `UX-1`（`TASKS.md §11.2`）。
>
> 相关：[`terminal-experience-plan.md`](terminal-experience-plan.md)（已交付的 CLI）、
> `internal/node/ws.go`（现有 hub）、`ARCHITECTURE.md §4.4 / §17`。

---

## 0. 结论（先看这个）

```text
节点（relayfirst-node）：保持 headless、零 UI 代码、零新依赖。
Dashboard：一个【独立模块】，另一个终端打开，通过 HTTP 只读连节点。
v0：【严格只读】—— 不控制、不写入。因为节点没有鉴权机制。
```

这个形状来自三类产品的**架构对照**（§2），并且**同时把节点安全风险降到最低**（§5）。

---

## 1. 三种"界面"，成本与适用不同

| 形态 | 长什么样 | 谁用 | 成本 | 依赖进哪 |
|---|---|---|---|---|
| **A. 命令 + TTY 彩色**（**已交付**） | `report`/`inspect` 打字即出 | 运维、脚本 | ✅ | 无 |
| **B. 交互控制台** | 进一个"控制台"敲命令 | 人坐终端前 | 中 | 独立模块 |
| **C. 实时 Dashboard** | 常驻面板，数据自己跳 | 人盯着一个跑着的节点 | 中 | 独立模块 |

A 一次性；B/C 常驻交互。**B/C 都放在独立模块**（§3）。

---

## 2. 参考什么、不参考什么（不再抽象）

| 来源 | 架构 | 我们取什么 |
|---|---|---|
| **裸 Claude Code / 裸 Pi** | 交互 TUI **同进程** | ❌ **不取架构** —— 它们观察的是**本地 agent 会话**，不是服务器 |
| **Claude Code `daemon` / `pi-agent` / `pi-studio`** | **长驻进程 + 瘦客户端**（socket / WebSocket） | ✅ **取架构** —— client 是纯消费者，"**退出 UI 不影响服务**" |
| **k9s / lazydocker** | **针对服务器的运维 TUI**（连 socket/API） | ✅ **取信息架构**：资源列表 + 详情 + 事件 |
| **Pi 的 tui 渲染** | **不夺屏**：写 scrollback + 差量渲染（保留原生滚动/搜索/复制） | ✅ **取手感** —— 运维 TUI 尤其适合 |
| **对话式 UI** | 一轮轮 chat | ❌ **不取** —— 节点不是对话 |

**结论**：**架构学 `pi-agent`/k9s，手感学 Pi，信息架构学 k9s，不学对话模型。**

> 事实依据（WebSearch，2026-10-06）：`pi-agent` = singleton detached daemon + per-agent worker，
> dashboard 是**纯 client**、走 Unix socket、退出不影响 worker；`pi-studio` CLI "never runs
> daemon code in-process"；Claude Code 有 `daemon attach` 把 TUI 接到后台会话；裸 CC/Pi 的 TUI 同进程。

### 2.1 为什么是 client-server —— 这是**通用做法**，不是我们的特例

**判定标准（一句话）**：

```text
后端是否【长驻】+ 是否【别人依赖它 / 要能远程看 / 不该背 UI】？
  → 是  → client-server（另开客户端）
  → 否  → 单进程（UI 与逻辑装一起）
```

**同类先例（几乎覆盖所有长驻服务）**：

| 后端（长驻） | 界面（另开、连它） |
|---|---|
| `postgres` | `psql` / pgAdmin / DBeaver |
| `redis-server` | `redis-cli` / RedisInsight |
| `dockerd` | `docker` CLI / **`lazydocker`** |
| k8s API server | `kubectl` / **`k9s`** |
| `bitcoind` | `bitcoin-cli` / 钱包 GUI |
| `geth` | `geth attach` / 区块浏览器 |
| **`tmux` server** | `tmux` client（**自身即分裂设计**） |
| **VS Code Remote** | 本地 UI ↔ 远端 server |
| **任何 Web App** | 浏览器 ↔ 后端 |

**节点命中"是"的**：长驻 ✅ · 别人依赖 ✅ · 可能远端/容器 ✅ · 不该背 UI ✅ · GUI 要能另开/随关 ✅。

**反例（该用单进程）**：一次性命令（`git`/`ls`）；纯本地单用户界面程序；
**裸 CC/裸 Pi** —— 它们的"后端"就是**当前本地会话**，会话结束进程即消失（**没有独立于 UI 继续跑的服务**）。

**结论**：`relayfirst-node`（主进程）+ `relayfirst-dashboard`（另开客户端）**与
`postgres↔psql`、`dockerd↔lazydocker`、`k8s↔k9s` 同一模式**。**唯一要定的**只是 TUI 装哪个二进制
（本文建议**独立**，让节点保持"小而哑"，与上述先例一致）。

---

## 3. 最优形状：独立模块 + HTTP 只读

```text
┌──────────────────┐   HTTP (read-only)   ┌──────────────────────────┐
│ relayfirst-node  │ ◄─────────────────── │ relayfirst-dashboard     │
│  (headless)      │   /healthz           │  (bubbletea TUI)         │
│  无 UI 代码      │   /.well-known       │  另开一个终端            │
│  零新依赖        │   /agents /tasks     │  可连本地或远程节点       │
└──────────────────┘   /observations      └──────────────────────────┘
       ▲                                              │
       │  节点崩了/GUI 崩了，互不影响（客户端-服务器）  │
```

- **节点零改动**：不加端点、不加依赖、不加 UI 代码。
- **Dashboard 是 client**：连**已经跑着的**节点；可连远程；退出不影响节点。
- **二进制**：`relayfirst-dashboard`（**独立**）。理由：节点运维工具**既不属于节点也不属于矿工 CLI**；
  也让 `bubbletea` **进不了** `relayfirst`（矿工 CLI 保持零 UI 依赖）。

### 3.1 实时数据从哪来（**已定：HTTP 轮询**）

**实测**：节点的 hub **只按 `agentId` 扇出**，**没有全节点活动流** —— 独立进程也**读不到** hub。

| 源 | 能拿到 | 结论 |
|---|---|---|
| **HTTP 轮询** `/well-known` + `/agents` + `/tasks` | 计数、吞吐（**计数差分**）、uptime、列表 | ✅ **v0 用它，节点零改动** |
| 新增**公开**全节点流端点 | 逐条消息 | ❌ **不做** —— 暴露所有人流量元数据（Nostr 也不这么做） |
| 本地 tail 日志 / 轮询 DB | — | ❌ 日志是开发工具；DB 轮询有锁争用 |

**"实时"在 v0 的含义** = **计数差分出的吞吐 + uptime**，**不是逐条 firehose**。
用户原话"**GUI 就显示他在运作就可以了**" —— **轮询充分**。

**逐条流**是另一个功能（调试），**不在 v0**；若日后要做，**只对 operator**、单独裁决。

---

## 4. 视图与布局（具体线框）

```text
┌─ RELAY · :8080 · verifies:false ─────────────── live ●━━ 1s · health ok ─┐
│  ┌ Overview ┬ Agents ┬ Tasks ┬ Config ┐          ← Tab / 1..4 切换       │
│   messages      1,234      agents       17       uptime  12m04s          │
│   observations    430      agent cards   9       node    0.1.0-s5        │
│   tasks            12      publicUrl    http://…                          │
│   ┌ throughput (60s, derived from counts) ──────────┐                     │
│   │ ▂▃▅▇▆▅▃▂▁▂▃▅▆▇▅▃▂▁▂▃▄▅▆▅▄▃▂▁▂▃  peak 42/s       │                    │
│   └──────────────────────────────────────────────────┘                    │
├───────────────────────────────────────────────────────────────────────────┤
│ [Tab] section  [↑↓] select  [/] filter  [r] refresh  [?] help  [q] quit   │
└───────────────────────────────────────────────────────────────────────────┘
```

| 视图 | 内容 | 源 |
|---|---|---|
| **Overview** | 计数 + uptime + 吞吐迷你图 | `/well-known`（轮询） |
| **Agents** | card 目录（agentId / updatedAt / relay set） | `/agents` |
| **Tasks** | 任务板（taskId / requester / subject / claims / expiresAt） | `/tasks` |
| **Config** | 连的 URL / 轮询间隔 / 节点 version / 自述 note | 本地 + `/well-known` |

> **没有 Stream 视图**（v0）—— 无源可接（§3.1）。避免画一个假的流。

---

## 5. 节点安全（本方案的重点约束）

> 原则：**Dashboard 不得扩大节点的攻击面，也不得让节点持有它本不该有的能力。**

| # | 安全约束 | 为什么 |
|---|---|---|
| **S1** | **v0 严格只读** —— 不控制、不写入、不新端点 | 节点**没有鉴权机制**（无 session/cookie，设计如此：NET-1）。任何"控制"都需一套鉴权，**那是新产品面 + 新风险**。**不做。** |
| **S2** | **Dashboard 不持密钥、不能签名** | 与节点/MCP 同一性质：**导入图结构上链接不到** `eip712`/`receipt`。它是**只读显示器**。 |
| **S3** | **不加公开端点** | 读的都是**本来就公开**的端点（任何人可读）→ **不新增暴露**。全节点流**否决**（会暴露 traffic 元数据）。 |
| **S4** | **轮询负载有界** | 固定间隔（默认 1–2s）+ **出错指数退避** + **单一 poller**（不是每视图一个）。否则 N 个 dashboard = 对节点的软 DoS。**最终防线是节点的 O1 反滥用（未做）** —— 在 S4 里注明这个依赖。 |
| **S5** | **Dashboard 崩/退出不影响节点** | 客户端-服务器天然如此；这正是选独立模块的理由之一。 |
| **S6** | **远程时用 HTTPS，不禁用证书校验** | 节点本身是 HTTP（需反代）；Dashboard 必须支持 `https://` 且**默认校验**。 |
| **S7** | **不信任节点 JSON**（只展示） | 不 `eval`、不把节点返回当指令；**限制响应体大小**（防恶意/损坏节点喂超大响应）。 |
| **S8** | **UI 依赖不进行节点** | `bubbletea` 只在 `relayfirst-dashboard`。**节点的导入图隔离（不得含签名代码）继续由现有门禁证明**，且**多了一层**：UI 依赖也不进节点。 |

**一句话**：**Dashboard 是一个"只读、无密钥、不新增端点、有界轮询"的客户端** —— 它让节点**更可见，而不是更脆弱**。

---

## 6. 分层与依赖

```text
internal/node             ← 不变（哑 HTTP + hub）。不加 UI/不加端点。
cmd/relayfirst-node       ← 不变（serve/report/status/inspect）。
cmd/relayfirst-dashboard  ← 新增：TUI client（bubbletea），连 HTTP 只读。
internal/term             ← 共享：IsTTY / Paint（节点已用到；dashboard 复用）
internal/noderead         ← 新增：**只读节点客户端**（拉 well-known/agents/tasks、退避、限大小）
internal/tui              ← 新增：bubbletea 模型 + 视图（只依赖 noderead）
```

**要点**：`noderead` 与 `tui` **分离** —— **取数**与**渲染**分开，便于测试（取数可单测，无需终端）。

| 依赖 | 类 | 位置 | 理由 |
|---|---|---|---|
| `charmbracelet/bubbletea` + `lipgloss` | ② | **仅 dashboard** | 真 TUI 需事件循环+差量渲染；手写=重造。纯 Go，A3 不受影响。 |

---

## 7. 任务分解（若开工）

| id | 任务 | 依赖 | 验收 |
|---|---|---|---|
| **D1** | `internal/noderead`：只读客户端（well-known/agents/tasks、**退避**、**限响应大小**、`https` 校验） | — | 单元测试：正常/超时/4xx/超大响应；**不新增端点** |
| **D2** | `internal/term` 抽共享（节点已用；dashboard 复用） | — | 节点行为不变 |
| **D3** | `cmd/relayfirst-dashboard` 骨架 + Overview（轮询计数 + 吞吐） | D1,D2 | 连**已跑**节点；`q` 退出**不影响**节点 |
| **D4** | Agents / Tasks / Config 视图 | D3 | 数据与 `inspect` 一致 |
| **D5** | 键位/帮助/退出一致；**非 TTY 明确报错**（不空跑） | D3 | 无 TTY 时打印用法并退出，不渲染垃圾 |
| **D6** | 安全回归：导入图断言（dashboard 无签名代码）；轮询退避测试 | D1 | `go list -deps` 无 `eip712`/`receipt`；退避测试通过 |

---

## 8. 反模式

| ❌ 不要 | 为什么 |
|---|---|
| UI/依赖进 `relayfirst-node` | 破坏"节点最小/哑"与隔离；已否决 |
| 默认进 TUI | `docker -d`/systemd/CI 无 TTY，会起不来 |
| 新增公开全节点流 | 暴露所有人 traffic 元数据（S3） |
| v0 加"控制/写入" | 节点无鉴权；引入即需新鉴权面（S1） |
| 无退避的高频轮询 | 软 DoS 自己的节点（S4） |
| 远程连接禁用证书校验 | MITM（S6） |
| 画一个"Stream"视图但无真实源 | 假流比没有更糟 |

---

## 9. 裁决记录

| # | 问题 | 决定 | 日期 |
|---|---|---|---|
| **Q2** | 实时源 | ⚠️ **被取代**：原"同进程读 hub" **作废**（独立进程读不到）。现为 **HTTP 轮询 + 计数差分**，节点**零改动** | 2026-10-06 |
| **Q3** | 依赖 | ✅ 引入 `bubbletea`（**仅 dashboard**） | 2026-10-06 |
| **Q5** | 架构 | ✅ **独立模块**（`relayfirst-dashboard`）+ **只读** —— 对齐 `pi-agent`/k9s；**节点零 UI 依赖** | 2026-10-06 |
| **Q1** | 形态（B / C） | ✅ **B + C 都做**；**先 C（只读 Dashboard）**，再 B（控制台） | 2026-10-06 |
| **Q4** | 二进制 | ✅ **独立 `relayfirst-dashboard`**（节点**零 UI 依赖**，同 `dockerd` 不带 `lazydocker`） | 2026-10-06 |
| **Q6** | 发布 | ✅ **要能 `npm install`** → 纳入打包（`package.json` 加 `bin` + `build-npm-binaries.sh` 加它）；**`npm publish` 动作记下后做** | 2026-10-06 |

**全部已定，可开工。** 开工顺序：**C（只读）→ B（控制台）**。
（控制/写入仍**不做** —— 节点无鉴权，属独立大决策，见 §5 S1。）
