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

## 2. 还差的（目标 = §17 三层发现 + §18 反滥用 + 联邦/复制）

### ① 发现层 —— **最大的缺口**

§17 定义了**三层**，只做了第 1 层：

| §17 层 | 状态 | 说明 |
|---|---|---|
| **L1 签名 Agent Card + relay set** | ✅ **已做** | `internal/a2a/relayset.go`。有效性来自 **agent 自己的签名**，不来自任何目录（§17.1） |
| **L2 多个可替换的 Indexer** | ❌ **未做** | **没有 Indexer 二进制**（`cmd/` 仅 `relayfirst`/`-node`/`-verifier`/`-mcp`）。节点自带的 card 目录（`GET /agents`）是**单节点便利**，**不是**网络发现 |
| **L3 可选 EVM bootstrap anchor** | ❌ **未做** | 无 registry 合约。`RelayAnchor.sol` 锚的是**回执 Merkle root**，**不是** §17.3 的 `cardHash` |

**后果（§17 开篇的警告）：**

> "每个人可以运行节点" **不等于** "其他人自动知道如何找到它"。

今天你必须**带外知道节点 URL**。节点找不到节点，客户端也**无法从网络发现一个陌生 agent**。

### ② 联邦 / 跨 relay 路由（Phase 7）

每个 relay 的存储是**孤岛**：客户端扇出到它**已知**的 N 个节点，但**节点之间不互相路由/复制**。
无跨 operator 联邦、无 indexer 生态、无可选 Nostr adapter（`ARCHITECTURE.md` Phase 7）。

### ③ 反滥用（§18）

"完全 permissionless" 的代价是**没法靠官方审核防垃圾**。grep 全 `internal/`：
**无 rate limit、无 hashcash / adaptive PoW**。目前每个 operator 自己扛。

### ④ 可用性 / 复制（无 gossip）

一个 relay 挂了，其数据就丢了，除非有人镜像。**无节点间 gossip/复制** —— 只有**客户端侧** failover。
"网络自愈"这一层不存在。

---

## 3. 补完的最小路径（建议顺序）

| 优先 | 动作 | 解开什么 |
|---|---|---|
| **1** | **L2：最小 Indexer**（或聚合多个节点的 card 目录） | 解开"必须带外给 URL" |
| **2** | **L3：`cardHash` 上链**（§17.3，canonical bootstrap + fallback） | indexer 全挂时仍能找到 agent |
| 3 | **反滥用**（per-operator rate limit / hashcash） | 公共 relay 可开放而不被打爆 |
| 4 | **联邦 / 跨 relay 路由** | 多 operator 网络真正的互操作 |

**1 和 2 做完，"任何人都能跑节点"才真的变成"任何人都能被找到"。**

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
