# RelayFirst — 架构文档（Canonical Architecture, v1.1.1）

> **RelayFirst = Ethereum-native identity + Headless A2A coordination + Permissionless multi-relay network + Optional onchain finality**
>
> 本文档是 RelayFirst 的唯一权威架构描述。它反映的是 **v1.1.1 修正后的设计**，并显式记录了 v0 → v1（见 §14）、v1 → v1.1（见 §14.2）、v1.1 → v1.1.1（见 §14.3）的每一处修正，以便设计推理不丢失。
>
> **v1.1.1 的关键变化：** (a) 修正 §12.1 中"`ecrecover` 仅 authority 使用"的内部矛盾 —— `ecrecover` 是**纯本地计算**，**热路径必需**，只有 `ethclient`/`eth_call` 属于 authority 专属；(b) 技术选型与 `MVP.md` §8 同步，明确**三分法**（密码学用库 / 编码优先用库 / 业务结构自研）、**无 CGO 约束**、**KAT 向量为硬性判据**；(c) 软化 LGPL 表述（从"避免传染"降为次要考虑）。
>
> **v1.1 的关键变化：** RelayFirst 被重新定位为一个 **permissionless、multi-relay 的 A2A 网络**（Nostr-style node model），而非"由官方运行一组 edge relay 的托管服务"。任何人都可以运行 RelayFirst 节点；任何 Agent 都可以持有自己的身份、发布自己的 Relay Set，并通过多个独立节点被发现、通信与迁移，**无需依赖 RelayFirst 官方服务器**。新增 §15–§20 描述该网络模型，并就地调和了 v1 中的托管式表述。
>
> 语言约定：所有协议标识符、类型名、Solidity struct、JSON key、Go package path 与代码块保持英文原样；散文、标题与表格使用中文。
>
> 状态：L4 长期路线图。**本文件是规范来源（specification of record），先于代码存在。**
> 实现现状：**S1–S8 已实现**（见 `TASKS.md`）；本文件**不约束 MVP 实现**（见 `DOCS.md` §1）。仓库 `/Users/cartermacbook/Desktop/Dev/RelayFirst` 非 git repo。

---

## 目录

1. [概述与定位](#1-概述与定位)
2. [架构总览](#2-架构总览)
3. [身份与权限层](#3-身份与权限层)
4. [Headless A2A 协议层](#4-headless-a2a-协议层)
5. [实时 Relay Fabric](#5-实时-relay-fabric)
6. [加密与隐私](#6-加密与隐私)
7. [Human-in-the-Loop](#7-human-in-the-loop)
8. [可选 EVM 经济最终性](#8-可选-evm-经济最终性)
9. [安全模型](#9-安全模型)
10. [端到端工作流](#10-端到端工作流)
11. [部署演进](#11-部署演进)
12. [技术选型](#12-技术选型)
13. [最终技术描述](#13-最终技术描述)
14. [v0 → v1 修正记录](#14-v0--v1-修正记录)
15. [Permissionless 网络模型](#15-permissionless-网络模型)
16. [RelayFirst Node（RFN）规格](#16-relayfirst-noderfn规格)
17. [节点发现](#17-节点发现)
18. [节点经济与反滥用](#18-节点经济与反滥用)
19. [标准族 RFN-01…RFN-12](#19-标准族-rfn-01rfn-12)
20. [完全去中心化的真实成本](#20-完全去中心化的真实成本)

---

## 1. 概述与定位

### 1.1 一句话定位

RelayFirst 是一层**去中心化、headless 的 A2A（agent-to-agent）协调织物**：它让成千上万个自治 agent 能够安全地相互发现、开启会话、协作长任务、交换加密信息、产出可验证回执（receipt），并且**只在需要时才**接入 EVM 授权与结算。

它的核心价值**不是支付、挖矿或区块链**。核心价值是：**让大量自治 agent 在无需互信的前提下，以每秒数千次事件的速率进行协调，并留下可审计、可验证、可选的链上最终性。**

工作口号：

> **Relay immediately. Verify continuously. Settle only when value is involved.**
> （立即中继。持续验证。仅当涉及价值时结算。）

**网络模型口号（v1.1）：**

> **Anyone can run a RelayFirst node. Any Agent can choose, self-host, or publish through multiple nodes. Event validity comes from cryptographic signatures, not from RelayFirst's servers.**
> （任何人都可以运行 RelayFirst 节点；任何 Agent 都可以自行选择、自托管或同时使用多个节点。事件是否有效取决于密码学签名与协议规则，而不是 RelayFirst 官方服务器。）

### 1.2 与 Google A2A 1.0 的关系：不是竞品，是权威 + 回执 + 结算层

这是定位中最重要的一点，必须置于最前：

- **Google 的 A2A（Agent2Agent）协议现已是 Linux Foundation 标准，达到 v1.0。** TSC 成员包括 AWS、Cisco、Google、IBM、Microsoft、Salesforce、SAP、ServiceNow；150+ 组织参与；已有生产部署。
- **A2A v1.0 已经内置了：**
  - **Signed Agent Cards**（基于密码学的 agent 身份验证）
  - **AP2（Agent Payments Protocol）**，60+ 组织参与，并通过 AP2 mandates extension 与 UCP 兼容
  - IBM 的 **ACP 已于 2025 年 8 月合并进 A2A**

因此：

> **RelayFirst 绝不能把自己定位成一个竞争性的 wire protocol。**
> **Wire = A2A 1.0。Authority + Receipts + Settlement = RelayFirst。Identity anchor + optional finality = EVM。**

### 1.3 真正的缺口（the wedge）

A2A 1.0 解决了"发现与消息"，但**没有**解决以下三件事——这正是 RelayFirst 的楔子：

| 缺口 | A2A 1.0 现状 | RelayFirst 提供 |
|---|---|---|
| **Scoped / revocable / budgeted delegation** | 有 signed Agent Card，但**没有**细粒度、可撤销、带预算的委托授权 | EIP-712 Session Delegation：作用域 + 额度 + 单调 nonce 撤销 |
| **Verifiable execution receipts** | 有消息，但**没有**可独立验证的执行回执 | 签名回执集 + Merkle 承诺 + 可选链上锚定 |
| **Batched settlement** | 有 AP2 支付授权，但**没有**批量化、抗双花的结算 | 基于 signed receipt-set 的 Merkle 批量结算 + 全局 bitmap 抗双领 |

**一句话：A2A 定义了 agent 如何说话；RelayFirst 定义了 agent 如何被授权、如何被追责、以及如何被结算。**

### 1.4 核心原则（源自原始 spec，全部保留）

1. **每秒数千次 A2A 事件走 offchain relay。**
2. **身份、权限、可审计性由签名与委托承担。**
3. **支付、托管、争议、收益主张，只有在需要时才进入 EVM L2。**
4. **"Relay immediately. Verify continuously. Settle only when value is involved."**

### 1.5 明确不做什么（non-goals）

- 不发明新的 wire protocol（使用 A2A 1.0）。
- 不做通用 L1/L2，不做共识，不做矿工。
- 不把 x402 当作 transport（x402 是 **Payment Adapter**）。
- 不在第一句话里谈支付（见 §14 修正 #9）。
- 不做"伪 trustless"的自动仲裁（主观争议交给人/验证者/治理）。
- **不做"官方控制网络"的托管服务。** 官方节点只能是网络中的一个 operator，而不是网络的控制点（见 §15）。

### 1.6 网络模型：完全 permissionless（v1.1 核心）

**可以做到，而且架构上应当从 Day 1 就按"像 Nostr 一样，任何人都能跑节点"的协议来设计。** 但要准确说：RelayFirst 是一个**完全开放、permissionless、multi-relay** 的 A2A 网络；它**不需要也不应该有**全局区块链式共识、全网统一数据库或官方中心路由。

四个网络不变式（与 §2 的分层不变式并列，编号前缀 `NET-`）：

```
NET-1  没有唯一官方 Relay。
NET-2  没有全局 canonical database。
NET-3  没有要求 Relay 节点彼此达成共识。
NET-4  Agent 自己选择与切换 Relay set；多 relay 冗余是默认行为。
```

这与"官方运行一堆服务器"的 Hosted SaaS 模式不同。完整的节点类型、节点规格、发现机制、经济模型与去中心化成本见 §15–§20。

---

## 2. 架构总览

### 2.1 分层组件视图

自顶向下，每一层只依赖其下一层；上层可替换且**不持有协议真相**。

```
┌──────────────────────────────────────────────────────────────────────────────┐
│  ① Experience / Renderer 层   （可替换，零协议真相）                            │
│     Web App · Telegram · Slack · CLI · AgentLand · Enterprise 系统            │
│     它们只是渲染同一个 approval object / task view；不定义协议语义              │
├──────────────────────────────────────────────────────────────────────────────┤
│  ② SDK / Integration 层                                                       │
│     TS / Go / Python SDK · A2A adapter · MCP bridge · Webhook adapter         │
│     · x402 adapter（仅作为 Payment Adapter）                                   │
├──────────────────────────────────────────────────────────────────────────────┤
│  ③ Headless A2A 协议层（协议真相所在）                                          │
│     Agent Card · Session · Task · RelayEnvelope · Receipt · State Machine     │
│     与 A2A 1.0 互操作：Agent Card / Message / Task 语义对齐                     │
├──────────────────────────────────────────────────────────────────────────────┤
│  ④ Realtime Relay Fabric（permissionless · multi-relay · 任何人可运行）        │
│     独立 RelayFirst Node（RFN）：personal / community / enterprise / archive   │
│     可选后端：NATS JetStream · Postgres · Redis · S3                          │
│     = 投递（delivery）。不可信（untrusted）。没有任何唯一官方 relay。           │
├──────────────────────────────────────────────────────────────────────────────┤
│  ⑤ Identity & Authority 层                                                    │
│     EIP-712 Session Delegation · nonce/revocation · verifier registry          │
│     · authority workers（唯一允许 ethclient / 链读的地方）                       │
│     = 权威（authority）。可对链或客户端持有的签名 checkpoint 验证。             │
│     注意：ecrecover 是本地密码学计算，属 edge 热路径，不在此列（见 §12.2）。     │
├──────────────────────────────────────────────────────────────────────────────┤
│  ⑥ Optional EVM Economic Finality 层                                          │
│     DelegationRegistry · SettlementManager · DisputeManager · ClaimBitmap      │
│     仅当涉及价值时进入。                                                       │
└──────────────────────────────────────────────────────────────────────────────┘
```

### 2.2 什么走 offchain，什么走 onchain（核心规则）

> **默认规则：一切走 offchain relay；只有当"价值 + 争议可能性 + 需要第三方强制"三者同时存在时，才把对应的*授权对象*或*结算对象*上链。**

| 对象 | offchain / onchain | 理由 |
|---|---|---|
| Session / Task / 事件流 | **offchain** | 每秒数千次；链上不可行 |
| Agent Card（含签名） | **offchain**（可锚定 hash） | 与 A2A 1.0 signed card 对齐 |
| 单个 RelayEnvelope | **offchain** | 热路径，ecrecover-only |
| Session Delegation grant | **onchain**（可选锚定） | 授权边界需要第三方可验证 |
| Receipt（执行回执） | **offchain 生成**，Merkle root 可选锚定 | 高频 |
| Settlement batch | **onchain** | 涉及真实价值转移 |
| Dispute | **onchain（仅当价值争议）** | 需要强制力 |

**关键不变式（INV-1）：** 签名一个 offchain 事件**不得**要求该事件所在链的知识。详见 §3.6 与 §14 修正 #1。

---

## 3. 身份与权限层

### 3.1 EVM 身份是规范身份

**拒绝 Nostr/npub/BIP-340 作为身份模型。** EVM 账户是唯一规范身份。

规范格式：

```
agent:eip155:<chainId>:<accountAddress>
```

示例：

```
agent:eip155:8453:0x9f4c...   // Base
agent:eip155:42161:0x1a2b...  // Arbitrum
agent:eip155:1:0xdead...      // Ethereum mainnet
```

**为什么拒绝 Nostr：**

- Nostr 的 key 与资产权威、智能账户、EIP-1271、ERC-4337、Safe 等 EVM 生态无法自然衔接。
- Nostr 上 NIP-26 delegated event signing（即 Nostr 版的 Session Delegation）**已被官方标记为 'unrecommended'**——这恰好是我们需要的原语，而它在 Nostr 侧被废弃了。
- Nostr 最多是 **Phase 3+ 的一个 ADAPTER**，绝不是 substrate。

### 3.2 账户兼容矩阵

原则：**EVM 身份是默认最优路径，但 relay/task 层不得排除非链上的企业 agent。**

| 账户类型 | 身份承载 | 热路径签名方案 | 说明 |
|---|---|---|---|
| **EOA** | `eip155:<chainId>:<addr>` | `ecrecover` | 最简，默认 |
| **Session Key EOA** | 由 delegation 派生的临时 EOA | `ecrecover` | 热路径主力（见 §3.5） |
| **ERC-4337 Smart Account** | 合约账户地址 | 授权时 ERC-1271；热路径由 Session EOA 签 | 见 §14 修正 #2 |
| **ERC-6551 TBA** | token-bound account 地址 | 同 ERC-4337 | agent NFT 的自然账户 |
| **Safe / DAO** | Safe 合约地址 | 授权时 ERC-1271 / 多签；热路径 Session EOA | 组织级 agent |
| **Enterprise OIDC** | `agent:oidc:<issuer>:<sub>`（非 EVM，但合法） | JWT（JWKS 验证） | 企业系统；见 §14 修正 #1 |

> **注意（AgentLand 集成点）：** AgentLand 的 `agent_defs` 表已有 `on_chain_token_id text`（明确注释"预留 Web3 agent NFT"），并有 `tenant_id`、`world_id`、`owner_user_id`。RelayFirst 的 `agent:eip155:<chainId>:<address>` 与 AgentLand 的 agent uuid 之间通过 wallet binding 建立映射（见 §10）。

### 3.3 密钥层级

```
Owner / Smart Account                ← 资产权威的最终来源（EOA 或合约账户）
        │  （EIP-712 签名，一次）
        ▼
EIP-712 Session Delegation           ← 授权对象：作用域 + 额度 + 有效期 + nonce
        │  （派生出）
        ▼
Session Signing Key (Session EOA)    ← 热路径签名密钥；只有"授权"权限，无资产提取权
        │
        │  （独立生成，与签名密钥无关）
        ▼
X25519 Encryption Key                ← 仅用于加密；对资产零权威（zero asset authority）
```

**核心分离：**

- Session Signing Key 只能**行使**被委托的权限，不能**扩大**权限。
- Encryption Key **与签名密钥无关**，即使泄露也无法签署事件或动用资产。
- Owner 可随时通过递增 nonce 撤销整个 delegation（见 §3.7）。

### 3.4 Session Delegation（Solidity struct，链锚定）

Delegation grant 是**唯一**在签名时携带链绑定 domain 的对象之一（另一个是 settlement）。

```solidity
/// @notice 一次性授予、可撤销、带作用域与额度的会话委托。
/// @dev    此对象是 onchain-anchored：其 EIP-712 domain 含 chainId 与 verifyingContract。
struct SessionDelegation {
    bytes32 delegationId;      // keccak256(owner, sessionKey, policyHash, salt)
    address owner;             // EOA / Smart Account / Safe
    address sessionKey;        // 派生的 Session EOA（热路径签名者）
    address policyRegistry;    // 存放 nonce / 撤销状态的合约
    bytes32 policyHash;        // keccak256(canonical policy JSON) —— 作用域定义
    uint64  validFrom;         // 生效时间戳
    uint64  validUntil;        // 失效时间戳
    uint256 maxValuePerAction; // 单次动作价值上限
    uint256 maxValueTotal;     // 委托生命周期内总价值上限
    uint256 nonce;             // 单调 nonce；撤销 = 递增（见 §14 修正 #3）
    bytes32 salt;              // 防重放 / 唯一化
}
```

**policy JSON（`policyHash` 的原像，canonical JSON）：**

```json
{
  "policyId": "pol_01H...",
  "scopes": {
    "eventTypes": ["TASK_*", "SESSION_OPEN", "RECEIPT_ISSUED"],
    "topics": ["rf.v1.acme.*"],
    "maxTaskDepth": 3,
    "allowedPeers": ["agent:eip155:8453:0x..."]
  },
  "limits": {
    "maxValuePerAction": "10000000",
    "maxValueTotal": "100000000",
    "maxActionsPerMinute": 600
  },
  "validUntil": 1760000000
}
```

### 3.5 签名方案判别符（signature-scheme discriminator）

**这是 v1 的关键修正。** 每个 envelope 显式携带一个判别符，而不是假设所有签名都是 EOA。

```solidity
/// @dev 在 RelayEnvelope 内联。
enum SignatureScheme {
    EOA,          // 0: ecrecover == agentId address（热路径，纯本地）
    ERC1271,      // 1: 合约账户；仅 authority worker 验证，绝不在 edge
    OIDC,         // 2: JWT；verifier = issuer，JWKS 缓存验证
    CUSTOM        // 3: verifier = 具名自定义验证器 id
}
```

- **热路径只允许 `EOA`。** 见 §14 修正 #2。
- `ERC1271` 事件在 edge 上**不做** `eth_call`；它们被标记并路由到 authority worker 异步验证（out-of-band）。
- `OIDC` 的 `verifier` 字段承载 issuer；JWT 用缓存 JWKS 验证。
- `CUSTOM` 的 `verifier` 是注册表中的具名验证器 id。

### 3.6 修正后的 EIP-712 domain 设计（offchain 事件）

**v0 错误：** 所有 offchain 事件的 domain 都钉死 `chainId: 8453` 与 `verifyingContract: 0xRelayFirstAuthorityRegistry`。后果：

1. 企业 OIDC/JWT 账户行**无法表示**（它没有链）。
2. "可选链上最终性"在签名时刻变成**强制**（你必须知道链）。
3. Arbitrum 上的 agent **无法**产出 Base envelope。

**v1 修正：offchain 事件的 domain 不含 `chainId` / `verifyingContract`。**

```solidity
/// @notice offchain 事件的 EIP-712 domain —— 链无关。
struct EIP712Domain {
    string  name;      // "RelayFirst"
    string  version;   // "1"
    bytes32 salt;      // 协议级常量 salt
    // 注意：没有 chainId，没有 verifyingContract。
}
```

**跨链重放如何防护？** 由身份与 nonce 承担，而非 domain：

- `agentId` 内嵌 `<chainId>`，因此 Base 的签名在 Arbitrum 上天然不匹配身份。
- `nonce` 是 per `(agent, policy)` 单调量，重放会被拒绝。
- `delegationId` 本身是链锚定的（其 grant 有链绑定 domain）。

**只有链锚定对象携带链绑定 domain：**

```solidity
/// @notice 仅用于 SessionDelegation 与 Settlement 等 onchain-anchored 对象。
struct ChainBoundEIP712Domain {
    string  name;
    string  version;
    uint256 chainId;             // 例：8453 / 42161 / 1
    address verifyingContract;   // 例：DelegationRegistry / SettlementManager
}
```

### 3.7 统一 nonce 与撤销（移除 `revocationNonce`）

**v0 错误：** 同时存在 `nonce` 与 `revocationNonce` —— 第二个真相源，产生 TOCTOU 窗口（relay 读到 `revocationNonce=5`，撤销落地，relay 却用陈旧状态验证支付）。

**v1 修正：每个 `(agent, policy)` 只有一个单调 nonce，撤销 = 递增。**

```
state: nonce[(agentId, policyId)] : uint256   // 单调递增

grant 时:  nonce = 0
每笔热路径事件: envelope.nonce 必须 > lastSeenNonce(agent, policy)
撤销时:    nonce += 1  → 之后所有 nonce <= 旧值的事件失效
```

**诚实的后果（必须写下来）：撤销对已缓存的 relay 不是瞬时的。**

- relay 缓存 `(agent, policy) -> lastSeenNonce`，TTL = `cacheTtl`（默认 5s，可配）。
- 撤销落地后，最多在 `cacheTtl` 窗口内，旧 nonce 的事件仍可能被某个 edge 接受。
- **爆炸半径 = `maxValuePerAction` × cacheTtl 窗口内可接受的动作数。**

**这个"有界窗口"论证就是安全论证本身：** 我们不宣称瞬时撤销，我们宣称**撤销的损失上界是已知且可配置的**。要缩小窗口就缩短 TTL（以 relay 负载为代价），要缩小损失就降低 `maxValuePerAction`。

### 3.8 验证顺序（热路径）

每个 envelope 在 edge relay 上按此顺序验证。**任何一步失败 → 拒绝，不落库。**

```
1. 解析 envelope，校验 protocolVersion == "a2a-relay/1.0"
2. 读取 scheme 判别符
3. 校验 issuedAt / expiresAt 时间窗
4. 校验 tenant / session / task 作用域是否落在 delegation policy 内
5. 校验 nonce > lastSeenNonce(agent, policy)   ← 单调，防重放
6. 按 scheme 验证签名：
     EOA      → ecrecover(sig) == agentId.address        （纯本地，热路径）
     ERC1271  → 标记并路由 authority worker（异步，非热路径）
     OIDC     → JWT 对缓存 JWKS 验证（verifier = issuer）
     CUSTOM   → 调用具名 verifier
7. 若 envelope 携带价值：原子 check-and-increment（Redis Lua，键 = (agent, policy)）
8. 通过 → 分配 relaySequence，持久化，扇出
```

**不变式（INV-2，edge 无链读）：** edge relay 内**不得**出现 `ethclient`、不得有任何 `eth_call` / RPC 调用。edge 是纯本地计算 + 消息投递。见 §12。

---

## 4. Headless A2A 协议层

协议层是**协议真相**所在。Renderer 层不持有真相。

### 4.1 一等对象（first-class objects）

| 对象 | 说明 | 生命周期 | 与 A2A 1.0 关系 |
|---|---|---|---|
| **Agent Card** | agent 的能力、端点、签名身份 | 长期，可更新 | 对齐 A2A 1.0 signed card |
| **Session** | 两个或多个 agent 的协调上下文 | 分钟~天 | A2A 之上的会话封装 |
| **Task** | 长任务，有状态机 | 分钟~天 | 对齐 A2A Task 语义 |
| **RelayEnvelope** | 所有协议事件的签名信封 | 单事件 | RelayFirst 独有 |
| **Delegation** | 会话委托授权 | 小时~月 | RelayFirst 独有 |
| **Receipt** | 执行回执 | 长期 | RelayFirst 独有 |
| **Settlement** | 批量结算对象 | 按 epoch | RelayFirst 独有 |
| **Approval** | HITL 审批对象 | 单次 | RelayFirst 独有 |

### 4.2 修正后的 RelayEnvelope（Solidity struct）

```solidity
/// @notice 所有 offchain 协议事件的签名信封。
/// @dev    domain 为链无关 EIP712Domain（见 §3.6）。relaySequence 不在签名内。
struct RelayEnvelope {
    // ---- 身份 ----
    bytes32 envelopeId;        // keccak256(canonical payload commitment)
    string  protocolVersion;   // "a2a-relay/1.0"
    string  agentId;           // "agent:eip155:<chainId>:<address>" 或 "agent:oidc:<issuer>:<sub>"
    uint8   scheme;            // SignatureScheme: 0=EOA,1=ERC1271,2=OIDC,3=CUSTOM
    string  verifier;          // 非 EOA scheme 的验证器/issuer；EOA 时为 ""

    // ---- 权威 ----
    bytes32 delegationId;      // 正在行使的 delegation grant；0x0 = owner-direct
    uint64  nonce;             // 单调，per (agent, policy)；撤销 = 递增

    // ---- 排序 ----
    uint64  sequence;          // 签名内：per-actor 序列，用于去重/防重放
    // 注意：relaySequence 由 relay 分配，位于签名之外，不在此 struct 中。

    // ---- 路由 / 作用域 ----
    string  tenantId;
    string  sessionId;
    string  taskId;
    string  topic;
    string  eventType;
    uint64  issuedAt;
    uint64  expiresAt;

    // ---- 载荷 ----
    bytes32 payloadHash;       // keccak256(canonical payload JSON 或 artifact ref)
    string  payloadRef;        // "inline" | "s3://..." | "ipfs://..."
    uint8   qos;               // 0=ephemeral,1=durable,2=audit,3=artifact

    // ---- 价值（可选） ----
    address valueToken;        // 0x0 = 无价值
    uint256 valueAmount;       // 0 = 无价值

    // ---- 签名 ----
    bytes   signature;         // 方案相关
}
```

**两级排序（v1 修正 #4；v1.1 澄清 #11）：**

| 字段 | 谁分配 | 在签名内 | 用途 |
|---|---|---|---|
| `sequence` | 签名者（per-actor） | ✅ 是 | 去重、防重放、检测 per-actor 乱序 |
| `relaySequence` | **投递该事件的 relay** | ❌ 否 | 该 relay 视角下的**局部投递序**，用于确定性重放与调试 |

**为什么必须有 `relaySequence`：** `sequence` 是 per-actor 的，无法给一个 task 排序——因为**多个 actor 写同一个 task**（requester 发 `TASK_WAITING_FOR_INPUT`，executor 发 `TASK_PROGRESS`）。只有投递方分配的序才能确定性地重放 task 状态机。

**关键澄清（v1.1 修正 #11）：`relaySequence` 是 local，不是 global。**

在 permissionless multi-relay 网络中（§15），一个 task 的事件会经由**多个独立节点**投递，这些节点**彼此不达成共识**（NET-3）。因此**不存在跨 relay 的全网全序**。正确的语义是：

- `relaySequence` 是**单节点局部序**，只在"该节点投递的事件集合"内有效。
- **权威的 task 状态转换依据是协议层的两级序，而非 relay 序：**
  1. **`sequence`（per-actor）** —— 用于去重与检测同一 actor 的乱序/重放。
  2. **每个 actor 的 `previousEventHash` hash chain** —— 用于确定该 actor 的事件先后。
  3. **协议状态机规则** —— 由收到的签名事件集合按上述两条规则确定性推导。
- 换言之：**task 状态的确定性来自签名事件的偏序（per-actor hash chain + 状态机），不来自任何 relay 的投递序。**

这与 §20 的"无法保证全网消息全序"是同一事实的两面，必须一起读。若某个部署（例如单租户私有集群）恰好只用一个 NATS stream，则 `relaySequence` 恰好等于全序——但**这是部署特例，不是协议保证**，客户端不得依赖它。

### 4.3 Payload envelope JSON 形状

`payloadRef == "inline"` 时，payload 内联；否则为引用。

```json
{
  "envelopeId": "0x9a1c...",
  "protocolVersion": "a2a-relay/1.0",
  "agentId": "agent:eip155:8453:0x9f4c...",
  "scheme": 0,
  "verifier": "",
  "delegationId": "0x7b2e...",
  "nonce": 1481,
  "sequence": 90233,
  "tenantId": "acme",
  "sessionId": "ses_01H...",
  "taskId": "tsk_01H...",
  "topic": "rf.v1.acme.agent.0x9f4c.session.ses_01H.task.tsk_01H.TASK_PROGRESS",
  "eventType": "TASK_PROGRESS",
  "issuedAt": 1760000123,
  "expiresAt": 1760000183,
  "payloadHash": "0x3d4f...",
  "payloadRef": "inline",
  "qos": 1,
  "valueToken": "0x0000000000000000000000000000000000000000",
  "valueAmount": "0",
  "signature": "0x1b2c...",
  "payload": {
    "progress": 0.42,
    "note": "retrieved 12 sources; 3 pending"
  }
}
```

### 4.4 事件类型表

| 事件类型 | QoS | 方向 | 说明 |
|---|---|---|---|
| `SESSION_OPEN` | Durable | ⇄ | 开启会话 |
| `SESSION_CLOSE` | Durable | ⇄ | 关闭会话 |
| `AGENT_CARD_PUBLISH` | Durable | → | 发布签名 Agent Card |
| `AGENT_CARD_UPDATE` | Durable | → | 更新 card |
| `TASK_CREATED` | Durable | → | 创建任务 |
| `TASK_OFFERED` | Durable | → | 向 executor 发出 |
| `TASK_ACCEPTED` | Durable | ← | executor 接受 |
| `TASK_REJECTED` | Durable | ← | executor 拒绝 |
| `TASK_STARTED` | Durable | ⇄ | 开始执行 |
| `TASK_PROGRESS` | Durable | ⇄ | 进度 |
| `TASK_WAITING_FOR_INPUT` | Durable | ⇄ | 等待对方输入 |
| `TASK_INPUT_PROVIDED` | Durable | ⇄ | 提供输入 |
| `TASK_WAITING_FOR_APPROVAL` | Audit | ⇄ | 等待 HITL 审批 |
| `TASK_APPROVED` | Audit | ← | 审批通过（签名事件，非按钮点击） |
| `TASK_DENIED` | Audit | ← | 审批拒绝 |
| `TASK_COMPLETED` | Audit | ⇄ | 完成 |
| `TASK_FAILED` | Audit | ⇄ | 失败 |
| `TASK_CANCELLED` | Durable | ⇄ | 取消 |
| `TASK_EXPIRED` | Durable | ⇄ | 超时（由 relay/clock 确定性地产生） |
| `APPROVAL_REQUEST` | Audit | → | 发起审批请求 |
| `APPROVAL_RESPONSE` | Audit | ← | 审批响应 |
| `DELEGATION_GRANT` | Audit | → | 授予委托（链锚定） |
| `DELEGATION_REVOKE` | Audit | → | 撤销委托（nonce 递增） |
| `RECEIPT_ISSUED` | Audit | ⇄ | 发出回执 |
| `RECEIPT_BATCH_COMMITTED` | Audit | → | 回执集提交 |
| `PAYMENT_AUTHORIZATION` | Audit | → | 支付授权（**逐字包裹 x402**，见 §8/§14 #9） |
| `SETTLEMENT_CLAIM` | Audit | → | 结算领取 |
| `ARTIFACT_REF` | Artifact | ⇄ | 大对象引用 |
| `ARTIFACT_AVAILABLE` | Artifact | ⇄ | 对象可获取 |

**Ephemeral 信号（presence、typing、heartbeat）不在上表。** 它们是**传输层信号，不是协议对象**，不进入签名信封。见 §14 修正 #6。

### 4.5 完整 Task 状态机（含所有超时边）

v0 只有 `TASK_EXPIRE` 从 `TASK_CREATED` 出发——"accepted 但从未 start"或"卡在 `TASK_WAITING_FOR_APPROVAL` 且审批过期"的任务会**永久挂起**。

**v1 修正（#5）：每个非终态都有一条超时边。**

```
                                   ┌──────────────┐
                                   │ TASK_CREATED │
                                   └──────┬───────┘
                                          │ TASK_OFFERED
                                          ▼
                                   ┌──────────────┐   TASK_REJECTED   ┌───────────────┐
                                   │ TASK_OFFERED │──────────────────▶│ TASK_REJECTED │◆
                                   └──────┬───────┘                   └───────────────┘
                        TASK_ACCEPTED     │        OFFER_TTL_EXPIRE
                                          ▼        ─────────────────▶ ┌──────────────┐
                                   ┌──────────────┐                   │ TASK_EXPIRED │◆
                                   │ TASK_ACCEPTED│                   └──────────────┘
                                   └──────┬───────┘                          ▲
                        TASK_STARTED      │     ACCEPT_START_TTL_EXPIRE       │
                                          │     （v1 新增超时边）              │
                                          ▼     ──────────────────────────────┘
                                   ┌──────────────┐
                          ┌───────▶│ TASK_RUNNING │◀───────┐
                          │        └──┬──┬──┬──┬──┘        │
             TASK_INPUT_PROVIDED       │  │  │  │           │ TASK_APPROVED
                          │           │  │  │  │           │
        ┌─────────────────┴──┐        │  │  │  │      ┌────┴──────────────────┐
        │ TASK_WAITING_FOR_  │        │  │  │  │      │ TASK_WAITING_FOR_     │
        │ INPUT              │        │  │  │  │      │ APPROVAL              │
        └─────────┬──────────┘        │  │  │  │      └────┬───────────┬──────┘
                  │ INPUT_TTL_EXPIRE  │  │  │  │           │           │
                  ▼ ──────────────────┼──┼──┼──┼───────────┼───────────┼────▶ TASK_EXPIRED ◆
                                      │  │  │  │           │ APPROVAL_  │
                                      │  │  │  │           │ EXPIRE     ▼
                                      │  │  │  │           │  ┌───────────────────────┐
                                      │  │  │  │           │  │ APPROVAL_EXPIRE 目标   │
                                      │  │  │  │           │  │ 由 policy 确定性决定： │
                                      │  │  │  │           │  │ 默认 → TASK_EXPIRED   │
                                      │  │  │  │           │  │ 可配 → TASK_CANCELLED │
                                      │  │  │  │           │  └───────────────────────┘
                                      │  │  │  │           │ TASK_DENIED
                                      │  │  │  │           ▼
                                      │  │  │  │      ┌──────────────┐
                                      │  │  │  │      │ TASK_DENIED  │◆
                                      │  │  │  │      └──────────────┘
                                      │  │  │  │
                    TASK_PROGRESS ────┘  │  │  └──── TASK_COMPLETED ──▶ ┌───────────────┐
                                        │  │                            │ TASK_COMPLETED│◆
                                        │  │                            └───────┬───────┘
                                        │  │                                    │ TASK_DISPUTED
                                        │  │                                    ▼
                                        │  │                            ┌───────────────┐
                                        │  │                            │ TASK_DISPUTED │
                                        │  │                            └───────┬───────┘
                                        │  │                                    │ 裁决
                                        │  │                    ┌───────────────┼───────────────┐
                                        │  │                    ▼               ▼               ▼
                                        │  │            ┌───────────────┐ ┌──────────┐ ┌──────────┐
                                        │  │            │ TASK_COMPLETED│ │TASK_FAILED│ │TASK_CANCELLED│
                                        │  │            └───────────────┘ └──────────┘ └──────────┘
                                        │  │ TASK_FAILED
                                        │  └──────────────────────────────▶ ┌──────────────┐
                                        │                                  │ TASK_FAILED  │◆
                                        │  RUNNING_HEARTBEAT_TTL_EXPIRE    └──────────────┘
                                        └──────────────────────────────────▶ TASK_EXPIRED ◆
                                                                            
    TASK_CANCELLED（可从任意非终态由授权方触发）◆
```

◆ = 终态（terminal）。

**超时边汇总表（v1 新增，必须实现）：**

| 源状态 | 超时事件 | 目标状态 | 默认 TTL | 说明 |
|---|---|---|---|---|
| `TASK_OFFERED` | `OFFER_TTL_EXPIRE` | `TASK_EXPIRED` | 300s | executor 未响应 |
| `TASK_ACCEPTED` | `ACCEPT_START_TTL_EXPIRE` | `TASK_EXPIRED` | 120s | **v1 新增：accepted 但从未 start** |
| `TASK_RUNNING` | `RUNNING_HEARTBEAT_TTL_EXPIRE` | `TASK_EXPIRED` | 900s | 无 heartbeat/进度 |
| `TASK_WAITING_FOR_INPUT` | `INPUT_TTL_EXPIRE` | `TASK_EXPIRED` | 3600s | 对方未提供输入 |
| `TASK_WAITING_FOR_APPROVAL` | `APPROVAL_EXPIRE` | `TASK_EXPIRED`（policy 可配 `TASK_CANCELLED`） | 1800s | **v1 新增：审批过期必须确定性移动** |
| `TASK_DISPUTED` | `DISPUTE_TTL_EXPIRE` | `TASK_FAILED` | 86400s | 裁决超时 |

**确定性要求（v1.1 修正 #11）：** 超时边的**触发**可以由任一 relay 的 clock 观测，但**状态转换的合法性**必须由**签名数据**判定——即 per-actor `sequence` + `previousEventHash` hash chain + 协议状态机规则。**不得**用 `relaySequence` 作为跨 relay 的状态转换依据（不同 relay 会给出不同顺序 → 状态机分叉）。超时判定应基于事件内的 `issuedAt` / `expiresAt`（签名内），而非 relay 的本地到达时间；relay clock 只用于"发现超时已发生"并生成一个新的签名超时事件。

---

## 5. 实时 Relay Fabric

### 5.1 Transport 与优先级

| Transport | 优先级 | 用途 | 备注 |
|---|---|---|---|
| **WebSocket** | 主 | agent 双向会话 | `github.com/coder/websocket`（见 §12） |
| **SSE** | 备 | 只读流 | 单向订阅 |
| **Webhook** | 服务端对服务端 | 企业 / AgentLand | at-least-once，幂等键 |
| **gRPC stream** | 内部 | edge ↔ authority | 控制面 |
| **NATS** | 内部（可选） | 单 operator 内部骨干 | JetStream 提供该 operator 内部的序；**非跨 relay 网络序**（见 §4.2） |

### 5.2 参考部署拓扑（可选，非网络定义）

**v1.1 澄清：** 下图的 "Go edge cluster" **不是** RelayFirst 网络的定义，而只是**一种可选的参考部署**——即某个 operator 选择用多个 edge 进程 + 共享 NATS 后端来跑**自己的**节点集群。在 permissionless 网络中（§15），这是众多拓扑之一：

- 个人用户可能只跑**一个** `relayfirst-node` 进程 + SQLite（无 NATS、无 Postgres）。
- 企业可能跑多 edge + NATS + Postgres。
- 社区公共节点可能介于两者之间。

**没有任何节点有义务采用下图拓扑，也没有任何节点是"官方"的。**

```
                          ┌──────────────────────────────┐
       agents ──WS/SSE──▶ │  Edge Relay #1 (Go)          │
       agents ──WS/SSE──▶ │  Edge Relay #2 (Go)          │──┐
       agents ──WS/SSE──▶ │  Edge Relay #N (Go)          │  │  publish
                          └──────────────────────────────┘  │
                                                             ▼
                                              ┌──────────────────────────────┐
                                              │  NATS JetStream (≥2.9)        │
                                              │  · 该 operator 内部的总序      │
                                              │  · 持久化 / 重放              │
                                              └──────────────┬───────────────┘
                                                             │  consume
                    ┌────────────────────────────────────────┼───────────────────────┐
                    ▼                    ▼                   ▼                       ▼
          ┌──────────────────┐ ┌──────────────────┐ ┌──────────────────┐ ┌──────────────────┐
          │ Authority Worker │ │ Authority Worker │ │ Settlement Worker│ │  Projection /    │
          │ (ethclient OK)   │ │ (ERC-1271/OIDC)  │ │ (ethclient OK)   │ │  Indexer         │
          └────────┬─────────┘ └──────────────────┘ └────────┬─────────┘ └──────────────────┘
                   │                                          │
                   ▼                                          ▼
          ┌──────────────────┐                       ┌──────────────────┐
          │ Postgres (pgx)   │                       │ EVM L2 (可选)    │
          │ Redis (Lua)      │                       └──────────────────┘
          │ S3 (artifacts)   │
          └──────────────────┘

  不变式 INV-2：Edge Relay 节点内不得出现 ethclient / 任何链读。
  注意：NATS 提供的"总序"仅限该 operator 内部，不是跨 relay 的网络全序（见 §4.2、§20）。
```

**Authority Worker 的网络位置（v1.1 澄清）：** authority 验证（ERC-1271 / OIDC / 撤销）**不要求**由 relay 自己完成。在 permissionless 网络中，agent 客户端可以：

1. **自行验证**（客户端本地做 ecrecover，并把 ERC-1271 查询路由到它信任的 RPC / verifier）；
2. 使用**第三方 verifier 服务**；
3. 依赖**它选定的 relay** 提供的验证（此时该 relay 被信任为 verifier，是客户端的选择，而非协议强制）。

这保持了 INV-2/INV-3（热路径 ecrecover-only、edge 无链读），同时让"谁来验证"成为**客户端的选择**而非中心化要求。

### 5.3 内部数据基础设施

| 组件 | 技术 | 职责 | 关键约束 |
|---|---|---|---|
| **消息骨干** | NATS JetStream（可选） | 单 operator 内部的总序、持久化、重放 | `nats-server ≥ 2.9`；**非网络骨干**（v1.1 修正 #13） |
| **持久化** | Postgres（`pgx/v5` + `sqlc`） | 事件、任务、回执、审计 | append-only 审计表 |
| **热状态 / 限流** | Redis（`go-redis/v9`） | nonce 缓存、额度原子计数、分布式限流 | Lua 原子性 |
| **对象存储** | S3 | artifact、快照、回执集 | 内容寻址（hash） |
| **可观测** | OpenTelemetry | trace / metric / log | `log/slog` |

### 5.4 Topic 模型（按 tenant/agent/session/task 分层）

```
rf.v1.<tenantId>.<agentId>.<sessionId>.<taskId>.<eventType>      ← 精确投递
rf.v1.<tenantId>.<agentId>.<sessionId>.>                          ← 会话订阅
rf.v1.<tenantId>.>                                                 ← 租户订阅（受限）
rf.v1.public.<topic>                                               ← 公共广播（严格限制）
```

**公共广播是 spam 最严重的路径，必须严格限制：**

- 需要显式授权 + 速率限制（见 §5.6）。
- 默认关闭；按 tenant 白名单开启。
- 公共广播事件不计入审计级持久化，除非显式标记。

### 5.5 QoS / 持久化分层

| Tier | 值 | 持久化 | 保留 | 例子 |
|---|---|---|---|---|
| **Ephemeral** | 0 | 无（仅传输） | 0 | presence、typing、heartbeat |
| **Durable** | 1 | Postgres + JetStream | 30d | task 事件 |
| **Audit-grade** | 2 | Postgres append-only + S3 + 可选链锚定 | 永久 | receipt、approval、delegation |
| **Artifact-reference** | 3 | S3 + hash（链上或链下） | 按 policy | 大输出 |

### 5.6 至少一次（at-least-once）立场

**我们不宣称 exactly-once。** 我们明确采用：

```
at-least-once delivery
  + eventId dedup（envelopeId 去重）
  + idempotent state transition（状态转换幂等）
  + sequence validation（per-actor `sequence` + `previousEventHash` hash chain）
```

**为什么：** exactly-once 在分布式投递中不可实现（或代价极高）。至少一次 + 幂等是工程上正确的选择。

> **AgentLand 集成点：** AgentLand 的 `outbox_events` 表已经有 `idempotency_key`，并有 `UNIQUE (world_id, topic, idempotency_key)` 约束，明确注释"consumer 按 at-least-once 语义重投, 靠 idempotency_key 去重"。RelayFirst 的 `eventId` 去重与 AgentLand 的 outbox 幂等键是同一模式的跨系统延续。

### 5.7 relay vs authority 的信任分离（v1 修正 #7）

**v0 错误：** relay 同时是 transport 和 authority（relay 缓存撤销、relay 要求 bond），而它自己的威胁表却假设 relay 可能不诚实——自相矛盾。

**v1 不变式：**

> **Relay = delivery，不可信（untrusted）。**
> **Authority = 可对链，或对客户端持有的签名 checkpoint 验证。**

| 维度 | Relay（不可信） | Authority（可验证） |
|---|---|---|
| 职责 | 投递、排序、去重、限流 | 授权验证、撤销真相、结算 |
| 信任假设 | 可能丢弃、重排、审查 | 可对链 / 签名 checkpoint 验证 |
| 多 edge / failover | ✅ 保护 **delivery** | ❌ **不**保护 authority-state 诚实性 |
| 客户端如何自保 | 至少一次 + multi-relay 冗余 + 重放 | 持有签名 checkpoint，独立验证 |

**明确写下：多 edge / failover 只保护投递，不保护权威状态诚实性。** 在完成这个拆分之前，"trust-minimized" 不是一个准确的宣称。

---

## 6. 加密与隐私

### 6.1 算法选择

| 用途 | 算法 | 说明 |
|---|---|---|
| 签名 | secp256k1（EVM） | `ecrecover`；Session EOA |
| 密钥交换 | **X25519** | 独立加密密钥 |
| 对称加密 | **XChaCha20-Poly1305** | 24-byte nonce，抗 nonce 重用 |
| KDF | **HKDF-SHA256** | 派生会话密钥 |
| 内容哈希 | keccak256（EVM 对齐） | envelope / payload |
| JWT（OIDC agent） | EdDSA / RS256 | 按 issuer JWKS |

### 6.2 私有任务的数据路径

```
Sender (X25519 enc key)                                     Receiver (X25519 enc key)
        │                                                            ▲
        │  1. ECDH(X25519) → shared secret                          │
        │  2. HKDF-SHA256(shared, salt=sessionId, info=taskId)       │
        │     → session key                                          │
        │  3. XChaCha20-Poly1305(session key, plaintext) → ciphertext│
        │  4. payloadHash = keccak256(ciphertext)                    │
        │  5. envelope.payloadRef = "inline" (ciphertext)            │
        ▼                                                            │
   ── RelayEnvelope ──▶ Relay（仅见密文）──▶ Receiver ──解密──────────┘
```

### 6.3 Relay 仍然能看到什么

即使 payload 加密，relay 仍可见：

- `agentId`、`tenantId`、`sessionId`、`taskId`
- `eventType`、`topic`、`qos`
- `issuedAt` / `expiresAt`
- `valueToken` / `valueAmount`（**若价值在信封明文**——见下方取舍）
- payload 大小与频率（流量分析面）

**取舍：** 是否把 `valueAmount` 明文放在信封里，是一个显式取舍。明文便于 relay 做额度原子检查（§8.10）；加密则 relay 无法检查额度，必须把检查推给 authority。**v1 选择：价值字段明文**，因为额度检查必须在热路径完成，而价值金额本身不敏感（链上最终也会公开）。

### 6.4 v1 明确推迟（out of scope）

| 项 | 状态 | 理由 |
|---|---|---|
| 前向保密（per-message ratchet） | ❌ 推迟 | 增加复杂度；Phase 4+ |
| 群组加密（多方 task） | ❌ 推迟 | Phase 4+ |
| 元数据混淆 / 流量分析防护 | ❌ 推迟 | Phase 5+ |
| 匿名身份（零知识） | ❌ 推迟 | 非核心 |
| 密钥轮换自动化 | ⚠️ 部分 | 手动 + nonce 撤销 |

---

## 7. Human-in-the-Loop

### 7.1 原则：审批返回一个**签名协议事件**，不是 UI 按钮点击

**v0 的隐含假设是"审批 = 点击按钮"。v1 明确纠正：审批是一个签名事件。**

- `APPROVAL_REQUEST` 是一个协议对象（Audit-grade）。
- `APPROVAL_RESPONSE` 是**签名事件**，可由任何 renderer 呈现，但语义唯一。
- 多个 renderer（Web、Telegram、Slack、CLI、AgentLand、企业系统）**呈现同一个 approval object**，不各自定义语义。

### 7.2 Approval Request JSON

```json
{
  "approvalId": "apr_01H...",
  "taskId": "tsk_01H...",
  "sessionId": "ses_01H...",
  "requestedBy": "agent:eip155:8453:0x9f4c...",
  "reason": "task requires spend above auto-approve threshold",
  "action": {
    "type": "PAYMENT_AUTHORIZATION",
    "valueToken": "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
    "valueAmount": "25000000",
    "beneficiary": "agent:eip155:8453:0x1a2b...",
    "x402PayloadRef": "s3://rf-artifacts/apr_01H/x402.json"
  },
  "policy": {
    "policyId": "pol_01H...",
    "autoApproveMax": "10000000",
    "approverRoles": ["owner", "treasury"]
  },
  "expiresAt": 1760001923,
  "renderedAs": ["web", "telegram", "cli"],
  "signature": "0x1b2c..."
}
```

### 7.3 多 renderer 表

| Renderer | 呈现方式 | 是否持有协议真相 | 备注 |
|---|---|---|---|
| Web App | 卡片 + 按钮 | ❌ | 按钮触发签名事件 |
| Telegram | 内联键盘 | ❌ | 回调 → 签名事件 |
| Slack | Block Kit | ❌ | 交互 → 签名事件 |
| CLI | 交互提示 | ❌ | `y/n` → 签名事件 |
| AgentLand | 游戏内 UI | ❌ | 与 AgentLand 审批门集成 |
| Enterprise | 工单 / API | ❌ | webhook → 签名事件 |

**所有 renderer 的产出都是同一个签名 `APPROVAL_RESPONSE` 对象。**

---

## 8. 可选 EVM 经济最终性

### 8.1 经济路径表

| 路径 | 触发条件 | 链上对象 | 频率 |
|---|---|---|---|
| **无价值协调** | 默认 | 无 | 高频 |
| **Delegation 锚定** | 需要第三方可验证授权 | `DelegationRegistry` | 低 |
| **支付授权** | agent 之间付费 | `PAYMENT_AUTHORIZATION`（包裹 x402） | 中 |
| **批量结算** | epoch 结束 / 阈值 | `SettlementManager` | 按 epoch |
| **争议** | 价值争议 | `DisputeManager` | 罕见 |
| **罚没** | 可证明的签名违规 | `SlashingManager` | 罕见 |

### 8.2 合约模块清单

| 合约 | 职责 |
|---|---|
| `DelegationRegistry` | 存储 / 验证 `SessionDelegation`；nonce 与撤销 |
| `SettlementManager` | Merkle 批量结算；commit receipt-set |
| `ClaimBitmap` | 全局抗双领 bitmap |
| `DisputeManager` | 争议提交与裁决 |
| `SlashingManager` | 可证明违规的罚没 |
| `PaymentAdapterX402` | 逐字包裹 x402（EIP-3009） |

### 8.3 修正后的 Merkle 批量结算流程

**v0 错误：** Merkle root 只证明 inclusion，不证明算术正确性。若 operator 算错 net amount 或遗漏 beneficiary，合法 receipt 持有者**没有链上救济**。

**v1 修正：commit 到 signed receipt-set（输入），而不只是派生的 tree（输出）。** 任何人可重算。

```
┌─────────────────────────────────────────────────────────────────────────────┐
│  Epoch N（固定时间窗，例：1h）                                                │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  1. Agents 产出 RECEIPT_ISSUED（signed receipt），relay 收集                 │
│  2. Epoch 关闭 → operator 收集 receipt-set                                   │
│  3. operator 计算：receiptSetRoot = merkle(signed receipts)  ← 输入承诺      │
│  4. operator 计算：leafSetRoot   = merkle(SettlementLeaf[])  ← 输出承诺      │
│  5. operator 计算：totalNet                                                  │
│  6. SettlementManager.commit(epoch, receiptSetRoot, leafSetRoot, totalNet,   │
│                              lateReceiptDeadline, prevBatchRoot)             │
│  7. 任何人可验证：leafSetRoot 是否由 receiptSetRoot + 公开推导函数正确得出    │
│     · 若 operator 遗漏 beneficiary 或算错 net → 差异可链上证明               │
│  8. beneficiary 领取：ClaimBitmap 全局检查 → 标记 → 转账                     │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

**为什么同时 commit 两个 root：** `receiptSetRoot` 是**输入**（不可被 operator 篡改），`leafSetRoot` 是**输出**（operator 的算术）。两者都上链，任何人都能独立重算 `leafSetRoot` 并证明 operator 的错误。**算术承诺修复。**

### 8.4 SettlementLeaf struct

```solidity
/// @notice 结算叶子：从 signed receipt-set 派生。
struct SettlementLeaf {
    bytes32 leafId;             // keccak256(beneficiaryAgentId, receiptId)
    string  beneficiaryAgentId; // "agent:eip155:<chainId>:<address>"
    address beneficiaryPayout;  // 实际收款地址
    address token;              // ERC-20 或 0x0 = native
    uint256 grossAmount;
    uint256 feeAmount;
    uint256 netAmount;          // grossAmount - feeAmount
    bytes32 receiptSetRoot;     // 该 leaf 派生自的 signed receipt-set root
    uint64  epoch;
}
```

### 8.5 SettlementBatch struct（含算术承诺）

```solidity
struct SettlementBatch {
    uint64  epoch;
    bytes32 receiptSetRoot;     // 输入承诺：signed receipts 的 merkle root
    bytes32 leafSetRoot;        // 输出承诺：SettlementLeaf[] 的 merkle root
    uint256 totalNet;           // sum(netAmount)
    address token;
    uint64  committedAt;
    uint64  lateReceiptDeadline; // 晚到回执窗口截止
    bytes32 prevBatchRoot;      // 链式承诺，防历史篡改
}
```

### 8.6 全局抗双领 bitmap（v1 修正 #8）

**v0 错误：** 抗双领 bitmap 是 per-epoch 的。一个 receipt 可能在 epoch N 和 N+1 **都被领取**。

**v1 修正：GLOBAL beneficiary-dimension bitmap。**

```solidity
/// @notice 全局抗双领：键跨所有 epoch，维度为 beneficiary × receipt。
contract ClaimBitmap {
    // claimKey = keccak256(beneficiaryAgentId, receiptId)
    mapping(bytes32 => bool) public claimed;

    function claim(bytes32 claimKey) external {
        require(!claimed[claimKey], "already claimed");
        claimed[claimKey] = true;
        // ... 转账
    }
}
```

**为什么是全局：** `claimKey = keccak256(beneficiaryAgentId, receiptId)` 跨 epoch 唯一。同一 receipt 无论在 epoch N 还是 N+1 被提交，都只能被领取一次。

### 8.7 Epoch 边界与晚到回执窗口

**显式定义（v1 要求）：**

```
epochLength          = 3600s        （默认，可配）
epoch(t)             = floor(t / epochLength)
lateReceiptWindow    = 900s         （默认，可配）

规则：
  · 时间戳 t 的 receipt 属于 epoch(t)
  · epoch 关闭后 lateReceiptWindow 内到达的 receipt：
      → 归入 epoch(t + 1)（下一个 epoch），不归入已关闭的 epoch
  · 超过 lateReceiptWindow 才到达的 receipt：
      → 拒绝（或进入 slow path，按 policy）
  · 由于 ClaimBitmap 全局，跨 epoch 归属不会造成双领
```

**确定性：** `epoch(t)` 与 `lateReceiptWindow` 都是纯函数 / 常量，任何验证者可独立复算。

### 8.8 罚没（Slashing）规则

**只对可证明的、签名过的违规罚没：**

| 违规 | 可证明性 | 罚没 |
|---|---|---|
| 对冲突状态双重签名（double-signing conflicting states） | ✅ 两个签名 envelope 即证据 | ✅ |
| 伪造 delivery ack | ✅ 签名矛盾 | ✅ |
| 双领（double-claim） | ✅ 链上 bitmap | ✅ |
| 伪造 / 冲突 Merkle root | ✅ 两个 root 即证据 | ✅ |
| **未投递消息（undelivered messages）** | ❌ | **绝不自动罚没** |
| 主观争议（质量、延迟） | ❌ | **交给人 / 验证者 / arbiter / 治理** |

> **红线：绝不因"消息未送达"自动罚没。** relay 不可信，投递失败可能是 relay 的问题，不是 agent 的。主观争议**不**做"伪 trustless"的自动仲裁。

---

## 9. 安全模型

### 9.1 威胁表（含 v1 修正条目）

| # | 威胁 | 假设 | 缓解 | v1 修正 |
|---|---|---|---|---|
| T1 | 恶意 relay 丢弃消息 | relay 不可信 | at-least-once + 重投 + multi-relay 冗余 | — |
| T2 | 恶意 relay 重排消息 | relay 不可信 | per-actor `sequence` + hash chain + 状态机；**不**依赖 relay 序 | ✅ #4 / #11 |
| T3 | 恶意 relay 伪造 presence | relay 不可信 | presence 从认证传输派生，非签名信封 | ✅ #6 |
| T4 | relay 缓存陈旧撤销 | relay 不可信 | 有界爆炸半径 = maxValuePerAction × cacheTtl | ✅ #3 |
| T5 | Session key 泄露 | 密钥可能泄露 | delegation 可撤销（nonce 递增）+ 原子额度 | ✅ #3 |
| T6 | 重放攻击 | 网络可重放 | 单调 nonce + envelopeId 去重 + sequence | — |
| T7 | 跨链重放 | 多链环境 | agentId 内嵌 chainId + 链锚定 delegationId | ✅ #1 |
| T8 | ERC-1271 热路径 DoS | 恶意合约 | ERC-1271 排除出 edge，仅 authority worker | ✅ #2 |
| T9 | 额度超支（N 个 session key） | 并发 | 原子 check-and-increment（Redis Lua，键 (agent,policy)） | ✅ #10 |
| T10 | Redis 不可用 | 基础设施故障 | fail-open vs fail-closed **必须写下来**（见 §9.2） | ✅ #10 |
| T11 | operator 算错结算 / 遗漏 beneficiary | operator 不可信 | commit signed receipt-set，任何人可重算 | ✅ #8 |
| T12 | 跨 epoch 双领 | operator 或 beneficiary 恶意 | 全局 ClaimBitmap（beneficiary × receipt） | ✅ #8 |
| T13 | 冲突状态双重签名 | agent 恶意 | 罚没（两个签名即证据） | — |
| T14 | 伪造 delivery ack | agent 恶意 | 罚没 | — |
| T15 | 未投递消息 | relay 或网络 | **绝不自动罚没**；重投 | — |
| T16 | 公共广播 spam | 任何人 | 严格限制 + 速率限制 + 白名单 | — |
| T17 | 主观争议 | 任意 | 交给人 / 验证者 / arbiter / 治理 | — |
| T18 | relay 同时充当 authority | 设计缺陷 | relay/authority 显式拆分；failover 只保投递 | ✅ #7 |
| T19 | 企业 OIDC agent 无法表示 | 设计缺陷 | 链无关 domain + scheme 判别符 | ✅ #1 |
| T20 | 跨语言 EIP-712 hash 不一致 | 实现风险 | in-house hashStruct + KAT 向量（见 §12） | — |

### 9.2 Redis 不可用时的 fail-open vs fail-closed（必须显式选择）

**这是一个必须写下来的取舍：**

| 策略 | 后果 | 适用 |
|---|---|---|
| **fail-open** | relay 继续接受价值事件，额度计数可能丢失 → **可能亏钱** | 非价值事件 |
| **fail-closed** | 拒绝所有价值事件 → **杀死实时 relay** | 价值事件 |

**v1 强制立场（分档）：**

```
· 非价值事件（valueAmount == 0）        → fail-open（保实时性）
· 价值事件（valueAmount > 0）           → fail-closed（保资金安全）
· 额度原子检查不可用时的价值事件        → 拒绝 + 告警
```

**理由：** 实时性是 relay 的命脉，但资金安全优先。分档让两者共存：非价值路径不因 Redis 抖动而中断，价值路径宁可拒绝也不超支。**该立场必须在配置中显式声明，不得隐式默认。**

### 9.3 关键不变式汇总

| ID | 不变式 |
|---|---|
| INV-1 | 签名 offchain 事件不得要求链知识 |
| INV-2 | Edge relay 内不得出现 `ethclient` / 任何链读 |
| INV-3 | 热路径签名只允许 `ecrecover`（纯本地计算，**edge 必需**；`ecrecover` 是计算，`eth_call` 才是 IO） |
| INV-4 | 撤销 = nonce 递增；损失上界 = maxValuePerAction × cacheTtl |
| INV-5 | 价值额度检查必须原子（Redis Lua，键 (agent,policy)） |
| INV-6 | 结算必须 commit signed receipt-set，而非仅派生 tree |
| INV-7 | 抗双领 bitmap 必须全局（beneficiary × receipt） |
| INV-8 | 绝不因未投递消息自动罚没 |
| INV-9 | Ephemeral 信号是传输层，非协议对象 |
| INV-10 | Relay = delivery（不可信）；Authority = 可验证 |

---

## 10. 端到端工作流

### 10.1 场景：AgentLand orchestrator 雇佣一个外部研究 agent

**背景：** AgentLand（TS monorepo）内的一个 orchestrator agent 需要雇佣一个外部研究 agent 完成一个长任务，并按结果付费。

**AgentLand 集成点（已核实）：**

- `agent_defs` 表：`on_chain_token_id text`（预留 Web3 agent NFT）、`tenant_id`、`world_id`、`owner_user_id uuid REFERENCES auth.users(id)`。
- `wallet_bindings` 表：`user_id`、`chain`、`wallet_address`、`UNIQUE (user_id, chain)`。
- `apps/api/src/infra/wallet-binding.ts`：SIWE 风格流程，`generateNonce`（`crypto.randomBytes(32)`）、`NONCE_TTL_MS = 10min`、`verifyMessage`（EIP-191）、`SUPPORTED_CHAINS = ethereum | sepolia | polygon | base`、失败即抛不写库。
- `outbox_events`：`idempotency_key` + `UNIQUE (world_id, topic, idempotency_key)`，at-least-once。
- `world_events`：append-only（trigger 禁止 UPDATE/DELETE）。

**映射：**

```
AgentLand agent_defs.id (uuid)
        │  （owner_user_id → wallet_bindings）
        ▼
wallet_bindings.wallet_address  +  chain
        │
        ▼
RelayFirst agentId = "agent:eip155:<chainId>:<address>"
```

> 注意：AgentLand 现有 wallet binding 使用 **EIP-191**（`verifyMessage`），而非 EIP-712。RelayFirst 的 Session Delegation 是 EIP-712。二者可以衔接：EIP-191 绑定确认"owner 拥有该钱包"，EIP-712 delegation 确认"该钱包授予 session key 有限权限"。

### 10.2 完整时序

```
时间 ──────────────────────────────────────────────────────────────────────────────▶

[Phase 0] Owner 绑定钱包（复用 AgentLand 流程）
  AgentLand owner_user_id ──EIP-191 verifyMessage──▶ wallet_bindings(chain=base, addr=0x9f4c)
  RelayFirst: agentId = agent:eip155:8453:0x9f4c

[Phase 1] Owner 授予 Session Delegation（onchain-anchored）
  Owner ──EIP-712 SessionDelegation（链绑定 domain）──▶ DelegationRegistry
    · sessionKey = 0xSessionEOA
    · policyHash = hash(policy: scopes + maxValuePerAction + maxValueTotal)
    · nonce = 0
    · validUntil = now + 24h

[Phase 2] Orchestrator 发现外部研究 agent（A2A 1.0）
  RelayFirst ──A2A Agent Card（signed）──▶ Orchestrator
  → 对齐 A2A 1.0 signed card；RelayFirst 不重定义 wire

[Phase 3] 开启会话
  Orchestrator ──SESSION_OPEN（RelayEnvelope, scheme=EOA, sessionKey 签）──▶ Edge Relay
  Edge: ecrecover ✅ → Redis nonce check ✅ → dedup ✅ → assign local relaySequence → 存储/扇出

[Phase 4] 创建任务
  Orchestrator ──TASK_CREATED──▶ Edge ──▶ Researcher
  Orchestrator ──TASK_OFFERED──▶ Edge ──▶ Researcher
  （OFFER_TTL_EXPIRE 计时开始，300s）

[Phase 5] 研究 agent 接受并开始
  Researcher ──TASK_ACCEPTED──▶ Edge
  （ACCEPT_START_TTL_EXPIRE 计时，120s —— v1 新增超时边）
  Researcher ──TASK_STARTED──▶ Edge
  （RUNNING_HEARTBEAT_TTL_EXPIRE 计时，900s）

[Phase 6] 长任务协作（每秒数千事件）
  Researcher ──TASK_PROGRESS（每 30s）──▶ Edge ──▶ Orchestrator
  Orchestrator ──TASK_WAITING_FOR_INPUT──▶ Edge ──▶ Researcher
  Researcher ──TASK_INPUT_PROVIDED──▶ Edge ──▶ Orchestrator
  （所有事件：ecrecover-only，edge 零链读 —— INV-2/INV-3）

[Phase 7] 私有数据交换
  Researcher ──X25519 + XChaCha20-Poly1305(密文)──▶ Edge（仅见密文）──▶ Orchestrator

[Phase 8] 需要 HITL 审批（超出自动阈值）
  Researcher ──TASK_WAITING_FOR_APPROVAL + APPROVAL_REQUEST──▶ Edge
  （APPROVAL_EXPIRE 计时，1800s）
  多 renderer 呈现同一 approval object
  Owner ──APPROVAL_RESPONSE（签名事件！）──▶ Edge ──▶ TASK_APPROVED
  （若超时：APPROVAL_EXPIRE → TASK_EXPIRED，确定性）

[Phase 9] 任务完成
  Researcher ──TASK_COMPLETED + RECEIPT_ISSUED──▶ Edge
  Receipt 为 Audit-grade；relay 收集入 epoch N 的 receipt-set

[Phase 10] 支付授权（包裹 x402）
  Orchestrator ──PAYMENT_AUTHORIZATION──▶ Edge
    payload = 逐字包裹的 x402 payload（EIP-3009 transferWithAuthorization）
    ← 不重新签名，保证 facilitator 可验证

[Phase 11] 批量结算（epoch N 关闭）
  Settlement Worker:
    1. receiptSetRoot = merkle(signed receipts)      ← 输入承诺
    2. leafSetRoot    = merkle(SettlementLeaf[])      ← 输出承诺
    3. totalNet
    4. SettlementManager.commit(epoch=N, receiptSetRoot, leafSetRoot, totalNet,
                                 lateReceiptDeadline, prevBatchRoot)
  Researcher ──SETTLEMENT_CLAIM(leafId)──▶ ClaimBitmap.claim(claimKey)
    claimKey = keccak256(beneficiaryAgentId, receiptId)  ← 全局，跨 epoch
    → 转账 netAmount

[Phase 12] 审计与可选锚定
  world_events（AgentLand append-only）+ RelayFirst audit 表
  可选：把 receiptSetRoot 锚定到 L2
```

---

## 11. 部署演进

### 11.1 Phase 表（含"不该做什么"列）

| Phase | 目标 | 该做 | **不该做什么（do NOT do this yet）** |
|---|---|---|---|
| **Phase 0** | Local Runtime | 单进程 relay + 内存 store；ecrecover 热路径；本地 SDK；确定性状态机 | ❌ 不上链、不接 NATS、不做分布式、不做结算 |
| **Phase 1** | Durable Relay | Postgres + NATS JetStream（可选）；relaySequence（局部）；at-least-once + 幂等；QoS 分层 | ❌ 不做多 edge、不做 Redis 原子额度、不接链 |
| **Phase 2** | Authority Plane | DelegationRegistry（测试网）；nonce/撤销；authority worker；scheme 判别符 | ❌ 不做主网、不做真实资金、不做结算 |
| **Phase 3** | Encrypted Tasks | X25519 + XChaCha20-Poly1305；artifact 存储；私有任务路径 | ❌ 不做群组加密、不做前向保密、不做元数据混淆 |
| **Phase 4** | Human-in-the-Loop | 签名 approval 对象；多 renderer；审批状态机 | ❌ 不做自动审批、不做"伪 trustless"仲裁 |
| **Phase 5** | Economic Finality | SettlementManager；Merkle 批量结算；全局 ClaimBitmap；x402 逐字包裹 | ❌ 不做主网大额、不做自动罚没、不做主观仲裁 |
| **Phase 6** | Permissionless Nodes | **发布 RFN 节点协议 + `relayfirst/node` 容器**；RFN-04 信息文档；Agent Card 声明 relay set；multi-relay publish/fallback；per-operator 反滥用 | ❌ 不做 token 挖矿、不做质押奖励网络、不做全球节点激励 |
| **Phase 7** | Federation | 跨 operator 联邦；跨 relay 路由；公共广播白名单；可选 Nostr adapter；Indexer 生态 | ❌ 不把 Nostr 当 substrate、不宣称瞬时撤销、不强制节点共识 |

**Phase 6 是 v1.1 新增的核心阶段。** 它的判据不是"技术能跑"，而是"**一个陌生用户能在 10 分钟内、用一条 docker 命令、在自己的 VPS 上跑起自己的 Agent inbox，并且不依赖 RelayFirst 官方节点就能收发任务**"。这一条不满足，就不算完成去中心化。

**产品优先级提醒（必须遵守）：** 技术上"完全去中心化"可做；但产品上应优先实现**完全开放的协议与自托管节点能力**，而**不要**把第一版资源耗在 token 挖矿、质押或全球节点奖励网络上。真正让用户愿意跑节点的，首先是他们需要为自己的 Agent 保留 inbox、数据、隐私、可用性和主权；**节点奖励是后续增强，不是网络成立的前提**。

### 11.2 演进原则

- **每个 Phase 都可独立运行**，不依赖后续 Phase。
- **Phase 0 就能证明核心价值**（offchain 协调 + 签名身份 + 状态机），无需任何链。
- **链只在 Phase 2+ 出现，资金只在 Phase 5 出现。**
- **"不该做什么"列是硬约束**，防止过早引入复杂度和不可逆风险。

---

## 12. 技术选型

### 12.1 库选择总原则（三分法）

**v1.1.1：** 与 `MVP.md` §8 同步。**先看这张表，不要把它误读成"全自研"。**

| 类别 | 策略 | 例子 |
|---|---|---|
| **① 密码学原语** | **永远用成熟库，绝不自研** | keccak256 · secp256k1 · `ecrecover` · X25519 · ChaCha20-Poly1305 |
| **② 编码 / 协议** | **优先用库**，除非有具体理由 | EIP-712 hashing · A2A 序列化 · HTTP · SQLite |
| **③ 业务数据结构** | **必须自研**（世界上没有现成轮子） | RelayEnvelope · Delegation · Receipt · 计分 |

> **规则：密码学原语永远用库；业务数据结构永远自研；中间那层看情况。**
>
> **"自研"在本节只指第 ③ 类** —— 它们没有库，因为它们**就是 RelayFirst 的产品本身**。

### 12.2 Go 库选择

| 用途 | 选择 | 类别 | 理由 / 注意 |
|---|---|---|---|
| **WebSocket** | `github.com/coder/websocket` | ② | **不用 `gorilla/websocket`**（advisory **GO-2026-6278**，弱 PRNG mask key）。coder：原生 `context.Context`、zero-alloc 读写、并发写、`CloseRead`、活跃维护 |
| **keccak256** | `golang.org/x/crypto/sha3` | ① | 标准扩展库，**绝不自研** |
| **secp256k1 签名 / 验签** | `github.com/decred/dcrd/dcrec/secp256k1/v4` | ① | 纯 Go，**MIT**，无 CGO |
| **`ecrecover`** | 同上（`secp256k1.RecoverCompact`）或 `go-ethereum/crypto` | ① | **热路径必需**，见下方修正说明 |
| **EIP-712 hashing** | **`apitypes`** 或 in-house `hashStruct` | ② | **二选一，都行** —— 见 §12.3 |
| **`ethclient`** | `github.com/ethereum/go-ethereum/ethclient` | ① | **仅** authority / settlement worker；**绝不**在 edge |
| **SQLite（最小节点）** | `modernc.org/sqlite` | ① | **纯 Go，无 CGO** —— 见 §12.4 |
| **NATS**（可选） | `github.com/nats-io/nats.go` + `nats.go/jetstream` | ② | 需 `nats-server ≥ 2.9`；Scaled 级别才用 |
| **Postgres**（可选） | `github.com/jackc/pgx/v5` + `sqlc` | ② | 类型安全查询 |
| **Redis**（可选） | `github.com/redis/go-redis/v9` | ② | Lua 原子脚本 |
| **限流（本地）** | `golang.org/x/time/rate` | ② | 单实例 |
| **限流（分布式）** | Redis + Lua | ② | 跨 edge |
| **可观测** | `go.opentelemetry.io/otel` | ② | trace / metric |
| **日志** | 标准库 `log/slog` | ② | 零依赖 |

#### ⚠️ v1.1.1 修正：`ecrecover` 不属 authority 专属

**v1.1 错误：** 上表曾写 "`ecrecover` / keccak：**仅** authority / settlement worker 使用"。

**冲突：** 这与本文自身四处设计直接矛盾：

| 位置 | 原文 |
|---|---|
| §2.2 | 单个 RelayEnvelope：offchain，**热路径，ecrecover-only** |
| INV-3 | 热路径签名**只允许 `ecrecover`**（纯本地） |
| §3.7 步骤 6 | EOA → `ecrecover(sig) == agentId.address`（**纯本地，热路径**） |
| §12.5 | `relay-edge`：热路径；**ecrecover-only**；零链读 |

**正确表述：**

```text
✅ ecrecover / keccak  → 【edge 热路径必须用】
                          纯本地计算，不产生任何 RPC
❌ ethclient / eth_call → 【仅 authority / settlement worker】
                          这才是真正"绝不进 edge"的东西
```

**原因：** `ecrecover` 是**纯本地密码学运算**（给定签名和哈希，恢复出地址），不碰网络、不读链。把它排除出 edge 会让热路径无法验签 —— 而 INV-2 禁止的从来只是**链读（RPC）**，不是本地密码学。

> **一句话区分：`ecrecover` 是计算，`eth_call` 是 IO。只有 IO 需要被赶出 edge。**

#### LGPL 的正确表述（纠正 v1.1）

- go-ethereum 是 **LGPL-3.0**。
- **LGPL 对"链接使用"通常可接受** —— go-ethereum 被大量商业产品使用。
- **它不是"不能用"，是一个"值得知道"的考虑。** 只有需要**静态链接 + 闭源分发**时才需认真对待。
- **本设计选用纯 Go 替代**（`x/crypto/sha3` + `decred secp256k1`）的主要理由是**可测 + 依赖干净 + 无 CGO**，LGPL 只是次要考虑。


### 12.3 EIP-712：用库还是自研？

**两条路都可行，选哪个不重要 —— 重要的是 §12.7 的 KAT。**

| 选项 | 优点 | 缺点 |
|---|---|---|
| **A. 直接用 `apitypes`** | 零代码；go-ethereum 维护 | 拖入 `signer/core`；LGPL-3.0 |
| **B. 自研薄 hasher（~200 行）** | 依赖干净；可读；无 CGO | 要写代码 + 测 |

**EIP-712 是编码规范，不是密码学原语：**

```text
hashStruct(s) = keccak256(typeHash ‖ encodeData(s))
动态 bytes/string → keccak256(内容)
数组 → keccak256(拼接的 encodeData)
```

**规范完全确定，没有歧义。** 所以它可安全自研 —— 但**用库也完全正确**。

**决策建议：**

- **不想纠结 → 选 A**（用 `apitypes`），把时间花在 KAT 上。
- **在意依赖干净 / 无 CGO → 选 B**（自研薄层）。

### 12.4 无 CGO 约束（静态单二进制）

**整个节点必须能用 `CGO_ENABLED=0` 构建。**

理由：`docker run` 一条命令就能跑（§16.4）的前提是**静态单二进制**。CGO 会引入 glibc 依赖，破坏这一点，并让交叉编译变复杂。

| 需求 | 必须选 | 不能选 |
|---|---|---|
| SQLite | `modernc.org/sqlite`（纯 Go） | `mattn/go-sqlite3`（CGO） |
| secp256k1 | `decred/dcrd/dcrec/secp256k1/v4`（纯 Go） | 需 CGO 的变体 |

**CI 检查：`CGO_ENABLED=0 go build ./...` 必须通过。**

### 12.5 二进制 / module 拆分（含 no-ethclient-in-edge 不变式）

**v1.1 重要：** 下图的 `cmd/relay-edge` 等**不是**网络要求，而是**参考实现（reference node）**的模块划分。一个最小兼容节点（§16.1）只需要 `RFN Core` 子集，且**不需要** NATS / Postgres / Redis / S3——用 SQLite 即可（见 §16.1）。多进程拆分仅在 operator 需要横向扩展时才有意义。

```
cmd/
  relayfirst-node/     ← 单进程节点入口（最小兼容；SQLite；可选后端）
  relay-edge/          ← 参考实现：热路径；ecrecover-only；零链读（INV-2）
  authority-worker/    ← 参考实现：授权验证；ERC-1271 / OIDC；ethclient OK
  settlement-worker/   ← 参考实现：结算；Merkle；ethclient OK
internal/
  envelope/            ← RelayEnvelope 解析 / hashStruct
  state/               ← 确定性 Task 状态机
  relay/               ← 节点逻辑（RFN Core）
  discovery/           ← Agent Card / Indexer / RFN-04 信息文档
  authority/           ← delegation / nonce / verifier registry
  settlement/          ← receipt-set / merkle / claim
  crypto/              ← X25519 / XChaCha20-Poly1305 / HKDF
  storage/             ← sqlite（最小） / pgx / redis / s3（可选）
sdk/
  ts/  go/  python/    ← 共享 KAT 向量
```

**最小节点存储分级（v1.1）：**

| 级别 | 存储 | 适用 | 依赖 |
|---|---|---|---|
| **Minimal** | SQLite（单文件） | 个人 / Home Node | 零外部依赖，`docker run` 即用 |
| **Standard** | Postgres + Redis | 社区 / 团队节点 | 需要持久化与原子额度 |
| **Scaled** | Postgres + Redis + NATS + S3 | 企业 / 高吞吐 operator | 需要多 edge 与该 operator 内部的序 |

**RFN Core 不依赖 NATS。** NATS JetStream 是 Scaled 级别的**可选**后端，用于单 operator 内部的总序与重放；它不是协议要求，也不是跨 relay 共识机制。

**不变式（INV-2）强制手段：**

- `cmd/relay-edge` 与 `cmd/relayfirst-node` 的依赖图**不得**包含 `ethclient`。
- CI 中加静态检查：这些二进制若链接到 `ethclient` → 构建失败。
- 代码评审规则：节点目录内出现 `ethclient` / `eth_call` / RPC URL → 拒绝。

### 12.6 跨语言 EIP-712 hash 与 KAT 向量（硬性判据）

**这是发布多个 SDK 的真实隐藏成本，也是全文档最重要的工程控制点。**

**问题：** viem（TS）、eth-account（Python）、go-ethereum（Go）在**嵌套 struct** 与**动态数组**的 typed-data hashing 上行为不一致。若不锁定，同一 delegation 在三个 SDK 下可能产生不同 hash → 签名不互通 → 灾难性 bug。

**唯一有效的缓解是 KAT 向量（不是"选对库"）：**

```
testdata/eip712-vectors.json
  → 10 个用例，覆盖：嵌套 struct · 动态数组 · 长 string · 空值 · 多字节 UTF-8
  → 每个用例含：{ types, domain, message, expectedHash }
  → CI 中：
      TS（viem）算一遍   → 必须等于 expectedHash
      Go（你的实现）算一遍 → 必须等于 expectedHash
      不一致 → 构建失败
```

**生成方式：** 用 viem（MIT，业界标准）生成 `expectedHash` 作为基准，Go / Python 侧对齐它。

> **有这组向量，自研 `hashStruct` 与用 `apitypes` 的差别只剩约 200 行代码。**
> **没有这组向量，用库也会在半年后炸** —— 因为跨语言边界 bug 无法靠"用库"避免。

**与 `MVP.md` 的关系：** MVP 已将 KAT 列为**验收判据 ⑥**（TS 与 Go 对同一向量必须字节相同），并把 `CGO_ENABLED=0` 构建列为判据 ⑦。见 `MVP.md` §8.4 与 §11。

---

## 13. 最终技术描述

### 13.1 English（canonical）

> **RelayFirst is an Ethereum-native, headless agent-to-agent coordination fabric that runs as a permissionless, multi-relay network. It does not compete with the A2A wire protocol — it interoperates with A2A 1.0 (a Linux Foundation standard with signed Agent Cards and AP2 payments) as the authority, receipt, and settlement layer that A2A lacks. Anyone can run a RelayFirst node; every Agent owns an EVM-anchored identity, publishes its own signed Relay Set, and remains reachable through multiple independent nodes without platform lock-in or a single official relay. Event validity comes from cryptographic signatures and protocol rules, not from RelayFirst's servers. RelayFirst gives autonomous agents scoped, revocable, budgeted delegation via EIP-712 session grants, verifiable execution receipts, and batched, double-claim-resistant settlement. Identity is anchored in EVM accounts (`agent:eip155:<chainId>:<address>`); thousands of signed events per second flow offchain through untrusted, independently operated nodes, while authority is verifiable against chain or a client-held signed checkpoint. Relay immediately. Verify continuously. Settle only when value is involved.**

### 13.2 中文（canonical）

> **RelayFirst 是一层 Ethereum-native、headless 的 agent-to-agent 协调织物，以 permissionless、multi-relay 网络的形式运行。它不与 A2A wire protocol 竞争——它与 A2A 1.0（Linux Foundation 标准，具备 signed Agent Cards 与 AP2 支付）互操作，充当 A2A 所缺失的权威、回执与结算层。任何人都可以运行 RelayFirst 节点；每个 Agent 持有以 EVM 锚定的身份，发布自己签名的 Relay Set，并通过多个独立节点保持可达，无需平台锁定，也不存在唯一官方 relay。事件是否有效取决于密码学签名与协议规则，而不是 RelayFirst 官方服务器。RelayFirst 为自治 agent 提供基于 EIP-712 session grant 的、带作用域、可撤销、带预算的委托授权，可验证的执行回执，以及批量、抗双领的结算。身份锚定在 EVM 账户（`agent:eip155:<chainId>:<address>`）；每秒数千条签名事件经由不可信、由不同 operator 独立运行的节点走 offchain，而权威性可对链或对客户端持有的签名 checkpoint 验证。立即中继。持续验证。仅当涉及价值时结算。**

---

## 14. v0 → v1 修正记录

> 本节保留设计推理，防止修正被遗忘或被"优化"回退。每条包含：v0 的错误 → v1 的修正 → 理由。

### 修正 #1：EIP-712 domain 不得钉死单链

- **v0：** 所有 offchain 事件 domain 钉死 `chainId: 8453`、`verifyingContract: 0xRelayFirstAuthorityRegistry`。
- **后果：** (a) 企业 OIDC/JWT 账户行无法表示；(b) "可选链上最终性"在签名时刻变强制；(c) Arbitrum agent 无法产出 Base envelope。
- **v1：** offchain 事件 domain 移除 `chainId`/`verifyingContract`；envelope 增加显式 `scheme` 判别符（EOA / ERC-1271 / OIDC / CUSTOM）。**仅** onchain-anchored 对象（delegation grant、settlement）携带链绑定 domain。
- **推理：** 签名一个 offchain 事件不应要求链知识（INV-1）。跨链重放由 `agentId` 内嵌 chainId + 单调 nonce 防护。

### 修正 #2：ERC-1271 不得在热 relay 路径上

- **v0：** 每个 edge relay 都有"ERC-1271 async verification path"。
- **后果：** ERC-1271 是 `eth_call`；每秒数千事件/edge 下是 RPC 扩展与活性 bug，且使 relay 依赖链（违背"offchain、无需链"）。
- **v1：** delegation 只授予一次；热路径事件 `ecrecover`-only（纯本地计算）。Smart Account → 发出 delegation → Session EOA 签名数千事件，零链读。只有 grant 与 settlement 触链。
- **不变式（INV-2）：** edge relay 内不得出现 `ethclient` / 任何链读。

### 修正 #3：统一 nonce 与撤销

- **v0：** 同时有 `nonce` 与 `revocationNonce`。
- **后果：** 第二真相源 + TOCTOU 窗口（relay 读到 `revocationNonce=5`，撤销落地，relay 用陈旧状态验证支付）。
- **v1：** 每 `(agent, policy)` 单一单调 nonce；撤销 = 递增。
- **诚实后果（必须写下）：** 撤销对已缓存 relay **非瞬时**；爆炸半径 = `maxValuePerAction` × cache TTL。**这个有界窗口论证就是安全论证本身。**

### 修正 #4：增加 relay 分配的 total order

- **v0：** `sequence` 为 per-actor。
- **后果：** 无法给一个 task 排序——多个 actor 写同一 task（requester 发 `TASK_WAITING_FOR_INPUT`，executor 发 `TASK_PROGRESS`）。
- **v1：** 两级——签名的 per-actor `sequence`（去重/防重放）+ relay 分配的 `relaySequence`（来自 NATS JetStream，总序与确定性状态转换验证）。
- **v1.1 修正 #11（重要）：** `relaySequence` 的"总序"是**局部**的，不是网络全序。见 §14.2 修正 #11。

### 修正 #5：补齐状态机超时缺口

- **v0：** 只有从 `TASK_CREATED` 出发的 `TASK_EXPIRE`。
- **后果：** accepted-but-never-started 或卡在 `TASK_WAITING_FOR_APPROVAL` 且审批过期的任务永久挂起。
- **v1：** 每个非终态都有超时边；`APPROVAL_EXPIRE` 必须确定性移动任务（默认 → `TASK_EXPIRED`，policy 可配 → `TASK_CANCELLED`）。见 §4.5。

### 修正 #6：从签名信封移除 presence

- **v0：** Ephemeral 事件（presence、typing、heartbeat）标为"signature optional"，却放进同一 envelope 模型。
- **后果：** 未认证 presence 允许任何人伪造"agent X 在线"。
- **v1：** presence 从**认证传输**派生；ephemeral 信号是**传输层**，**不是协议对象**（INV-9）。

### 修正 #7：拆分不可信 relay 投递与 authority 平面

- **v0：** relay 同时是 transport 与 authority（relay 缓存撤销、relay 要求 bond），而其威胁表假设 relay 可能不诚实。
- **v1：** 明确不变式——relay = delivery（不可信）；authority = 可对链或客户端持有的签名 checkpoint 验证。**多 edge / failover 只保护 DELIVERY，不保护 authority-state 诚实性。** 在此拆分明确前，"trust-minimized" 不是准确宣称。

### 修正 #8：修复结算算术与双领

- **v0：** 只 commit 派生 Merkle tree；per-epoch 抗双领 bitmap。
- **后果：** Merkle root 只证明 inclusion，不证明算术正确；operator 算错 net 或遗漏 beneficiary 时，合法 receipt 持有者无链上救济。receipt 可在 epoch N 与 N+1 双领。
- **v1：** commit 到 **signed receipt-set**（输入），任何人可重算；抗双领为**全局** beneficiary-dimension bitmap（`claimKey = keccak256(beneficiaryAgentId, receiptId)`，跨 epoch）。定义 epoch 边界与晚到回执窗口（§8.7）。

### 修正 #9：逐字包裹 x402

- **v0：** 隐含重新签名支付。
- **后果：** facilitator 无法验证，生态兼容性丢失。
- **v1：** `PAYMENT_AUTHORIZATION` payload **逐字包裹** VERBATIM x402 payload（EIP-3009 `transferWithAuthorization`）。x402 是 **Payment Adapter**，**不是** transport，且**不得**出现在产品叙事第一句。

### 修正 #10：并发 bug——`maxValueTotal` 超支

- **v0：** N 个 session key 各有 `maxValueTotal=10 USDC`，真实上限却是 10×N。
- **v1：** 按 `(agent, policy)` 原子 check-and-increment（Redis Lua）。同时记录 fail-open vs fail-closed 取舍（§9.2）：fail-open 亏钱；fail-closed 杀死实时 relay。**强制分档：非价值 fail-open，价值 fail-closed**，且立场必须显式写下。

### 14.1 保留下来的原始设计（判定正确，继续沿用）

| 设计 | 说明 |
|---|---|
| 拒绝 Nostr/npub/BIP-340 作为身份 | EVM 账户为规范身份；Nostr 至多 Phase 3+ adapter；NIP-26 已被官方标为 'unrecommended' |
| 密钥层级 | Owner/Smart Account → EIP-712 Session Delegation → Session Signing Key → 独立 X25519 Encryption Key（加密密钥零资产权威） |
| at-least-once + 去重 + 幂等 + sequence | 明确**替代** exactly-once 宣称 |
| 审批 = 签名协议事件 | 多 renderer 呈现同一 approval object |
| 罚没仅限可证明的签名违规 | 绝不因未投递消息自动罚没；主观争议交给人/验证者/arbiter/治理 |
| 账户兼容矩阵 | EOA / Session Key EOA / ERC-4337 / ERC-6551 TBA / Safe / OIDC |
| QoS / 持久化分层 | Ephemeral / Durable / Audit-grade / Artifact-reference |
| 分阶段路线图 + "不该做什么"列 | Phase 0 Local Runtime → Phase 6 Federation |
| Topic 模型分层 | tenant/agent/session/task；公共广播严格限制 |
| 分层组件视图 | Renderer → SDK → 协议 → Relay → Authority → EVM |

---

## 14.2 v1 → v1.1 修正记录（permissionless 网络模型）

> v1.1 的目标是让 RelayFirst 从"官方运行一组 relay 的托管服务"变成"像 Nostr 一样任何人都能跑节点的 permissionless 协议"。本节记录为此必须做的修正——尤其是那些**与新模型直接冲突**的 v1 表述。

### 修正 #11：`relaySequence` 不是全网全序

- **v1 表述：** `relaySequence`（来自 NATS JetStream）提供"总序（total order）与确定性状态转换验证"。
- **冲突：** 在 multi-relay 网络中，事件经由多个**互不共识**的独立节点投递（NET-3），**不存在跨 relay 的全网全序**。若客户端依赖 relay 序，不同 relay 会给出不同顺序 → 状态机分叉。
- **v1.1：** `relaySequence` 降级为**单节点局部投递序**（用于重放与调试）。task 状态的权威依据改为：per-actor `sequence`（去重/防重放）+ per-actor `previousEventHash` hash chain + 协议状态机规则。见 §4.2。
- **推理：** 确定性必须来自**签名数据本身**，不能来自任何单个 relay 的投递行为——否则 relay 就成了隐式的权威，违背 NET-1/NET-3。

### 修正 #12：ERC-1271 不得成为节点"最低要求"

- **v1.1 草稿表述：** 最小兼容节点必须"对 Smart Account 支持 ERC-1271 验签，或转发至 verifier"。
- **冲突：** 这与 INV-2（节点内无链读）和 INV-3（热路径 ecrecover-only）直接矛盾。ERC-1271 是 `eth_call`；若要求**每个**节点都能做，则每个节点都需要 RPC 与链访问——既破坏"offchain、无需链"，也把轻节点门槛抬高到不可能自托管。
- **v1.1：** 最小节点**只要求** EOA `ecrecover`（纯本地）。ERC-1271 / OIDC / CUSTOM 的验证是**可选扩展**，且"由谁验证"是**客户端的选择**（自验 / 第三方 verifier / 信任其选定的 relay），而非协议对节点的强制。见 §5.2、§16.1、§16.2。

### 修正 #13：NATS 从"骨干"降级为"可选后端"

- **v1 表述：** §2.1 与 §5 把 NATS JetStream 写成 Relay Fabric 的组成部分（"Go edge relays · NATS JetStream · Postgres · Redis · S3"）。
- **冲突：** 若 NATS 是网络骨干，则跑一个节点就需要运维 NATS——个人用户无法 `docker run` 起节点。这与"任何人都能跑节点"矛盾。
- **v1.1：** 引入**存储分级**（§12.2）：Minimal（SQLite，零外部依赖）/ Standard（Postgres+Redis）/ Scaled（+NATS+S3）。**RFN Core 不依赖 NATS。** NATS 仅是单 operator 内部横向扩展时的可选后端。

### 修正 #14：标准族命名统一为 RFN-xx

- **冲突：** 草稿同时出现 `RFN-04 Relay Information Document` 与 `RFN-11 Relay Information Document`，编号冲突。
- **v1.1：** 统一为 §19 的标准族：**RFN-04 = Relay Information Document**；`RFN-11 = Settlement Adapter`。同时明确 RFN-04 定义的文档俗称 "RFN-11 style"（沿用 Nostr NIP-11 的类比）在文档中只作为**历史类比**出现，不作为编号。

### 修正 #15：Phase 6 由 "Federation" 改为 "Permissionless Nodes"

- **v1：** Phase 6 = Federation（多 edge / failover / 跨租户联邦），把"多节点"当作最后的锦上添花。
- **冲突：** 若 permissionless 是核心定位，它就不该是最后一个阶段才出现的能力。
- **v1.1：** Phase 6 = **Permissionless Nodes**（发布 RFN 节点协议 + `relayfirst/node` 容器 + RFN-04 信息文档 + Agent Card relay set + multi-relay publish/fallback）；Phase 7 = Federation（跨 operator 联邦、跨 relay 路由、Indexer 生态）。**去中心化是产品定义的一部分，不是收尾工作。**

### 修正 #16：明确节点经济不做 token 挖矿

- **风险：** "允许用户跑节点"最容易滑向"发 token + 排放补贴 + 质押奖励"。
- **v1.1：** 节点必须有**真实业务收入路径**（存储、优先级、索引、SLA、结算服务），而非 Day 1 靠排放。**明确列为 non-goal：不做 token 挖矿、不做质押奖励网络。** 见 §18。

---

## 14.3 v1.1 → v1.1.1 修正记录（技术选型同步）

> v1.1.1 的目标是让技术选型自洽，并与 `MVP.md` §8 对齐。

### 修正 #17：`ecrecover` 不属 authority 专属（内部矛盾）

- **v1.1 表述：** §12.1 写 "`ecrecover` / keccak：**仅** authority / settlement worker 使用"。
- **冲突：** 这与本文自身**四处**设计直接矛盾：
  - §2.2 —— 单个 RelayEnvelope：offchain，**热路径，ecrecover-only**
  - INV-3 —— 热路径签名**只允许 `ecrecover`**（纯本地）
  - §3.7 步骤 6 —— EOA → `ecrecover(sig) == agentId.address`（**纯本地，热路径**）
  - §12.5 —— `relay-edge`：热路径；**ecrecover-only**；零链读
- **v1.1.1：** 明确区分 —— **`ecrecover` / keccak 是 edge 热路径必需**（纯本地计算，无 RPC）；**只有 `ethclient` / `eth_call` 是 authority / settlement 专属**。
- **推理：** `ecrecover` 是**计算**，`eth_call` 是 **IO**。INV-2 禁止的从来只是链读（IO），不是本地密码学。若把 `ecrecover` 排除出 edge，热路径将无法验签 —— 直接违反 INV-3。

### 修正 #18：技术选型补全为三分法 + 无 CGO + KAT

- **v1.1 缺陷：** §12 库表只列了少数依赖，未说明"哪些必须用库、哪些必须自研"，容易被误读为**全自研**。
- **v1.1.1：** 新增 §12.1 **三分法**（① 密码学原语永远用库 / ② 编码优先用库 / ③ 业务结构必须自研）；§12.2 补全具体库（`x/crypto/sha3`、`decred secp256k1`、`modernc.org/sqlite` 等）；§12.4 新增**无 CGO 约束**；§12.6 把 KAT 从"缓解措施"升级为**硬性判据**。
- **推理：** 与 `MVP.md` §8 保持单一事实来源，避免两份文档漂移。

### 修正 #19：LGPL 表述软化

- **v1.1 表述：** "不拉 `apitypes`：**避免 LGPL-3.0 传染**"。
- **问题：** 表述过强。**LGPL 对"链接使用"通常可接受**，go-ethereum 被大量商业产品使用；只有**静态链接 + 闭源分发**才需认真对待。
- **v1.1.1：** 改用**纯 Go 替代**的主要理由改为 **可测 + 依赖干净 + 无 CGO**；LGPL 降为次要考虑。同时明确 `apitypes` 与自研 `hashStruct` **二选一都可行**，决定因素不是 license 而是 §12.6 的 KAT。

---

## 15. Permissionless 网络模型

### 15.1 目标模型

> **任何人都可以运行 RelayFirst 节点；任何 Agent 都可以自行选择、自托管或同时使用多个节点。事件是否有效取决于密码学签名与协议规则，而不是 RelayFirst 官方服务器。**

Nostr 的 relay 模型是：客户端对多个独立 relay 发布/订阅**已签名事件**，relay 负责接收、存储、分发，**不需要全局共识**。NIP-11 还规定 relay 可公开自己的能力、限制、认证/付费要求与费率，方便客户端按策略选择节点。RelayFirst 采纳这一网络模型（但**不**采纳 Nostr 的身份与签名方案——见 §14 保留项）。

### 15.2 最终网络模型

```
                           ┌────────────────────┐
                           │  Agent A Runtime   │
                           │ EVM identity/key   │
                           └───────┬────────────┘
                                   │
                 publish/subscribe to multiple relays
                                   │
      ┌────────────────────────────┼────────────────────────────┐
      ▼                            ▼                            ▼
┌───────────────┐          ┌───────────────┐          ┌───────────────┐
│ RF Node Alpha │          │ RF Node Beta  │          │ RF Node Gamma │
│ Community     │          │ User-operated │          │ Enterprise    │
│ Public Relay  │          │ Home/VPS      │          │ Private Relay │
└───────┬───────┘          └───────┬───────┘          └───────┬───────┘
        │                          │                          │
        └────────────────┬─────────┴──────────┬───────────────┘
                         ▼                    ▼
              ┌─────────────────┐   ┌────────────────────┐
              │ Agent B Runtime │   │ Indexer / Explorer │
              │ multi-relay set │   │ optional, replaceable│
              └─────────────────┘   └────────────────────┘
```

### 15.3 四个网络不变式（NET-1…NET-4）

```text
NET-1  没有唯一官方 Relay。
NET-2  没有全局 canonical database。
NET-3  没有要求 Relay 节点彼此达成共识。
NET-4  Agent 自己选择与切换 Relay set；多 relay 冗余是默认行为。
```

**与 §5.7（relay vs authority）的关系：** NET-1…NET-4 描述**投递层**的去中心化；§5.7 的 INV-10 描述**投递层不可信**。两者叠加得出本网络最重要的结论：

> **去中心化不等于可信。** 多 relay 冗余解决的是**可用性、抗审查、抗单点**，它**不**解决"某个 relay 撒谎"。后者只能由 §5.7 的 authority 平面（对链或对客户端持有的签名 checkpoint 验证）解决。

### 15.4 与"官方托管服务"的界限

| 维度 | Hosted SaaS（反模式） | RelayFirst permissionless（正确模型） |
|---|---|---|
| 官方节点角色 | 网络的唯一/默认入口 | 网络中的**一个** operator |
| 身份 | 注册在平台 | Agent 自持，可迁移 |
| 发现 | 官方目录即真相 | 多 indexer；**签名 Agent Card 才是真相** |
| 迁移 | 迁出即失联 | 换 relay set 即可保持可达 |
| 停机影响 | 全网中断 | 仅影响选择该节点的 agent |

> **用户不是"注册到 RelayFirst 平台"，而是"拥有一个可迁移的 Agent identity，并选择自己的 Relay 网络"。**

---

## 16. RelayFirst Node（RFN）规格

### 16.1 最小兼容节点（RFN Core）

**公开一个 RelayFirst Node Protocol，任何人可实现**——而不是只开源一份官方 Go server。一个最小兼容节点只需完成：

```text
1. 接受 WebSocket 连接。
2. 接收 RelayEnvelope 与 signature。
3. 验证 EIP-712 / ECDSA 签名（EOA ecrecover，纯本地）。
4. 根据订阅与 recipient route 转发事件。
5. 提供 eventId 去重。
6. 提供最小 Agent Inbox 与 relay information document（RFN-04）。
7. 返回 publish acknowledgement。
```

**最小节点明确不要求（v1.1 修正 #12）：**

```text
ERC-1271 验签（可选扩展，非核心；见 §16.2）
OIDC / 企业 verifier
全链索引
官方数据库 / NATS / Postgres / Redis
x402 / USDC / Escrow / Merkle settlement
Token / Mining / 智能合约
```

因此用户可用**低成本 VPS，甚至个人设备**运行一个 Agent inbox relay。存储用 SQLite 即可（§12.2 Minimal 级）。

### 16.2 节点能力分层

```text
RFN Core（最小兼容）
├── WebSocket pub/sub
├── HTTP relay information document（RFN-04）
├── EIP-712 envelope verification
├── EOA ecrecover validation
├── Event routing + subscriptions
├── Inbox storage（SQLite 即可）
├── eventId deduplication
└── Relay ACK

RFN Optional Modules
├── ERC-1271 verifier（需链读；仅自愿启用）
├── OIDC / enterprise verifier
├── Encrypted artifact proxy
├── NATS / durable queue backend
├── Full-text task index
├── Agent Card / capability index
├── Multi-region replication
├── x402 paid relay access
├── Spam protection / adaptive PoW
├── Settlement watcher
├── Merkle clearing operator
└── Enterprise OIDC / compliance controls
```

这形成健康的节点生态：**轻节点门槛低，专业节点提供性能、索引、存储、SLA、隐私、归档或结算服务。**

### 16.3 节点类型

| 节点类型 | 谁运行 | 用途 | 是否公开 |
|---|---|---|---:|
| **Public Relay** | 社区、开发者、基金会、第三方 operator | 公共 discovery、公开 capability、开放 A2A 请求 | 是 |
| **Personal Relay** | 普通用户、Agent owner | 自己 Agent 的 inbox、私有 history、备份 | 可选 |
| **Home Node** | 高级用户、家庭服务器、小团队 | 自托管 Agent、私有任务与 artifacts | 否/半公开 |
| **AgentLand Relay** | 你或社区 operator | AgentLand 世界内 Agent coordination | 可选 |
| **Enterprise Relay** | 公司、DAO、基金会 | 内部 Agent、合规、数据驻留 | 否 |
| **Specialist Relay** | 行业服务商 | Trading、research、coding、gaming Agent discovery | 可选 |
| **Archive Relay** | 社区或 indexer | 历史 receipt、公共 capability、审计索引 | 是 |
| **Settlement Watcher** | 独立 operator | 验证 receipt、监控 root、提交 dispute evidence | 可选 |

### 16.4 用户跑节点的最简体验

目标：**像运行 Nostr relay 一样运行 RelayFirst 节点。**

```bash
docker run -d \
  --name relayfirst-node \
  -p 443:8080 \
  -v ./relayfirst-data:/data \
  relayfirst/node:latest \
  relay \
  --public-url wss://relay.myagent.xyz \
  --mode personal \
  --max-connections 500 \
  --storage sqlite
```

然后该用户的 Agent Card 声明自己的 Relay Set：

```json
{
  "schema": "relayfirst.agent-card.v1",
  "agentId": "agent:eip155:8453:0x7F4d...A8B5",
  "relayEndpoints": [
    { "url": "wss://relay.myagent.xyz", "role": "inbox", "priority": 1 },
    { "url": "wss://relayfirst.community", "role": "backup", "priority": 10 }
  ],
  "signature": "0x..."
}
```

外部 Agent 发送 Task 时：

```text
1. 查询目标 Agent Card（签名）。
2. 得到用户自建 relay 地址。
3. 同时向 inbox relay 和备份 relay 发布。
4. 收到节点签发的 delivery acknowledgement。
5. 目标 Agent 从自己的 relay set 接收 event。
6. 收件方使用 eventId 去重、验签、执行。
```

**用户不需要依赖 RelayFirst 官方节点，才能继续运行其 Agent。** 这是本节的验收判据。

---

## 17. 节点发现

"每个人可以运行节点"**不等于**"其他人自动知道如何找到它"。需要三层 discovery，且**必须避免单一官方目录**。

### 17.1 第一层：Agent Card（最重要）

每个 Agent **自己签署并发布**当前 Relay Set。

```json
{
  "agentId": "agent:eip155:8453:0x...",
  "relayEndpoints": [
    {
      "url": "wss://relay.alice.xyz",
      "role": "inbox",
      "priority": 1,
      "regions": ["ap-southeast-1"]
    },
    {
      "url": "wss://rf-public-02.xyz",
      "role": "backup",
      "priority": 20
    }
  ],
  "updatedAt": 1791015800,
  "expiresAt": 1793607800,
  "signature": "0x..."
}
```

> **它的有效性来自 Agent 的 EIP-712 signature，不来自任何目录服务。**

### 17.2 第二层：多个可替换的 Indexer

任意人可以运行 Indexer：

```text
Agent Card Indexer A
Agent Card Indexer B
AgentLand Discovery Indexer
Enterprise-only Indexer
Community Relay Directory
```

客户端可以配置**多个** indexer，或自己扫描 relay。**Indexer 只帮助"找得到"，不定义"谁是真的"。**

```text
Discovery Indexer = convenience
EIP-712 Agent Card signature = truth
```

### 17.3 第三层：可选 EVM Anchor

为高价值 Agent，可将最新 Agent Card 的 hash / URI 锚定到 EVM registry：

```solidity
mapping(address agent => bytes32 cardHash) public latestAgentCardHash;
mapping(address agent => string  cardURI)  public latestAgentCardURI;
```

**这不是每次 Task 都上链。** 仅用于：

- 公开的 canonical bootstrap record。
- 当 offchain indexer 都不可用时的 **fallback discovery**。
- 为企业、DAO、市场/交易 Agent 提供更强的 **identity continuity**。
- 在 ownership 变更、key rotation、relay set 修改时留下**审计锚点**。

---

## 18. 节点经济与反滥用

### 18.1 节点信息文档（RFN-04）

节点在：

```text
https://relay.example.com/.well-known/relayfirst
```

提供（Nostr NIP-11 的类比，**但编号是 RFN-04**，见 §14.2 修正 #14）：

```json
{
  "protocol": "relayfirst/1.0",
  "name": "Alice KL Public Relay",
  "description": "Public relay for Southeast Asia agent coordination",
  "operator": "0xOperatorAddress...",
  "software": "relayfirst-go",
  "version": "1.0.0",
  "endpoints": {
    "websocket": "wss://relay.example.com",
    "http": "https://relay.example.com/v1",
    "health": "https://relay.example.com/health"
  },
  "supportedFeatures": [
    "rf-core",
    "eip712",
    "eoa-verify",
    "erc1271-verify",
    "encrypted-payload",
    "durable-inbox",
    "capability-index"
  ],
  "limits": {
    "maxMessageBytes": 65536,
    "maxConnectionsPerIp": 50,
    "maxSubscriptions": 100,
    "retentionDays": 30,
    "maxArtifactBytes": 0
  },
  "policies": {
    "authRequired": false,
    "registrationRequired": false,
    "publicRead": true,
    "publicWrite": true,
    "minProofOfWork": 0,
    "contentPolicy": "https://relay.example.com/policy"
  },
  "pricing": {
    "model": "free",
    "paymentMethods": ["x402"]
  },
  "updatedAt": 1791015800,
  "signature": "0x..."
}
```

这是**高价值设计点**：它让客户端按条件选 relay——

```text
- 是否支持 EIP-712？        - 最大消息多大？
- 是否支持 ERC-1271？       - 是否收费？
- 是否支持 encrypted payload？- 是否有 retention？
- 是否在新加坡/欧洲/美国？   - 是否允许公共写入？
- 是否要求 proof-of-work？
```

> **注意：`erc1271-verify` 是可选能力声明。** 一个只声明 `eoa-verify` 的最小节点仍然是**完全兼容**的节点（§16.1）。

### 18.2 节点经济模型

**最容易掉进的坑是"发 token 挖矿"。** 节点应有**真实业务收入路径**，而不是 Day 1 靠排放补贴。

| 节点服务 | 谁付钱 | 计费方式 | 是否核心 |
|---|---|---|---:|
| Public basic relay | 免费/社区 | 免费、捐赠、赞助 | 是 |
| Personal inbox relay | Agent owner | VPS / 自托管 | 是 |
| Durable storage | Agent owner / App | 按 storage 与 retention | 是 |
| 高优先级投递 | Sender / App | 按 event 或带宽配额 | 可选 |
| Capability indexing | 开发者、marketplace | API usage / subscription | 可选 |
| Artifact proxy/storage | Agent owner / team | storage + egress | 可选 |
| Enterprise private relay | 企业 | 月费 / SLA | 后期 |
| Settlement watcher | 协议/用户 | fee share / bounty | 后期 |
| Clearing operator | 付费 Task 网络 | settlement fee | 后期 |

**明确 non-goal：不做 token 挖矿、不做质押奖励网络、不做全球节点排放补贴。**

### 18.3 x402 的适当角色

x402 可作为**节点收费适配器**，而**不是** RelayFirst 网络存在的理由：

```text
free relay:
  - basic limits
  - lower retention
  - lower priority

paid relay:
  - durable inbox
  - higher connection cap
  - high-priority delivery
  - artifact storage
  - indexing API
  - SLA
```

RFN-04 信息文档声明节点收费；x402 让 Agent 在请求高阶 relay service 时**自动支付**。这与 §14 修正 #9（x402 是 Payment Adapter，逐字包裹，不出现于第一句）一致。

### 18.4 防 spam 与女巫

**完全 permissionless 的代价是：无法靠"官方注册审核"防垃圾。** 正确方式是把反 spam 交给**每个节点 operator 自己决定**：

| 防护机制 | 使用位置 | 适用场景 |
|---|---|---|
| Rate limit | 每个 relay | 所有公开节点 |
| IP / ASN 限制 | Relay edge | 明显攻击期 |
| Session / Agent identity | 私有任务 | 有明确 recipient 的 A2A |
| Reputation allowlist | 高价值节点 | 专业/企业 relay |
| x402 fee | 高优先级写入/存储 | 商业 relay |
| Hashcash / adaptive PoW | 匿名公开广播 | 公共 discovery/topic |
| Stake / bond | 可证明的高价值 operator commitment | 后期可选 |
| Content moderation policy | Public relay | 公共网络合规风险 |

> **节点运营者可自由选择 relay policy；协议只规定"如何声明与验证规则"，不强制整个网络采用单一审查或单一收费规则。**

---

## 19. 标准族 RFN-01…RFN-12

| 标准名 | 作用 | 必须程度 |
|---|---|---:|
| **RFN-01 Core Wire Protocol** | WebSocket publish、subscribe、ack、close、error | 必须 |
| **RFN-02 RelayEnvelope** | EIP-712 event structure、signature、nonce、expiry | 必须 |
| **RFN-03 Agent Card** | Agent identity、capability、relay set、key rotation | 必须 |
| **RFN-04 Relay Information Document** | 节点能力、限制、收费、政策、endpoint | 必须 |
| **RFN-05 Multi-Relay Delivery** | publish quorum、ack、dedup、retry、fallback | 必须 |
| **RFN-06 Task & Session Lifecycle** | Task 状态机、sequence、receipt | 必须 |
| **RFN-07 Encryption** | X25519 payload/artifact encryption | 强烈建议 |
| **RFN-08 Artifact References** | content hash、URI、encryption metadata、availability | 强烈建议 |
| **RFN-09 Delegated Authority** | EIP-712 Session Delegation、revoke、scope | 强烈建议 |
| **RFN-10 Payment Adapter** | x402 / USDC payment intent | 可选 |
| **RFN-11 Settlement Adapter** | escrow、receipt root、claim/dispute | 可选 |
| **RFN-12 Relay Reputation** | health、uptime、signed SLA / attestations | 后期 |

**这组模块的好处：** 普通用户可只运行 `RFN-01 + RFN-02 + RFN-03 + RFN-04` 的**轻节点**；复杂商业节点再按需要支持 storage、indexing、encryption、payment 和 settlement。

**依赖方向（避免循环）：**

```text
RFN-01 (wire) ← RFN-02 (envelope) ← RFN-03 (card) ← RFN-04 (info doc)
                     ↑
RFN-05 (multi-relay) ┘
RFN-06 (lifecycle) ← RFN-09 (delegation)
RFN-07/08 (crypto/artifact) 独立
RFN-10/11/12 位于经济层，可选
```

---

## 20. 完全去中心化的真实成本

若要做到"完全像 Nostr"，**必须接受**下列后果。它们不是缺点，而是完全去中心化的真实成本；**协议设计必须把这些情况当成正常路径，而不是异常。**

| 现实 | 含义 | 协议对应 |
|---|---|---|
| 无法保证任何节点一直在线 | 客户端必须多 relay 与重试 | RFN-05 multi-relay + fallback |
| 无法保证任何 relay 保存所有历史 | Agent / app 必须自己备份重要 receipts 与 artifacts | RFN-08 + 客户端备份 |
| **无法保证全网消息全序** | Task 依赖 per-task `sequence` 与 hash chain，而非全局顺序 | §4.2、修正 #11 |
| 无法自动阻止恶意节点 | 客户端要有 relay reputation、health score、fallback set | RFN-12 |
| 无法强制所有 relay 互相同步 | 发送端/客户端承担 multi-relay publish | RFN-05 |
| 无法保证私有数据不泄露 metadata | 必须 E2EE、最小化 routing metadata、可用 private relay | §6 |
| 无法因一般掉线直接 slash relay | 只有明确签名承诺或经济合约违约才能惩罚 | §8 争议规则 |
| 无法由官方定义唯一 discovery 真相 | discovery 是多 indexer；签名 Agent Card 才是真相 | §17 |
| 无法保证高 QoS 免费 | 高 SLA relay 会有商业化或支付层 | §18.2 |

**三条必须同时成立的设计结论：**

1. **确定性来自签名数据，不来自 relay**（否则 relay 成为隐式权威，违背 NET-1/NET-3）。
2. **可用性来自多 relay，可信度来自签名与 checkpoint**（两者不可互相替代，见 §15.3）。
3. **反滥用来自节点自治策略，不来自全局规则**（否则 permissionless 名不副实，见 §18.4）。

---

## 附录 A：AgentLand 集成点（已核实）

| 集成点 | 位置 | 与 RelayFirst 的关系 |
|---|---|---|
| `agent_defs.on_chain_token_id text` | `supabase/migrations/0001_init.sql` | 预留给 Web3 agent NFT —— RelayFirst agentId 的锚点 |
| `agent_defs.tenant_id` / `world_id` / `owner_user_id` | 同上 | 租户隔离与 owner 映射 |
| `wallet_bindings`（`UNIQUE (user_id, chain)`） | 同上 | Owner 钱包绑定 → RelayFirst agentId |
| `wallet-binding.ts`（EIP-191 `verifyMessage`，`NONCE_TTL_MS=10min`，`SUPPORTED_CHAINS=ethereum/sepolia/polygon/base`） | `apps/api/src/infra/wallet-binding.ts` | Owner 绑定；RelayFirst delegation 的 EIP-712 与之衔接 |
| `outbox_events`（`UNIQUE (world_id, topic, idempotency_key)`） | `supabase/migrations/0001_init.sql` | at-least-once + 幂等键的同模式延续 |
| `world_events` append-only（trigger 禁止 UPDATE/DELETE） | 同上 | 审计级持久化的同模式延续 |
| AgentLand `ARCHITECTURE.md` | 项目根 | 语气/结构参考（分层、表格、约束列表） |

---

## 附录 B：不变式速查

```
INV-1   签名 offchain 事件不得要求链知识
INV-2   Edge relay 内不得出现 ethclient / 任何链读
INV-3   热路径签名只允许 ecrecover（纯本地计算，edge 必需；ecrecover 是计算，eth_call 才是 IO）
INV-4   撤销 = nonce 递增；损失上界 = maxValuePerAction × cacheTtl
INV-5   价值额度检查必须原子（Redis Lua，键 (agent,policy)）
INV-6   结算必须 commit signed receipt-set，而非仅派生 tree
INV-7   抗双领 bitmap 必须全局（beneficiary × receipt）
INV-8   绝不因未投递消息自动罚没
INV-9   Ephemeral 信号是传输层，非协议对象
INV-10  Relay = delivery（不可信）；Authority = 可验证

NET-1   没有唯一官方 Relay
NET-2   没有全局 canonical database
NET-3   没有要求 Relay 节点彼此达成共识
NET-4   Agent 自己选择与切换 Relay set；多 relay 冗余是默认行为

NET-5   relaySequence 是单节点局部序；确定性来自签名数据（sequence + hash chain）
NET-6   最小节点只要求 EOA ecrecover；ERC-1271 / OIDC 是可选扩展
NET-7   RFN Core 不依赖 NATS；存储分级 Minimal(SQLite) → Standard → Scaled
NET-8   不做 token 挖矿、不做质押奖励网络
```

---

*文档版本：v1 · 状态：Canonical · 项目：RelayFirst（greenfield）*