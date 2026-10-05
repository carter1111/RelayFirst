# 去中心化现状与差距（目标：Nostr 式的 permissionless 网络）

> **本文回答一个问题：** "你们多去中心？" —— 用**目标定义**量，而不是用"共识链"这种不属于本项目的尺子。
>
> 目标定义在 [`ARCHITECTURE.md`](../ARCHITECTURE.md) §1.6 / §15–§18（**L4 路线图**）。
> 本文是**现状快照**，不复制那些规范。
>
> 更新：2026-10-06。**MVP 期范围生效**（见文末"范围声明"）。

---

## 0. 目标是什么，先说清楚

**RelayFirst 的目标形态 = `ARCHITECTURE.md` §1.6 的定义：**

> 一个**完全开放、permissionless、multi-relay** 的 A2A 网络，
> **不需要也不应该有**全局区块链式共识、全网统一数据库或官方中心路由（Nostr-style node model）。

**四个网络不变式（NET-1…NET-4，§15.3）：**

```text
NET-1  没有唯一官方 Relay。
NET-2  没有全局 canonical database。
NET-3  没有要求 Relay 节点彼此达成共识。
NET-4  Agent 自己选择与切换 Relay set；多 relay 冗余是默认行为。
```

> ⚠️ **不要用"共识/全序/全网状态"来评价这个项目。** §1.6 明确把那些列为**不需要**。
> 用它评价 = 拿错尺子。

---

## 1. 已经具备（NET-1…NET-4 + 不可伪造 + 客户端不盲信）

| 性质 | 状态 | 证据 |
|---|---|---|
| **NET-1** 无唯一官方 Relay | ✅ | 无中心服务；`docker run` 单二进制（`Dockerfile`，实测 build/run 成功） |
| **NET-2** 无全局 canonical DB | ✅ | 节点不读链：`internal/node/node.go` `wellKnownNote` 原文 "this node does not read any chain" |
| **NET-3** 节点无需共识 | ✅ | `internal/publish/policy.go` 的 quorum 数的是**投递确认**，注释明说 "Not consensus" |
| **NET-4** agent 自选 relay set | ✅ | `internal/a2a/relayset.go`（agent 自签的 relay set）+ 客户端 **quorum / 健康追踪 / 有界 failover**（`internal/publish/policy.go`） |
| **节点不能伪造** | ✅ | 导入图**结构上**链接不到 `eip712`/`receipt`/`publish`（CI 门禁 `go list -deps`）；`well-known` 自述 "holds no key… cannot sign anything, by design"（ADR-0004） |
| **客户端不盲信节点** | ✅ | 判据 ②⑩；`internal/assertion` 的 `Check` 把节点结论当**输入评估**，非答案 |
| **有效性来自签名** | ✅ | per-actor hash chain + 状态机；不依赖任何 relay 的投递序（ARCH §4.2） |

---

## 2. 缺口清单（按维度分，避免拿错尺子）

> **正确性维度一条都不缺**（NET-1..4 + 签名有效性）。
> 下面按【是否影响去中心化】重新分类 —— 上一版把两件 operator 事务错列成了去中心化缺口。

### 2A. 真正属于【去中心化 / 可用性】的缺口

| # | 缺口 | 状态 | 证据 | 影响 |
|---|---|---|---|---|
| **G1** | **发现层 L2：多 Indexer 生态** | ❌ 未做 | `cmd/` 仅 4 个二进制，无 indexer；节点自带 `GET /agents` 是**单节点便利** | "任何人能跑节点" ≠ "别人找得到" |
| **G2** | **relay 查询 filter**（`since`/`until`/`kinds`） | ❌ 未做 | `internal/sqlite/mailbox.go` `ByAgent` 仅支持 `limit`；`internal/node` 也只解析 limit | 客户端**无法增量轮询**，只能取最新 N 条自行去重；relay 无法高效服务时间窗查询。Nostr 的 `REQ` 有全套 filter |
| **G3** | **发现层 L3：`cardHash` EVM anchor** | ❌ 未做（**§17.3 自述"可选"**） | 无 registry 合约；`RelayAnchor.sol` 锚的是**回执 Merkle root**，非 cardHash | indexer 全挂时**无 fallback 发现** |
| **G4** | **联邦 / 跨 relay 路由** | ❌ 未做（Phase 7） | 无节点间路由 | 每个 relay 是**孤岛**，客户端只扇出到**已知**节点 |
| **G5** | **可用性复制 / anti-entropy** | ❌ 未做 | 无节点间复制 | 一个 relay 挂了数据就丢（除非有人**自愿**镜像） |

### 2B. **不是**去中心化缺口 —— 是【operator 事务】（Nostr 同样不解决）

| # | 项 | 状态 | 说明 |
|---|---|---|---|
| **O1** | **反滥用**（rate limit / hashcash / PoW） | ❌ 未做 | **Nostr 也没有**：反垃圾交给**每个 operator 自选**（付费 relay / 白名单 / 自扛）。RF 已有 `MaxPayloadBytes`；rate limit 属 operator 决策，**不是协议能力** |
| **O2** | **镜像 / 复制策略** | ❌ 未做 | **Nostr 也不 gossip**：镜像是**自愿的**，不是协议要求 |
| **O3** | **存储无上界 / 无 pruning** | ⚠️ | `ByAgent` 非破坏性（✅ 与 Nostr 一致），但**无 TTL/pruning** → 公共 relay 的磁盘会无限增长。属 operator 策略 |
| **O4** | **TLS** | ⚠️ | 节点只提供 HTTP；生产需放在反向代理后。属部署 |

### 2C. 已具备（不要误列为缺口）

| 项 | 证据 |
|---|---|
| **持久化、非破坏性拉取** | `ByAgent` 无 `DELETE`；重复拉取重复返回（同 Nostr relay） |
| **多 relay 客户端**（quorum/健康/failover） | `internal/publish/policy.go` |
| **哑节点 + 不可伪造** | 导入图隔离 + `well-known` 自述 |
| **自签身份 + 自签 relay set** | `internal/a2a/relayset.go` |

### 2D. 结论：还差什么

```text
好用（真正还差，按杠杆排序）：
   G1  L2 发现层 / Indexer        ← 主要缺口（也解释了"没有 explorer"）
   G2  relay 查询 filter          ← 上一版漏掉的
   G3  L3 cardHash anchor         ← 可选 fallback
   G4 联邦 / G5 复制              ← Phase 7
运营（operator 自决，非去中心化缺口）：
   O1 反滥用 / O2 镜像 / O3 pruning / O4 TLS
```

### 2E. 与 "Explorer" 的关系（关键）

**以太坊有 etherscan，RelayFirst 目前没有等价的统一视图。** 原因和 G1 是**同一个**：

```text
"Explorer" = L2 Indexer + 一个 UI
```

现在只有**每个节点各自的 JSON API**，且**视角是局部的**：

| 端点 | 看得见什么 | 局限 |
|---|---|---|
| `GET /agents` | **本节点**收到的 Agent Card | 单节点；无跨节点聚合 |
| `GET /observations` | **本节点**索引的回执 | 单节点；无 validity 列（故意的） |
| `GET /tasks` | **本节点**的中转任务 | 单节点 |

所以"统一节点/agent action 的 explorer"**今天不存在**，它就是 **G1（L2 Indexer）** 的产品化形态。
`ARCHITECTURE.md` 把这角色叫 "Indexer / Explorer"（§15 拓扑图、Phase 7 "Indexer 生态"），
**但没有任何实现**。

---

## 3. 补完的最小路径（建议顺序）

| 优先 | 动作 | 解开什么 |
|---|---|---|
| **1** | **G2：relay 查询 filter**（`since`/`until`/`kinds`） | 增量轮询；是 Indexer 与 Explorer 的**前置**（没有它，聚合器只能拉全量） |
| **2** | **G1：最小 Indexer**（聚合多节点 `GET /agents`，只读） | 解开"必须带外给 URL"；**也是 Explorer 的后端** |
| **3** | **G3：`cardHash` 上链**（§17.3，canonical bootstrap + fallback） | indexer 全挂时仍能找到 agent |
| 4 | **G4 联邦 / G5 复制** | 多 operator 网络真正的互操作与自愈 |

**G2 + G1 做完，"任何人都能跑节点"才真的变成"任何人都能被找到"，也才谈得上一个统一 explorer。**

---

## 4. 范围声明（重要）

**以上 ①②③④ 全部属于 `ARCHITECTURE.md` §15–§20（L4 路线图）。**

按 [`DOCS.md` §1](../DOCS.md) 的权威层级：**L4 不约束 MVP 实现**。
所以准确说法是：

> **"距离 fully permissionless network 还差发现层 + 联邦 + 反滥用 + 复制"**
> —— **不是** "MVP 没做完"，因为 MVP（`MVP.md` L0）的范围本来就不含它们。

**⚠️ 若要把"Nostr 式去中心化"提升为 MVP 的第一目标**（即把发现层纳入工期），
那是**范围变化**，必须按 `AGENTS.md` §5.1 的顺序：

```text
① 先改 MVP.md（L0）  →  ② 同步 TASKS.md  →  ③ 视情况同步 HANDOFF.md §4
```

**这是 L0 决策，需要用户拍板，不能由 agent 自行改。**
