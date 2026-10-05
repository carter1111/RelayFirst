# 发现层（Discovery / Indexer / Explorer）实施计划

> **这是 G2 + G1 的 solid plan。** 目标：让"任何人都能跑节点"变成"任何人都能被找到"，
> 并为其提供统一视图（Explorer）。
>
> 缺口定义：[`decentralization-gap.md`](decentralization-gap.md)（G1/G2/G3）。
> 架构与红线：[`explorer-indexer-plan.md`](explorer-indexer-plan.md)。
>
> **⚠️ 范围**：属 `ARCHITECTURE.md` Phase 7。**要排进 MVP 工期，必须先改 `MVP.md`（`AGENTS.md §5.1`）** ——
> 本文件提供任务分解，**不代表已批准范围**。

---

## 0. 为什么顺序是 G2 先、G1 后

```text
没有 filter（G2）→ crawler 每次只能拉全量再自行去重 → 不可扩
有 filter（G2）  → crawler 只拉增量 → Indexer 才可能长期运行
```

所以 **G2 是 G1 的硬前置**。这是本计划的排序依据，不是偏好。

---

## 1. G2 —— relay 查询 filter（前置，独立可交付）

### 1.1 现状

`internal/sqlite/mailbox.go` 的 `ByAgent(agentID, limit)` **只支持 `limit`**；
`internal/node` 也只解析 `limit`。客户端**无法增量轮询**。

### 1.2 目标（对齐 Nostr `REQ` 的最小集）

| 参数 | 语义 | 注意 |
|---|---|---|
| `since` | `received_at >= since`（unix 秒） | **用 received_at（relay 观察时间）**，不是事件内 `issuedAt` —— 见 §1.3 |
| `until` | `received_at < until` | 同上 |
| `kind` | 过滤 `receipt` / `event` / `grant`（可重复） | 大小写不敏感？**否** —— 精确匹配，避免多写一个真相 |
| `limit` | 同今天 | 保留 |

### 1.3 三条设计约束（承重）

1. **只用 `received_at`，不用签名内时间。**
   `issuedAt` 在**签名内**，节点**不解析载荷**（§7.1）。要按它过滤，节点就得读懂事件 —— 越界。
   `received_at` 是 relay 自己的观察时间，节点**有权**用它。
   **代价（如实说）**：依赖客户端的钟；但这是**过滤**，不是**有效性**，可接受。
2. **不做分页游标（cursor）。** `since`+`limit` 足够增量轮询；游标是**新状态**，会引入服务端权威。
3. **不排序保证跨节点一致。** 节点只按 `received_at DESC, receipt_id`（今天如此）；**确定性排序是客户端的事**（ARCH §4.2）。

### 1.4 任务分解

| id | 任务 | 依赖 | 验收 |
|---|---|---|---|
| **G2-1** | `MessageStore.ByAgentFiltered(agentID, Filter)`：`since`/`until`/`kinds`/`limit` | — | SQL 参数化；空 filter == 今天行为（测试锁定） |
| **G2-2** | `GET /messages/{agentId}` 解析新 query 参数 | G2-1 | 非法值 400；未知参数**忽略**（前向兼容） |
| **G2-3** | 文档 `/.well-known/relayfirst` 声明支持的 filter | G2-2 | well-known 列出 `since`/`until`/`kind` |
| **G2-4** | 客户端 `session verify` / 拉取路径**用增量**（若适用） | G2-2 | 不回归；可选 |

**证据要求**：`G2-1` 必须有"空 filter 与旧路径逐条一致"的测试（否则是静默行为变化）。

---

## 2. G1 —— 最小 Indexer / Explorer

### 2.1 组件形态（独立二进制）

```text
cmd/relayfirst-indexer        ← 新二进制（独立部署，不并入节点）
  crawler  → 拉多节点（G2 filter 增量）
  store    → 自己的只读投影（独立 SQLite 或无状态内存）
  server   → 只读 JSON API + 静态 Web UI
```

**为什么不并进节点**（三条，都承重）：
1. 节点**结构上不能验签**（导入图门禁）；indexer 的"可选标注"需要验签能力 → **能力不同**
2. 节点是**单节点**视角；indexer 要**跨节点聚合** → **职责不同**
3. indexer 崩了不能影响中继 → **关键路径不同**

### 2.2 红线（`§17.2`）

```text
Discovery Indexer = convenience
EIP-712 Agent Card signature = truth
```

**行为红线**：**永不显示无出处的 `valid`**；只呈现**原始签名字节 + 谁声称了什么**。
（若接 `relayfirst-verifier`，标注必须写成"verifier X 声称 valid"，**与事实分开**。）

### 2.3 任务分解

| id | 任务 | 依赖 | 验收 |
|---|---|---|---|
| **G1-0** | **配置**：种子节点列表（env/flag 或 relay-set 发现起点） | — | 至少 2 个节点可配 |
| **G1-1** | **Crawler v0**：周期拉每个节点的 `/agents`、`/observations`、`/tasks` | G2-2 | 幂等；失败节点**不阻断**其他 |
| **G1-2** | **read model**：nodes / agents / observations / tasks 投影 | G1-1 | **无 validity 列**（测试锁定） |
| **G1-3** | **只读 JSON API**：`/nodes`、`/agents`、`/observations`、`/tasks` | G1-2 | 每条带**原始字节链接** |
| **G1-4** | **Web UI v0**：节点/agent/observation/任务列表页 | G1-3 | 每行**链回原始签名**；**无 validity 徽章** |
| **G1-5** | **跨节点分组**：observations 按 `subject`/`contentHash` 聚合 + `distinctAgents` | G1-2 | 与 S10-2 的语义一致，跨节点 |
| **G1-6** | **独立部署**：Dockerfile（indexer 自己的） | G1-3 | 一条 `docker run` |

### 2.4 反模式（`explorer-indexer-plan.md §5` 复述，避免误做）

- ❌ 无出处的 `verified: true` 徽章
- ❌ "官方唯一 explorer"（**任何人都应能跑一个**）
- ❌ 改写/重签数据（只呈现原始字节）
- ❌ 让 indexer 成为中继的关键路径

---

## 3. G3 —— `cardHash` EVM anchor（可选，Phase 3）

**§17.3 自述"可选"**。用途：indexer 全挂时的 **fallback 发现** + canonical bootstrap。

| id | 任务 | 依赖 | 验收 |
|---|---|---|---|
| **G3-1** | registry 合约：`latestAgentCardHash` / `cardURI` | — | 只存 hash/URI，**不存 card 本体** |
| **G3-2** | CLI：把 card hash 锚上去 / 读回来 | G3-1 | 读回可校验（hash 匹配） |

**为什么最后做**：它是 **fallback**，G1/G2 才是主路径；且它引入链上依赖（成本）。

---

## 4. 顺序与里程碑（若获批进入工期）

```text
M-D1   G2 全部        ← 独立可交付；对现有客户端无破坏
M-D2   G1 v0（crawler + read model + JSON API）      ← 有 API，无 UI
M-D3   G1 UI v0      ← 有 Explorer 页面
M-D4   G1 跨节点分组  ← 真正的"统一视图"
M-D5   G3（可选）
```

---

## 5. 开工前必须做的两件事（顺序不可反）

1. **改 `MVP.md`（L0）**：把发现层纳入范围（`AGENTS.md §5.1` 的严格顺序）。
2. **改 `TASKS.md`（L1）**：把下面的任务放进主表。

**在 ① 完成前，本计划的任务只在 `TASKS.md §11.1` 登记（Roadmap），不进主表。**
