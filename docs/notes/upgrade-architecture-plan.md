# 笔记：MVP 2.0 → 全架构的升级方案（可升级性与库采用）

- 日期：2026-10-04
- 状态：**提案**（不改代码；部分结论需要人工批准 `MVP.md` 变更，见 §8）
- 相关：`MVP.md` §4/§5.0/§8/§12、`ARCHITECTURE.md` §3/§4/§5/§11/§12/§15/§19、
  `internal/receipt/receipt.go`、`internal/protocol/envelope.go`、`internal/eip712/`、
  `docs/decisions/ADR-0001-eip712-in-house-thin-layer.md`、`docs/notes/epoch-anchoring.md`

> **这份文档只做三件事：**
> ① 定义"永不硬分叉"的版本化设计（§2）；
> ② 排出 MVP 2.0 → 全架构的升级顺序（§3）；
> ③ 给出库采用表与风险登记册（§4–§6）。
>
> **它不实现任何东西。** 需要改 `MVP.md` 的地方在 §8 单独列出，等待人工裁决。

---

## 0. 结论摘要（TL;DR）

1. **能不能升级？能，但前提是先加一层版本化地基。** 现在直接开写 S9，第一次加字段就会
   把已上线的节点永久分叉掉 —— 因为当前回执是**精确匹配 + 拒绝未知字段**（`receipt.go:239`、
   `Unmarshal` 的 `DisallowUnknownFields`）。

2. **一个被误判的前提需要纠正：EIP-712 typehash 不随"载荷加字段"改变。** 当前
   `RelayReceipt` 的 typehash 只覆盖 `{agentId, epoch, payloadHash}`，载荷是**作为一个 blob
   被哈希**的。真正的分叉来自**旧验证器无法逐字重建新载荷**（见 §1）。这反而让修复更容易：
   把载荷改成**逐字签名**即可。

3. **不要自研版本协商。** 用 A2A SDK 已有的 `A2A-Version` / `A2A-Extensions` /
   `AgentInterface`（`a2a-go/v2` 已提供 `SvcParamVersion`、`ErrVersionNotSupported`）。

4. **传输层：现在就把 WebSocket 作为新的绑定加进来，而不是"先 HTTP、以后再重写"。**
   理由见 §3.3。注意这是一次**范围变更**，必须先改 `MVP.md`（§12 目前明确写"不做 WebSocket"）。

5. **升级性应当成为 A9**，但必须配一条 CI 门禁，否则只是口号（§5）。

6. **必须在 S9 第一行代码之前定稿的 8 件事**见 §7。其中 **S9-7（把 `task.verification`
   放进 schema）按现在的写法就是一个分叉点** —— 它落在签名载荷内。

---

## 1. 先纠正一个前提：typehash 不随载荷字段变

问题描述里说：

> "Fork point 3 — EIP-712 typehash 由字段列表推导。若签名载荷新增字段，typehash 变化，
> 所有旧签名失效。"

**在这份代码里，这个说法不成立**，而正确理解它决定了修复方案。

看 `internal/receipt/receipt.go` 的 `TypedData()`：

```go
types := eip712.Types{
    "RelayReceipt": {
        {Name: "agentId", Type: "string"},
        {Name: "epoch", Type: "uint256"},
        {Name: "payloadHash", Type: "bytes32"},
    },
}
```

而 `payloadHash = keccak256(canonicalJSON(signedPayload))`。

于是：

| 变更 | typehash 变吗 | 旧签名还能验吗 | 真正的破坏点 |
|---|---|---|---|
| 在 `signedPayload`（即 `task`/`work`/`result`/`anchors`）加字段 | ❌ **不变** | ✅ 签名覆盖新字节，本应有效 | **旧验证器无法逐字重建新载荷** → 它算出的 `payloadHash` 与签名者不同 |
| 改 `RelayReceipt` 结构本身（如把 `payloadHash` 换成 `payloadBytes`） | ✅ **变** | ❌ 全部失效 | 这是真正的 major |
| 改 domain（`name` / `version`） | 不变（domain 不进 typehash） | ❌ 全部失效（digest 变） | 这也是 major |

**所以真正的分叉机制不是 typehash，而是"验证器必须逐字重建签名者签过的字节"。**
当前实现里，验证器拿到的是**结构化字段**，再用自己的 `canonicalJSON` 重新序列化。只要新旧
两版的字段集合不同，重建结果就不同。

**这带来一个关键结论：** 只要把签名载荷从"重新规范化的结构"改成"**逐字签名的 blob**"，
那么"载荷加字段"就从 major 降级为 minor。这个改造**便宜、可测、且越早做越好**（§2.4）。

> ⚠️ 但请注意：`Receipt.Unmarshal` 用 `DisallowUnknownFields`，所以**即使签名能验过**，
> 旧二进制也会在解码阶段直接报错。向后兼容需要同时放宽这一处。

---

## 2. 反分叉版本化设计

### 2.1 三条版本轴（必须分开，不要混为一谈）

现在文档里"版本"这个词被用在三个不同的地方。混在一起就会做出错误的协商。

| 轴 | 谁定义 | 存在哪里 | 协商方式 | 谁来管 |
|---|---|---|---|---|
| **① A2A wire 版本** | 上游 A2A 规范 | HTTP service param `A2A-Version` / `AgentInterface` | **带外协商**（不签名） | 跟随上游 SDK |
| **② 回执 schema 版本** | RelayFirst | 回执的 `schema` 字段（**在签名覆盖范围之外**） | **自描述**，无需协商 | 我们 |
| **③ EIP-712 domain 版本** | RelayFirst | domain 的 `version` 字段 | **自描述 + 多版本验证** | 我们 |

**核心规则（这是整个设计的支点）：**

> **协商发生在签名字节之外；签名字节必须自描述。**
>
> 一个验证者验证一份**历史回执**时，**永远不需要任何带外信息**。
> 它读回执自己声明的 `schema` 与 domain 版本，套用对应的验证规则即可。

这一条直接回答了"如何让自托管公共节点永不分叉"：**分叉的根源是"验证者需要知道对端是谁、
在什么版本"，只要验证只依赖产物本身，就永远不会分叉。**

> 注意：`ARCHITECTURE.md` §4.2 的 `RelayEnvelope` 里有一个 `protocolVersion` 字段，
> 且**它在签名 struct 内**（`§3.8` 第 1 步还要求校验它等于 `"a2a-relay/1.0"`）。
> **这是设计缺陷** —— 它让"版本协商"变成了"签名校验的一部分"，于是升版本 = 废签名。
> 正确做法是：**协商信息放在信封外**（`A2A-Version` 头 / service param），信封内只留
> 自描述的 schema 标识，且校验规则是"属于我支持集合"，而非"等于某个常量"。

### 2.2 破坏性 vs 增量：规则

**定义（按"是否需要旧验证器改变行为"划分）：**

**增量（additive）—— 不需要新 major：**

1. 在**签名载荷之外**新增字段（如 `verification`、节点签名、索引元数据、传输层字段）。
2. 新增一个**之前未使用的**枚举值，且旧验证器遇到它时能安全地"不处理"（fail closed，
   但报"unsupported"而非"invalid"）。
3. 新增端点 / 新增 A2A 扩展（`A2A-Extensions`）。
4. 新增一个 A2A `AgentInterface`（如加 WebSocket 绑定）。
5. 在**逐字签名 blob**（§2.4）的 JSON 里新增字段 —— **前提是 §2.4 已落地**。

**破坏性（breaking）—— 必须新 major，且旧 major 的验证器永久保留：**

1. 改**已签名字段的语义**（不是名字，是含义）。
2. 从签名载荷中**删除或重命名**字段。
3. 改 EIP-712 **domain** 的任一字段（含 `version`）。
4. 改**规范化编码**（canonical JSON 规则、hash 前缀、地址大小写）。
5. 改 `epoch` 的**起点**（`BLK-4`）—— 因为 epoch 号在签名载荷内。
6. 改**签名方案**（如引入 ERC-1271）在热路径上的处理方式。

**一句话规则：**

> **凡改变"一份旧产物在新验证器下是否仍判定为有效"的，是增量；
> 凡改变"一份旧产物在旧验证器下是否仍判定为有效"的，是破坏性。**
>
> 前者由我们控制；**后者一旦发生就不可逆** —— 这正是要竭力避免的那一类。

### 2.3 回执 schema 向后兼容

**现状：** `receipt.go:239` `if r.Schema != Schema { reject }` —— 精确匹配。
`Unmarshal` 用 `DisallowUnknownFields` —— 未知字段直接报错。

**目标语法（增量扩展，不破坏现有字符串）：**

```
relayfirst.receipt.v<major>[.<minor>]

v1     ≡ v1.0        （兼容现有已发布字符串，必须永久接受）
v1.1                 增量
v2.0                 破坏性
```

**解析规则：**

- 缺省 minor = 0。
- **major 必须精确匹配到"我支持的集合"**；不在集合里 → 结果 = `unsupported`（不是 `invalid`）。
  这个区分很重要：`unsupported` 表示"我不能判断"，`invalid` 表示"我判断它错"。
  把它俩混在一起，会让"我太旧"看起来像"你造假"。
- **minor 大于我支持的最大 minor 时：必须接受并忽略未知字段**（§2.4 落地后这才是安全的）。

**未知字段容忍：**

- 把 `DisallowUnknownFields` 从**回执解码路径**移除，改为在**校验阶段显式检查**：
  - 未知字段若落在**签名载荷之外** → 直接忽略。
  - 未知字段若落在**签名载荷之内** → 见 §2.4（逐字 blob 让它无害）。
- 保留一个**严格模式**（`--strict`）用于红队与 KAT：严格模式下未知字段报错，
  生产模式容忍。这样"容忍"是可测的，而不是靠嘴说。

**⚠️ 不可回避的事实：** 旧二进制无法被追溯地改成"容忍"。所以**第一次升级本身就是分叉**，
除非我们在发布"容忍版"时**保留对 `relayfirst.receipt.v1` 的精确接受**，把容忍逻辑作为
**新增能力**加进去。也就是说：**这套设计必须在 S9 的第一批改动里落地，作为后续一切升级的前提。**

### 2.4 签名载荷：从"重新规范化"到"逐字 blob"（最重要的单项改动）

**问题：** 当前签名者签的是 `canonicalJSON(signedPayload)`，验证者**重新序列化**同一结构。
只要两端的字段集合或编码规则有任何差异，重建的字节就不同。

**方案：** 让回执**携带被签名的载荷字节本身**，验证者只做哈希，不做重建。

```jsonc
{
  "schema": "relayfirst.receipt.v2.0",
  "receiptId": "0x...",
  "agentId": "agent:eip155:8453:0x...",
  "epoch": 42,

  // 逐字签名：这就是签名者签过的字节（canonical JSON 字符串），
  // 验证者只对它做 keccak256，绝不重新序列化。
  "payload": "{\"agentId\":\"...\",\"epoch\":42,\"task\":{...},...}",
  "payloadHash": "0x...",   // = keccak256(utf8(payload))

  // 以下是载荷之外的、不被签名的附加信息（可自由增量）
  "verification": { ... },
  "signature": "0x..."
}
```

EIP-712 struct **保持不变**（`RelayReceipt {agentId, epoch, payloadHash}`），
只是 `payloadHash` 的来源从"重建结构"变为"哈希逐字字节"。

**收益：**

| 变更 | 改造前 | 改造后 |
|---|---|---|
| 在载荷 JSON 里加字段 | 旧验证器重建失败 → 分叉 | 旧验证器逐字哈希 → 仍有效 |
| 两端 canonical JSON 实现漂移 | 静默分叉 | 无影响（不再重建） |
| 加字段是否需要新 major | 需要 | **不需要** |

**代价与诚实边界：**

- 载荷变成**不透明字符串**，可读性下降（但 `receipts --export` 可以美化展示，不影响签名）。
- 需要一个"canonical JSON 只在**签名时**用一次"的约定，且要**冻结**：一旦冻结，
  canonical JSON 的实现只影响**新签名**，不再影响**旧验证**。
- **仍需 KAT**（A4）：跨语言验证的是"同一 payload 字节 → 同一 payloadHash → 同一 digest"，
  这比现在更简单、更稳。

**结论：这是"昂贵到不可逆"的决策里最值得现在做的一个。** 它把一整类未来的分叉消掉了。

### 2.5 EIP-712 多版本验证（旧回执永久可验）

**版本化位置：domain 的 `version` 字段**（不进 typehash，但进 digest）。

```
domain = { name: "RelayFirst", version: "<V>" }
```

**验证算法（多版本 try-all，带快路径）：**

```
1. 若回执声明了 domainVersion → 只试这一个（O(1)）。
2. 否则 → 依次尝试"所有已发布且未被删除的 domain version"，
   任一版本恢复出的地址 == 声明的 agentId → 有效。
3. 全部失败 → invalid。
```

`ecrecover` 是纯本地计算（约几十微秒），try-all 的成本可忽略。

**"永久可验"的机制保障：**

- 维护一张**只增不减**的表：`schema major → domain version → 载荷编码规则`。
- 每一个历史版本对应一份**冻结的验证实现**（可以是同一函数 + 一张参数表）。
- **代码里永不删除历史版本分支。** 这条要写进 A9（§5）。
- 每个历史版本对应一组**冻结的 golden 回执语料**（§2.8）。

**为什么用 domain 而不是 typehash 版本化：** 因为 `RelayReceipt` 的结构是固定的
（§1），不需要为每个版本造一个新 struct；domain version 已经足够区分"编码/语义代际"，
且改动它本来就是破坏性的（符合 §2.2 的定义）。

### 2.6 协商：采用 A2A 的机制，不自研

**决定：不自研任何版本协商原语。** 理由：

- `a2a-go/v2`（Apache-2.0，官方，Linux Foundation）**已经提供**：
  `SvcParamVersion = "A2A-Version"`、`SvcParamExtensions = "A2A-Extensions"`、
  `ErrVersionNotSupported`、以及 `AgentInterface`（每个接口声明 target URL + transport）。
- 自研协商 = 多一套要维护的语义 + 与上游不兼容的风险。
- `AgentInterface` 恰好解决了"同一 agent 同时暴露 HTTP 与 WebSocket 绑定"的问题（§3.3）。

**分层映射（把三条轴对齐）：**

| 场景 | 用什么 |
|---|---|
| 两个节点/A2A agent 握手 | `A2A-Version` 头 + `AgentInterface` 列表 |
| 节点声明自己支持哪些 RelayFirst 写入版本 | `/.well-known/relayfirst` 的 `supportedSchemas` 字段（RFN-04 扩展） |
| 验证一份回执 | **不用协商** —— 读回执自声明的 `schema` + domain version |

**冲突处理：** 双方支持的 A2A 版本无交集 → 用 SDK 的 `ErrVersionNotSupported`
**显式失败**，不得静默降级到某个默认版本。

**要新增的映射表（写进文档，防止两条轴漂移）：**

```
A2A-Version  ↔  支持的 RelayFirst schema majors
（例：A2A 1.0 → relayfirst.receipt v1, v2）
```

### 2.7 弃用政策（带数字）

**最重要的区分：**

> **验证（verification）永不弃用；只有写入/中继（minting/relaying）才弃用。**

历史回执必须**永远**可验（验收判据 ② 要求"关掉所有服务器仍能验证"）。
弃用一个 major 只意味着"我不再接受用这个版本**新签**的回执"，不意味着"我不再承认旧回执"。

| 项 | 数字 | 说明 |
|---|---|---|
| 验证支持 | **永久** | 所有历史 major 的验证器永不删除 |
| 写入支持（节点） | **最新 major + 上一个 major** | 更旧的写入请求 → `unsupported` |
| 写入弃用前置期 | **≥ 12 个月** | 新 major 发布后，旧 major 仍可写满 12 个月 |
| 日落公告期 | **≥ 6 个月** | 停止写入前至少提前 6 个月公告（写进 RFN-04 信息文档） |
| 增量（minor）弃用 | **永不** | 未知字段容忍是永久的 |
| 节点最低要求 | 支持**最新**写入 major | 与 `ARCHITECTURE.md` §15 的 permissionless 一致 |

**数字的来源（诚实说明）：** 这些数字是**工程判断，不是定理**。12/6 个月的选取原则是：
让"一个很久没升级的自托管节点"有足够时间跟上，同时不让协议被旧版本永久拖住。
它们应该被写进 `MVP.md`/`ARCHITECTURE.md` 成为可引用的常量，而不是散落在笔记里。

### 2.8 CI 兼容门禁（A4 的扩展）

现有 CI 有 10 道门禁。**新增 4 道兼容门禁：**

| # | 门禁 | 检查什么 | 为什么必要 |
|---|---|---|---|
| **G1** | **冻结语料** `testdata/receipts/v<major>/*.json` | 每个历史 major 至少一份**真实签名**的合法回执，**只增不删**；测试断言每份仍能通过对应版本的验证 | 防止"某次重构悄悄让旧回执验不过" |
| **G2** | **跨版本矩阵** | 对每个 `(schema major, domain version)` 组合，断言 verify 结果符合预期；断言"版本 X 的签名不会在版本 Y 的规则下验过"（防签名混淆） | 防跨版本重放/混淆 |
| **G3** | **未知字段注入** | 向当前版本回执注入未知字段：载荷外 → 必须接受；载荷内 → 按 §2.4 必须接受；`--strict` 模式 → 必须拒绝 | 让"容忍"可测 |
| **G4** | **协商无交集** | 两个支持集合无交集的节点 → 必须显式失败，且失败信息含双方版本 | 防止静默降级 |

**与现有门禁的关系：** G1–G4 是**新增**，不替代任何现有门禁。KAT（A4）继续存在，
且**每个历史版本都要有自己的 KAT 向量**。

### 2.9 现有代码里已确认的分叉点（含一个 S9 任务）

| 分叉点 | 位置 | 现状 | 修复 |
|---|---|---|---|
| **F1 schema 精确匹配** | `receipt.go:239` | `!= Schema` 即拒 | §2.3 语法 + `unsupported`/`invalid` 区分 |
| **F2 未知字段硬拒** | `receipt.go` `Unmarshal` | `DisallowUnknownFields` | §2.3 生产容忍 / 严格模式可选 |
| **F3 验证器重建载荷** | `SignedPayload()` + `canonicalJSON` | 两端各自序列化 | §2.4 逐字 blob |
| **F4 envelope 无版本** | `internal/protocol/envelope.go` | `Envelope{ID,AgentID,Kind,Payload}` | 协商放信封外（§2.6）；**不要**在信封内加版本常量 |
| **F5 domain 无版本策略** | `TypedData()` `Version:"1"` | 单一版本 | §2.5 多版本 try-all |
| **F6 ⚠️ S9-7 本身** | `TASKS.md` S9-7 | 把 `task.verification` 放进 schema —— **它在签名载荷内** | **必须先做 §2.4**，否则 S9-7 就是一次分叉 |

> **F6 是这份笔记里最具体的发现：** 按现在的任务分解，S9-7 会在签名载荷里新增
> `task.verification`。若先做 S9-7 再做 §2.4，则所有已上线的 v1 节点会**永久拒绝**新回执。
> **顺序必须是：§2 地基 → S9-7。**

---

## 3. MVP 2.0 → 全架构 升级路径

### 3.1 缺口（复核你的估算）

你的估算（~70% 增量 / ~30% 重写）**基本正确**，我做一点修正：

| 块 | 性质 | 说明 |
|---|---|---|
| A2A 协议层（S9） | **增量** | 新包 + 新字段；但 **S9-7 依赖 §2.4** |
| 节点能力（S10） | **增量** | 新端点；旧 4 个端点保留 |
| SBT 积分（S11） | **增量** | 新合约；复用 `internal/merkle` |
| 结算 + MCP（S12） | **增量** | 新合约 + 新 server |
| E2EE + 密钥层级（S13） | **增量** | 新字段 + 新包；与 Agent Card 交互 |
| **传输层 HTTP → WebSocket** | **重写（若现在不做抽象）** | 见 §3.3 |
| **消息排序（per-actor sequence + hash chain）** | **重写（若信封形状定错）** | §4.2 的两级序 |
| **authority 平面（delegation/nonce）** | **重写** | Phase 2，暂时不做 |
| **验证管线** | **重写（改为多版本）** | §2.5 |

**修正点：** 你把"传输 HTTP→WebSocket"归为 30% 的重写。**如果现在就把传输抽象出来、
并把 WebSocket 作为新绑定加上，这一块会从"重写"变成"增量"。** 这就是 §3.3 的建议。

### 3.2 正确的顺序（永不画地为牢）

```
┌─ 第 0 步：版本化地基（必须在任何 S9 代码之前）────────────────────┐
│  §2.3 schema 语法 + unsupported/invalid 区分                      │
│  §2.4 逐字签名 blob（冻结 canonical JSON 只在签名时使用）          │
│  §2.5 domain 多版本验证（try-all）                                │
│  §2.6 采用 A2A-Version / AgentInterface，不自研                    │
│  §2.7 弃用政策数字写进文档                                         │
│  §2.8 G1–G4 兼容门禁                                              │
│  → 冻结语料库建立（v1 必须先入库）                                 │
└───────────────────────────────────────────────────────────────────┘
                              ↓
┌─ 第 1 步：冻结消息契约（便宜，但信封形状不可逆）──────────────────┐
│  per-actor `sequence` + `previousEventHash`（ARCHITECTURE §4.2）   │
│  现在可以恒为零/空，但**字段位置现在就定**                          │
│  两级排序规则写进文档（relaySequence 是 local，不是 global）        │
└───────────────────────────────────────────────────────────────────┘
                              ↓
┌─ 第 2 步：S9 A2A 协议层 ──────────────────────────────────────────┐
│  引入 a2a-go/v2（仅 wire 与序列化边界）                            │
│  Agent Card / Session / Task 状态机（4 条超时边）                  │
│  S9-7 verification 三值入 schema（现在安全了）                     │
│  传输绑定：新增 WebSocket，保留 HTTP（§3.3）                       │
└───────────────────────────────────────────────────────────────────┘
                              ↓
        ┌─────────────────────┼─────────────────────┐
        ▼                     ▼                     ▼
┌─ S13 E2EE ─┐      ┌─ S10 节点能力 ─┐      ┌─ S11+S12 链上 ─┐
│ X25519 /   │      │ 观测索引 / 任务 │      │ SBT + 结算      │
│ XChaCha20  │      │ 中转 / 可归因验证│      │ + MCP           │
│ 密钥层级    │      │ (建在 WS 绑定上) │      │ (ABI 也要版本化) │
└────────────┘      └────────────────┘      └────────────────┘
                              ↓
┌─ 后期（Phase 1/2，按需，非 MVP）─────────────────────────────────┐
│  Postgres + NATS + Redis（durable relay）                         │
│  authority 平面（DelegationRegistry / nonce / scheme 判别符）      │
│  → 全部是**加法**，不改前面任何线的格式                             │
└───────────────────────────────────────────────────────────────────┘
```

**为什么这个顺序是"永不画地为牢"的：**

- **版本化最先**：它保护的是**后面所有**的变更。放最后做，等于前面所有变更都成为永久分叉。
- **消息契约第二**：`sequence` / `previousEventHash` 在签名信封内，**改它是 major**。
  现在定下来（哪怕是空值），Phase 1/2 才有地方长。
- **A2A 第三**：它是本体；且它的 wire 依赖 A2A 版本协商（第 0 步已就绪）。
- **E2EE 与节点并行**：两者都只加字段/端点，互不阻塞。
- **链上最后**：合约 ABI 一旦部署不可改（只能新部署），所以**结算 schema 也要版本化**，
  且要等任务语义稳定后再冻结。

### 3.3 传输层决策：现在采用 WebSocket（新增绑定），而不是以后重写

**问题：** MVP 用 HTTP；`ARCHITECTURE.md` §5.1 / RFN-01 要 WebSocket。

**推荐：现在就加 WebSocket 作为新的传输绑定，HTTP 全部保留。不要走"先 HTTP、以后重写"。**

**理由：**

1. **真正不可逆的不是传输本身，而是消息契约。** 只要信封已经是传输无关的
   （ID + 签名载荷 + 排序字段），HTTP↔WS 的切换是**绑定层**的事，不是协议的事。
   §3.2 第 1 步已经把这件事锁死了。
2. **A2A 原生支持多绑定。** `AgentInterface` 就是"一个 agent 声明多个 URL+transport"。
   声明 `[{http}, {websocket}]` 是 A2A 的**正常用法**，不是 hack。
3. **HTTP 是 pub/sub 的坏拟合。** S10 的"任务中转 + 实时事件 + 观测索引"天然是推送型。
   先做 HTTP+SSE 再改成 WS，会重写 S10 的**大部分**，而 S10 是本体。
4. **保留 HTTP 成本极低。** v1.0 的 `POST /messages`、`GET /messages/{agentId}`、
   `/healthz`、`/.well-known/relayfirst` 全部是既有资产，**继续保留**（`MVP.md` §7.3 已要求
   "新能力是叠加，不是替换"）。WS 是**新增**一个绑定。

**代价与诚实边界：**

- 引入 `github.com/coder/websocket`（ISC，纯 Go，见 §4）。
- WS 的**顺序语义要写清楚**：WS 只解决"推送"，**不解决"全序"**。
  `ARCHITECTURE.md` §4.2 已澄清：`relaySequence` 是 local，权威序来自
  per-actor `sequence` + hash chain。这条必须在实现 WS 时同时写进文档，
  否则会有人误以为 WS 给了全序。

**⚠️ 这是一次范围变更。** `MVP.md` §12 目前明确列着"WebSocket transport：不做"。
按 `AGENTS.md` §5.1，**必须先改 `MVP.md`，再动 `TASKS.md`**。本笔记不代改，只在 §8 提出。

---

## 4. 库采用策略

**原则（沿用 `MVP.md` §8.0 三分法）：** ① 密码学原语永远用库；② 编码/协议优先用库；
③ 业务数据结构自研。**下面的表只在①和②里做选择。**

| 能力 | 采用 | 版本 | 许可 | CGO 风险（A3） | 官方/维护 | 已知 advisory | 替代掉什么 |
|---|---|---|---|---|---|---|---|
| **A2A wire（Go）** | `github.com/a2aproject/a2a-go/v2` | v2.x（≥ v2.3） | Apache-2.0 | 无（纯 Go） | ✅ 官方（Linux Foundation） | 无已知 | 自研 Agent Card / Task 序列化 |
| **A2A wire（TS）** | `@a2a-js/sdk` | 0.3.x | Apache-2.0 | N/A | ✅ 官方 | 无已知 | 自研 TS 客户端 |
| **MCP server** | `github.com/modelcontextprotocol/go-sdk` | **≥ v1.4.0** | Apache-2.0 / MIT | 无 | ✅ 官方（与 Google 共维） | **GO-2026-5771 / CVE-2026-34742**（< v1.4.0，localhost DNS rebinding） | 自研 MCP server（S12-5） |
| **WebSocket** | `github.com/coder/websocket` | v1.8.x | **ISC** | 无（纯 Go，含 asm 快路径） | ✅ 活跃 | 无已知 | **`gorilla/websocket`（GO-2026-6278）** |
| **keccak256** | `golang.org/x/crypto/sha3` | 已有 | BSD-3 | 无 | ✅ 官方扩展 | 无 | —（已在用） |
| **secp256k1 / ecrecover** | `decred/dcrd/dcrec/secp256k1/v4` | 已有 v4.4.1 | MIT/ISC | 无（纯 Go） | ✅ | 无 | **`go-ethereum/crypto`（LGPL + CGO 风险）** |
| **X25519 / XChaCha20-Poly1305** | `golang.org/x/crypto/curve25519`、`/chacha20poly1305` | 已有 v0.57.0 | BSD-3 | 无 | ✅ | 无 | 自研（**违反 A1**） |
| **EIP-712 hashing** | **自研薄层**（`internal/eip712`） | — | — | 无 | — | — | `apitypes`（见 ADR-0001） |
| **SQLite** | `modernc.org/sqlite` | 已有 v1.60.1 | BSD-3 | 无（纯 Go） | ✅ | 无 | `mattn/go-sqlite3`（CGO） |
| **CLI** | `github.com/spf13/cobra` | — | Apache-2.0 | 无 | ✅ | 无 | 自研 flag 解析 |
| **限流（本地）** | `golang.org/x/time/rate` | — | BSD-3 | 无 | ✅ | 无 | 自研令牌桶 |
| **日志** | 标准库 `log/slog` | — | — | — | — | — | 第三方 logger |
| **SBT 合约** | `OpenZeppelin Contracts`（ERC-721 基类 + `IERC5192`） | 5.x | MIT | N/A | ✅ 事实标准 | 无 | **手写 ERC-721（等同合约层违反 A1）** |
| **Merkle** | 自研 `internal/merkle`（RFC 6962 + keccak256） | — | — | — | — | — | `cbergoon/merkletree`（无域分离，不满足需求） |
| **支付 / x402** | **暂不引入 Go SDK** | — | — | — | ❌ 未找到可信 Go SDK | — | 见下 |

### 4.1 明确"不要引入"的依赖

| 不要引入 | 原因 |
|---|---|
| `gorilla/websocket` | GO-2026-6278（弱 PRNG mask key）。用 `coder/websocket` |
| `go-ethereum`（整体） | LGPL-3.0 + 依赖树含 CGO 组件（`crypto/secp256k1` 有 `cgo` build tag）→ 威胁 A3。**若确需 RPC，只把 `ethclient` 隔离在独立的 settlement 二进制里**，绝不进节点 |
| `mattn/go-sqlite3` | CGO → 违反 A3 |
| 任何 ORM / query builder | SQLite 查询够简单；多一层抽象只会掩盖 SQL |
| Web 框架（gin/echo/chi…） | 标准库 `net/http` 已足够（现有节点就是这么做的） |
| NATS / Postgres / Redis（**现在**） | Phase 1/2 才需要；提前引入会让 MVP 依赖膨胀且无收益 |
| 搜索引擎（bleve 等） | S10 的观测索引用 SQLite 足够；除非有真实检索需求，否则不引入 |
| 未经验证的 x402 Go 库 | 见 §4.2 |

### 4.2 支付 / 结算：x402 的处理

**现状：** `ARCHITECTURE.md` §18.3 把 x402 定位为"Payment Adapter，逐字包裹"；
`MVP.md` §12 明确"只做最小 USDC 通道，不做 x402 适配器"。

**建议：**

1. **不引入任何 x402 Go SDK** —— 目前**没有确认存在的官方 Go SDK**。
   引入一个未验证的支付库，风险远大于收益。
2. **S12-3 直接做最小 USDC 通道**：ERC-20 `transferFrom` + 一个最小 escrow 合约
   （或直接用 `SettlementManager` 的最小版）。这与 `MVP.md` §12 一致。
3. **x402 留作可选 adapter**：当且仅当（a）有可信 Go SDK，且（b）有真实需求方要求
   按 HTTP 402 逐字包裹时，再加。它属于 `ARCHITECTURE.md` §8 的 Roadmap。
4. **⚠️ 结算 schema 也要版本化。** 合约 ABI 一旦部署不可改（只能新部署）。
   `SettlementLeaf` / `ClaimBitmap` 的形状必须现在就想清楚并写进版本表，
   否则未来结算格式变化会变成"链上永久分叉"。

### 4.3 MCP 的具体加固要求（GO-2026-5771）

- **必须** pin `modelcontextprotocol/go-sdk ≥ v1.4.0`。
- **必须** 在 localhost 部署下显式启用 DNS rebinding 防护（该 advisory 的根因是
  "默认关闭"）。
- **必须** 保持 `TASKS.md` S12-6 的约束：**MCP 不持主密钥，只转发已签字节**。
  这与 advisory 是两层独立防护，不能互相替代。
- CI 建议：加 `govulncheck ./...` 门禁（若尚未有）。

---

## 5. 是否新增硬不变式 A9（升级性）

**结论：应该加。** 但必须配一条 CI 门禁，否则是空文。

### 5.1 为什么该加

- **不加，§2 的所有设计都只是"建议"。** 下一个接手的人完全可以加一个字段、
  顺手改个 domain，然后整个网络的旧节点静默失联 —— 而且**没有任何测试会失败**。
- **分叉是不可逆的。** 一旦公共节点分叉，没有回滚路径（旧节点不会自己升级）。
  这与 A1–A8 的"违反即无效"完全同级。
- **它保护的是项目的对外身份。** v2.0 的对外身份是"去中心化 A2A 协议"。
  一个会硬分叉的"去中心化协议"不成立 —— 这与 A5/A6 保护叙事是同一种动机。
- **成本可控。** 真正贵的只有 §2.4（一次性），之后每次加字段都更便宜。

### 5.2 反对意见与回应

| 反对 | 回应 |
|---|---|
| "A2 刚被放宽（v1→v2），再加约束会拖慢速度" | A2 放宽的是**任务类型**；A9 约束的是**版本纪律**。两者不冲突，且 A9 的收益是"速度可持续" |
| "MVP 阶段谈向后兼容是过度工程" | 恰恰相反：**向后兼容只在"已上线"时才贵**。现在没有公共节点，是唯一的低成本窗口 |
| "会让每次加字段都要写测试" | 是的，但那正是目的。G3 一条测试即可覆盖 |

### 5.3 A9 草案文本（可直接粘进 `MVP.md` §17.3）

```
A9  协议版本兼容性（升级性）
    任何线格式或签名载荷的变更，必须是【向后可验证】的。

    ① 历史版本的验证规则永不删除。
       弃用只作用于"写入/中继"（minting/relaying），
       不作用于"验证"（verification）。旧回执永久可验。

    ② 版本协商必须在签名字节之外。
       使用 A2A-Version / AgentInterface / service params；
       不得在签名信封内放置"版本常量并要求精确匹配"。

    ③ 签名载荷必须自描述。
       回执携带 schema 标识与 domain 版本；
       验证者不得依赖任何带外协商信息来验证历史产物。

    ④ 签名载荷逐字签名（frozen blob）。
       验证者对载荷字节做哈希，不重新序列化结构。
       canonical JSON 的实现只影响新签名，不影响旧验证。

    ⑤ 破坏性变更必须新增 major，且旧 major 的验证器永久保留。
       破坏性 = 改变"旧产物在旧验证器下是否仍有效"。

    ⑥ CI 必须包含跨版本冻结语料与兼容矩阵门禁（G1–G4）。

    ⑦ 弃用数字：写入支持 = 最新 major + 上一个；
       写入弃用前置期 ≥ 12 个月；日落公告期 ≥ 6 个月；
       增量（minor）永不弃用。
```

**⚠️ 加 A9 是范围变更，必须先改 `MVP.md`（L0），再同步 `TASKS.md`。**
按 `AGENTS.md` §5.1，顺序不可反。本笔记不代改。

---

## 6. 风险登记册

**不可逆项（先列，因为它们的成本不对称）：**

| 项 | 为什么不可逆 | 决策时点 |
|---|---|---|
| **签名载荷形状**（逐字 blob vs 重建） | 一旦有回执上线，改它就是 major | **S9 前** |
| **EIP-712 domain 内容** | 改它 = 废掉全部旧签名 | **S9 前** |
| **回执 schema 语法** | 旧二进制无法追溯地容忍新语法 | **S9 前** |
| **epoch 起点（BLK-4）** | epoch 号在签名载荷内 | 开放真实挖矿前（已在 `docs/notes/epoch-anchoring.md` 记录） |
| **合约 ABI**（SBT / 结算） | 部署后不可改，只能新部署 | 部署前 |
| **消息契约**（sequence / hash chain 字段位置） | 在签名信封内 | S9 前 |

**可逆项（不必过度纠结）：** 库版本、端点路径、CLI 形状、存储引擎（SQLite→Postgres 是加法）。

**风险表：**

| # | 风险 | 严重度 | 缓解 | 可逆吗 |
|---|---|---|---|---|
| U1 | **不做 §2 地基就开写 S9** → 第一次加字段永久分叉 | **极高** | §7 的 8 项前置决策 | ❌ 不可逆 |
| U2 | **S9-7 先于 §2.4 落地** → `task.verification` 分叉 v1 节点 | **极高** | 强制顺序：地基 → S9-7 | ❌ 不可逆 |
| U3 | 旧验证器重建载荷字节漂移 | 高 | §2.4 逐字 blob + G1 冻结语料 | ❌（若已上线） |
| U4 | A2A SDK 版本 ≠ A2A 规范版本，跟随 SDK 升级导致 wire 漂移 | 中 | 钉住**规范版本**（`a2a.Version`），不钉 SDK 版本；G4 协商测试 | ✅ |
| U5 | MCP advisory GO-2026-5771 | 高 | pin ≥ v1.4.0 + 显式加固 + `govulncheck` | ✅ |
| U6 | `go-ethereum` 依赖蔓延（LGPL + CGO）破坏 A3 | 中 | 明确禁用；只允许隔离在 settlement 二进制 | ✅ |
| U7 | 传输层"先 HTTP 后重写" | 中 | §3.3：现在就加 WS 绑定，HTTP 保留 | ✅（现在做） |
| U8 | 手写 ERC-721 / SBT | 高 | 用 OpenZeppelin 基类 | ✅ |
| U9 | 依赖未验证的 x402 Go 库 | 中 | §4.2：不做，只做最小 USDC | ✅ |
| U10 | A2A 版本轴与 RelayFirst schema 轴漂移 | 中 | §2.6 的显式映射表 | ✅ |
| U11 | 加 A9 但不加门禁 → 空文 | 中 | G1–G4 与 A9 同时落地 | ✅ |
| U12 | 结算/bitmap schema 忘记版本化 | 中 | §4.2 第 4 点 | ✅ |
| U13 | 未知字段容忍被滥用（攻击者塞垃圾） | 低 | 大小上限（`MaxPayloadBytes` 已有）+ 严格模式可测 | ✅ |
| U14 | `unsupported` 与 `invalid` 混淆 → 把"我太旧"报成"你造假" | 中 | §2.3 显式区分 + G4 | ✅ |

---

## 7. 必须在 S9 第一行代码之前决定的事项

**这 8 项都是"决定成本 << 反悔成本"。**

| # | 决定 | 为什么必须现在 |
|---|---|---|
| **D1** | 回执 schema 语法（`v<major>[.<minor>]`）+ `unsupported`/`invalid` 区分 | 旧二进制无法追溯地容忍 |
| **D2** | 未知字段容忍策略（生产容忍 / `--strict` 拒绝） | 同上 |
| **D3** | **签名载荷改为逐字 blob**（§2.4） | 决定"加字段是否 = major" |
| **D4** | EIP-712 domain 多版本验证（try-all + 只增不减版本表） | 决定"旧回执能否永久可验" |
| **D5** | 采用 A2A-Version / AgentInterface，**不自研协商**；定义两条轴的映射表 | 决定互操作 |
| **D6** | 消息契约：`sequence` + `previousEventHash` 的字段位置（现在可恒空） | 在签名信封内，改它是 major |
| **D7** | 传输绑定：现在就加 WebSocket 绑定（**需改 `MVP.md` §12**） | 决定 S10 是否重写 |
| **D8** | 是否新增 A9 + G1–G4 门禁（**需改 `MVP.md` §17.3**） | 决定纪律是否可执行 |

**可以在 S9 之后再定的（便宜）：** 库的具体小版本、端点路径命名、CLI 文案、
`/observations` 的查询参数、MCP 的 IDE 配置形状。

**已经在别处记录、但要在上线前定稿的：** `BLK-4` epoch 起点
（`docs/notes/epoch-anchoring.md`）。它与 D4 相关：**起点变更 = 新 major + 新 domain version**，
所以 D4 必须支持"两个起点共存可验"。

---

## 8. 需要人工批准的 `MVP.md` 变更（本笔记不代改）

按 `AGENTS.md` §5.1，范围变化必须先改 `MVP.md`。以下三项需要人工裁决：

| # | 变更 | 影响位置 |
|---|---|---|
| **M1** | 把"WebSocket transport"从 §12 的**不做**清单移入范围（作为**新增绑定**，保留 HTTP） | `MVP.md` §7.3 / §12 |
| **M2** | 新增不变式 **A9**（§5.3 草案） | `MVP.md` §17.3 |
| **M3** | 把"版本化地基"列为 **S9 的前置任务**（建议编号 S9-0 或独立 stage） | `TASKS.md` §10.1 |

**建议的 S9 前置任务分解（供参考，不代改 `TASKS.md`）：**

```
S9-0a  回执 schema 语法 + unsupported/invalid 区分（D1/D2）
S9-0b  逐字签名 blob + 冻结 canonical JSON 语义（D3）
S9-0c  domain 多版本验证 + 版本表（D4）
S9-0d  采用 A2A-Version/AgentInterface 的版本探测（D5）
S9-0e  消息契约字段（sequence / previousEventHash）占位（D6）
S9-0f  CI：G1 冻结语料（v1 先入库）
S9-0g  CI：G2 跨版本矩阵 + G3 未知字段注入 + G4 协商无交集
```

**S9-7 必须排在 S9-0* 之后。**

---

## 9. 一句话总结

> **现在最该做的不是 A2A，而是"让以后每一次变更都不必再担心分叉"。**
>
> 具体就是四件事：**逐字签名载荷**、**多版本验证**、**协商放到签名之外**、
> **用 A2A 官方的版本原语而不是自研**。这四件事便宜、可测、且**过了这个窗口就再也做不了**。