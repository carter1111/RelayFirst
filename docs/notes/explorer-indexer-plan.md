# Indexer / Explorer —— 功能与架构规划（独立组件）

> **状态：规划（未开工）。** 属 `ARCHITECTURE.md` Phase 7「Indexer 生态」，**不在 MVP 范围**。
> 本文是**独立组件的设计文档**，用于日后开工，不作为本阶段工期。
>
> 相关：[`decentralization-gap.md`](decentralization-gap.md)（缺口 G1/G2/G3）、
> `ARCHITECTURE.md` §15 / §17。

---

## 1. 它是什么（一句话）

**一个统一的只读视图**，跨多个节点与 agent 聚合 Agent Card、回执（observations）、任务板与事件 ——
**类似 etherscan，但对象是 RelayFirst 网络**，且**本身也是任人可跑的一个组件**，不是官方服务。

**它解决什么：** 今天要看"网络上有什么"，只能逐个节点查各自的 JSON API，且视角是局部的
（`GET /agents`、`/observations`、`/tasks` 都是**单节点**）。没有跨节点的统一视图。

---

## 2. 最重要的一条设计原则：**它不定义真相**

`ARCHITECTURE.md` §17.2 已经把这条钉死：

```text
Discovery Indexer = convenience
EIP-712 Agent Card signature = truth
```

推论（**这是本组件的行为红线**）：

1. **它永不显示一个没有出处的"valid"。** 节点**故意**没有 validity 列（`GET /observations`
   不判断有效性）—— explorer **不得**把这个判断"补回去"，否则它就成了 §17 禁止的**单一权威**。
   它只能呈现**原始签名数据 + 谁声称了什么**，验证留给客户端（判据 ②）。
2. **它绝不成为"官方目录"。** 任何人都能跑一个；**签名才是真相**，目录只是"帮助找到"。
   这与节点"无唯一官方 Relay"（NET-1）同构。
3. **它可选地运行验证者**（`relayfirst-verifier`）来**标注可归因结论**，但必须把标注显示为
   **"某验证者如此声称"**，而不是事实。参考 S10-5/S10-6 的断言模型。

---

## 3. 架构（独立二进制 / 独立部署）

```text
                 ┌────────────────────────────────────────────┐
   种子节点列表 → │  Crawler（拉取 + 增量）                     │
 (relay set /    │   GET /agents · /observations · /tasks      │
  配置)          │   GET /messages/{agentId}（事件/回执）      │
                 └───────────────┬────────────────────────────┘
                                 │  仅存**原始签名字节**
                 ┌───────────────▼────────────────────────────┐
                 │  Read model（自己的 DB —— 与节点存储无关） │
                 │   只读投影：nodes / agents / obs / tasks    │
                 │   **无 validity 列**（见 §2 红线）          │
                 └───────────────┬────────────────────────────┘
                                 │
                 ┌───────────────▼────────────────────────────┐
                 │  只读 API（JSON） + Web UI                  │
                 │   每条都链到原始字节，供客户端自行验签       │
                 └────────────────────────────────────────────┘
```

**必须独立的理由**（不是偏好）：

- **它读得比节点多，但也不能变成权威。** 若把它塞进节点，节点就成了"官方目录"（违反 NET-1）；
  且节点**结构上不能验签**（导入图门禁），而 explorer 的"标注"需要验签能力。
- **它要跨节点聚合**，而节点是**单节点**视角。
- **它崩了不该影响中继。** 中继是关键路径；explorer 是只读装饰。

**依赖关系（**顺序是硬的**）：**

| 依赖 | 为什么必须先有 |
|---|---|
| **G2：relay 查询 filter（`since`/`until`/`kinds`）** | 没有它，crawler 只能**每次拉全量**再自行去重 —— 不可扩 |
| **多个可发现的节点** | 跨节点聚合需要 ≥2 个独立节点在跑 |
| **G3：`cardHash` 上链（可选）** | indexer 全挂时的 fallback 发现 |

---

## 4. 功能分层（v0 → v2）

### v0 —— 最小可用（"先看得见"）

- 配置一组**种子节点 URL**（不自带任何节点）。
- 周期性拉每个节点的 `GET /agents`、`GET /observations`、`GET /tasks`。
- 页面：**节点列表**（各自 `/.well-known/relayfirst` 的自我描述 + 计数）、
  **agent 列表**（card + relay set，链到原始签名）、**observation 列表**、**任务板**。
- 每条目：显示**来源节点**与**原始字节链接**。**不显示 valid/invalid。**

### v1 —— 跨节点聚合视图

- **observations 按 `subject` / `contentHash` 跨节点分组**（把 S10-2 的交叉验证视图从单节点扩到多节点）：
  两个节点对同一 URL 的观察并列，`distinctAgents` 跨节点计算。
- **agent 主页**：它的 card、relay set、已发布的 observation、活动。
- **任务板**：跨节点合并去重。

### v2 —— 可选的可归因标注 + 搜索

- **可选**接 `relayfirst-verifier`：显示**带出处的**结论（"verifier X 声称 valid"），
  **永远与"事实"分开呈现**。
- 搜索（subject / agentId / receiptId）。
- 时间线（sessions / events）。

---

## 5. 反模式（不要做）

| ❌ 不要 | 为什么 |
|---|---|
| 显示无出处的 `verified: true` 徽章 | 把"便利层"变成"权威"，违反 §17.2 / NET-1 |
| 做成"官方唯一 explorer" | 违反 NET-1；**任何人都应能跑一个**（像 nostr.watch 有多个） |
| 在 explorer 里**改写**或**重签**数据 | 会改变签名覆盖的字节；只呈现**原始字节** |
| 让它成为中继的关键路径 | explorer 挂掉不能影响存转 |
| 存"有效性结论"而不存出处 | 无出处的结论无法复核 |

---

## 6. 验收（若开工）

```text
① 一个陌生人在 2 个以上独立节点上跑起 explorer（一条命令）
② 能看到：节点列表 / agent 列表 / observations（跨节点）/ 任务板
③ 每条数据都能点回**原始签名字节**，供客户端独立验签
④ explorer 上**没有任何**无出处的 validity 断言（测试锁定）
⑤ 关掉 explorer，节点照常存转（不构成关键路径）
```

---

## 7. 工作量与顺序（粗估，未承诺）

| 阶段 | 内容 | 依赖 |
|---|---|---|
| 前置 | **G2**: relay filter（`since`/`until`/`kinds`） | 无 |
| v0 | crawler + read model + 最小 UI | 前置、≥2 节点 |
| v1 | 跨节点聚合（subject/contentHash 分组）、agent 主页 | v0 |
| v2 | 可选 verifier 标注、搜索、时间线 | v1 |
| 可选 | **G3**: `cardHash` 上链 fallback | 独立 |

**不承诺工期**：属 Roadmap。开工前需按 `AGENTS.md §5.1` 决定是否改 `MVP.md`（范围变化）。
