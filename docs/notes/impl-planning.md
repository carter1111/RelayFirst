# 实现规划：激励机制落地（Stage 3）

> **输入**：[`impl-assessment.md`](impl-assessment.md)（Stage 2）。**输出** → [`impl-tasks.md`](impl-tasks.md)（Stage 4）。
> **不写实现代码。** 每个 P0 给"改哪些文件 + 接口怎么定"。

---

## 0. 前置（**先于任何实现**）

### 0.1 文档修正（Stage 2 清单 A/A'/H/I）——**裁决与状态（2026-10-08）**

| # | 矛盾 | 裁决 / 状态 |
|---|---|---|
| **A** | `ADR-0009 D7` 说 `bind`**拆出/未定义**；两处却引用 "D7" 说**机制已定** | ✅ **已裁决 = 选 (iii)**：**D7 已改写**收录（delegation-1 意向 + assertion-1 确认；理由：delegation 语义本就是"委托"，比硬塞 assertion 更顺；verifier 签 assertion-1 在白名单内，不动 policy；"节点无密钥"不受影响）。**无独立文档**。两处引用**现有效** |
| **A'** | `user-registration-flow.md:86`（已定）vs `:145`（待定）| ✅ **已修**：`:145` 改为"已定" |
| **H** | `incentive.md` 头版本 "v0.1" vs changelog "v0.21" | ✅ **已修**：头改 **v0.21** |
| **I** | `incentive.md` 自称 dev-blocking，`TASKS.md` 无对应任务 | ✅ **已做**：`TASKS.md §14`（`f1b878d`）；状态改为**已进 TASKS** |

### 0.2 L0 缺口（**实现前须先改 `MVP.md`**，§5.1）

| # | 缺口 | 裁决 / 说明 |
|---|---|---|
| **B** | 双池 + phase 比例（50/75/90 ∶ 50/25/10）**不在 MVP §6.2** | ✅ **定性 = 纯同步工作**（不重议）：`L0-1` 直接做。**注**：Layer 0 **分配**（非记账）才需 L0-2 |
| **E** | Merkle root：文档说 **settled map**；代码建在 **receipt 树** | ✅ **已裁决（2026-10-08）：保留两个 root，命名分职**——<br>**balance root**（settled map，叶 `keccak256(agentId‖total‖epoch)`）→ 用户 **claim 验积分**（`RelayPoints.sol` 已用此公式）<br>**receipt root**（回执集，叶 = receipt ID）→ **包含性**（补 §10.10 的"**可发现**"），已在 `MVP.md §S7` 定义<br>**两者不混成一个**。接线：P0-1 建 balance root；receipt root 已有（`anchorRoot`）|

> **注意**：B/E 属**范围/定义**变化 → **先改 `MVP.md`**，不得只在代码里改。
> **E 的补充（不阻塞，属 P1-4）**：receipt root 只给**包含**证明；**非包含（omission）**需排序树 + 非包含证明，或"重算 root 不符"的争议路径。

---

## 1. P0（可开工，不被 D2 / L0 阻塞）

### P0-1 settle 公式接线 —— **已完成（`a348a1d`），剩验证 + root**

| 项 | 内容 |
|---|---|
| 现状 | 常数已接线（7 天 / 7M / 0.85^⌊n/4⌋）；`Settle` 纯函数已实现 |
| **剩余** | ① **settled map → Merkle root** 接线（清单 E）；② 发布路径（root 上链/多签，属 S7，可后）|
| 改文件 | `cmd/relayfirst/main.go`（`runSettle` 后建 root）；`internal/merkle/`（叶编码辅助）|
| **接口** | `func RootFromSettled(epoch uint64, settled map[string]float64) (merkle.Hash, error)` |
| **硬约束** | 叶 **`keccak256(agentId ‖ total_micro ‖ epoch)`**（对齐 `RelayPoints._leaf`，**含 epoch 防跨期重放**，S11-4 教训）；**agent 排序**（canonical，防 map 乱序 —— 本次已修的同型 bug）|
| 验收 | root 可复算；与合约叶编码一致；`relayfirst settle` 打印 root；跨 epoch 同 leaf 得到不同 root |

### P0-2 5% cap + 销毁 —— **行为已对，缺显式断言**

| 项 | 内容 |
|---|---|
| 现状 | `CapAllocation` 截断 → 总量可 < 预算 → 即"销毁"（无独立开关）|
| 改文件 | `internal/scoring/emission_test.go` / `settle_test.go` |
| 验收 | 新增测试：单 agent work 占 20% → 其 points = `B(n)×5%`；`TotalAllocated(settled) < EpochBudget(n)`；**且无第二处写入**（不滚存）|

### P0-3 tenure / tier 记账

> ⚠️ **本项含一个必须先定口径的问题**（见下），且**只做"记账"，不做"分配"**。
> **分配**（Layer 0 pool 份额）卡在 **L0 缺口 B**。

| 项 | 内容 |
|---|---|
| 可做部分 | **tenure 状态机**（输入的"合格 epoch"序列 → tenure 数 + tier 权重）|
| 不可做部分 | **在线 slot 测量**（依赖 P1 的 liveness/challenge 协议 + slot 参数）|
| 新文件 | `internal/tenure/tenure.go`（**叶子包**，无 crypto、无 scoring，可被抓任何二进制引用而不破坏 CI 导入图）|
| **接口** | `func Tier(tenureEpochs int) float64` → 1× / 1.1× / 1.25×（3–5/6–11/12+）<br>`type State struct{ Tenure int; … }`<br>`func Advance(prev State, qualified bool) State` → 合格 +1；单次不合格降一档；**连续两次 → 清零** |
| 表 | 新表 `tenure(node_id, epoch, qualified, …)`（`internal/store`）|
| **待定口径 ❌（需你裁决）** | "**连续两 epoch 不达标清零**"里的"不达标"如何喂入？选项：(a) 只吃一份**外部给出的** qualified 布尔（P0 最省，测量留 P1）；(b) P0 就定义 slot 门槛（越界到 P1 的未设计协议）。**建议 (a)** |
| CI 风险 | tenure 若被 `relayfirst-node` 引用须仍无 crypto；否则只能在 settler/verifier 侧 |
| 验收 | 状态机测试覆盖：+1、降档、连续两次清零、边界 3/6/12；tier 加权求和正确 |

### P0-4 claim（CLI 优先 + web ticket proposed）

| 项 | 内容 |
|---|---|
| **范围切分** | **P0 做"证明生成 + 展示"（不碰 key）**；**链上提交**是 TGE-time（需钱包签名，见下）|
| 改文件 | `cmd/relayfirst/main.go`（新 `claim` 命令）；复用 `internal/merkle`（`Proof` 生成）|
| **接口** | `relayfirst claim [--epoch N] [--agent 0x…] [--to 0x…] [--web]`<br>输出：`{agentId, epoch, total, proof:[…], root}`（JSON，非 TTY 冻结）|
| ticket 配对（**proposed**） | `relayfirst claim --web` → 一次性 ticket（5 分钟）；Web 拉只读数据；**Web 永不接触 key** |
| **D2 边界** | 证明生成**不需密钥**（读本地账本）→ **P0 可做**。**链上 claim tx 需钱包签名** → 依赖 signer（**BLOCKED**）→ **提交路径 defer** |
| 验收 | 给定 (agent, epoch) 产出可被 `merkle.Verify` 通过的 proof；不改变非 TTY JSON；无密钥参与 |

### P0-5 待结算预估显示层

| 项 | 内容 |
|---|---|
| 改文件 | `cmd/relayfirst/main.go`（`status` TTY 路径）；读 `SQLWorkLedger.Totals(epoch)` |
| 语义 | 区分 **已结算（points 账本）** vs **本 epoch 待结算预估（work 聚合，非提前结算）** |
| 验收 | 非 TTY 仍输出今天 JSON（字段不变）；TTY 面板加"待结算预估"行，标注"未结算" |

---

## 2. P1（待设计，不进 P0 工期）

| id | 内容 | 为什么是 P1 |
|---|---|---|
| **P1-1** | liveness **challenge 协议** + slot 参数（1008 slots、95% 门槛、X 值）| 协议未设计；防游戏参数未定 |
| **P1-2** | **VPS farm ROI 建模** | **launch-blocking**（上线门槛，非开工门槛）；50% 池下须先证明矿场无利可图 |
| **P1-3** | SBT **发放/吊销规则**（tier 阈值已定 3/6/12）| 规则未定 |
| **P1-4** | **omission 机制**（settler 审查的强制纳入）| 机制未设计 |

---

## 3. BLOCKED（等 D2 批准，**D2 未批不得实现**）

> `ADR-0009` 状态 = **proposed**；D2 = **推翻 no-init 决策**，属 **L0**（`README`/`getting-started` 明文记录 no-init）。

| id | 内容 | 阻塞点 |
|---|---|---|
| **BLK-D2-1** | `relayfirst init`（两把独立密钥 + keystore + mnemonic）| **D2 未批** |
| **BLK-D2-2** | keystore 存储（0600 / OS keychain）| D2 |
| **BLK-D2-3** | signer daemon（per-key domain 白名单）| D2（且本会话已裁定 **v2 延后**，见 `planning.md §0A2`）|
| **BLK-D2-4** | `relayfirst bind` | D2 **且** 机制无处定义（清单 A）→ **双重阻塞** |

**规则**：D2 未批 → 这四项**只停在 planning/tasks 文字**，不落代码。

---

## 4. 可并行 vs 串行

```text
文档修正（0.1）─────┐
L0 修订（0.2 B/E）──┴─→ P0-1 root 接线
                        P0-2 cap 测试
                        P0-3 tenure（口径定后）
                        P0-4 claim 证明
                        P0-5 显示层
   ↑ 这五条彼此独立，可并行；除 P0-3 需先定"qualified 如何喂入"
BLOCKED（D2）—— 全部串行于 D2 之后
```

---

## 5. 给 Stage 4 的输入

1. P0-1..5 → 拆成可派任务；P0-3 先放一个"口径裁决"前置任务。
2. 文档修正（A/A'/H/I）与 L0 修订（B/E）→ **前置任务**，不并入代码任务。
3. BLOCKED 四项 → 记为"待 D2"，**不派**。

---

## 6. Layer 0 设计（**P1 前置，2026-10-08**）—— 不是"接线"能解决的

> **背景**：IMP-1..5 做完后，Layer 1 的代码链路闭环了，但 **Layer 0 几乎全空**（见 `impl-assessment.md §0.1`）。
> **关键发现**：**Layer 0 的真正阻塞是【没有输入源】** —— `internal/tenure` 需要一个"合格 epoch"布尔，
> 而**产出它的 liveness 协议在仓库里不存在**（`internal/a2a` 的 heartbeat 是**任务**级，不是节点级）。
> 所以"把 tenure 接进去"目前**无处可接**。按 `AGENTS.md §7.3`，这里**不发明协议**，只给**选项 + 待决**。

### 6.1 缺的三块，依赖关系是【链式】

```
① "合格 epoch" 的【测量】   ← 产出布尔
      ↓
② tenure → tier → 权重      ← 已建库（internal/tenure），等①
      ↓
③ 两池拆分 + phase 比例     ← settle 仍是单池；等②定义"Layer 0 分什么"
```

**倒着做不通**：③ 要知道分给谁、按什么权重（需②）；② 要有布尔输入（需①）。

### 6.2 ① 测量：让"合格 epoch"不可自证 —— **待决**

`incentive.md` 要求"发放挂钩**可验证存活信号**，**不接受自证**"。但结点**无密钥**（ADR-0004）——
即**节点日志本身不可信**（否则节点就能伪造激励）。**所以存活信号必须由【他人】观测。** 关键问题：
**谁观测、以什么为证据、如何防串谋？** 三个选项：

| 选项 | 谁观测 | 证据 | 代价 / 弱点 |
|---|---|---|---|
| **A. Verifier 签心跳** | 独立 **verifier**（有密钥，`assertion-1` 白名单内）| 对"我在 slot t 收到 node N 的响应/消息"签轻量 attestation | 协议**全新**；**verifier–node 可串谋**（须 A≠B 约束，类似 S4-6）；1008 slots/epoch 的签名噪声 |
| **B. 邻居观测（轻 gossip）** | 其他节点（**无密钥**）| 对等观测，客户端**聚合** | 节点不可签（无密钥）→ 证词须**外部**签名；仍是**新协议** |
| **C. 链上/挑战式 proof-of-serve** | 任意挑战者 | 对某键的 challenge-response | 需**密钥**（节点无）→ 须外部 signer；引入交互协议 |

**共同结论**：Layer 0 的测量**无法用节点自己的话**。**必须先定"谁来签存活证据"**，否则②③无输入。

### 6.3 ②③ 一旦① 定，才谈得上

- **②** `internal/tenure` **已就绪但无消费者**：需一个**喂入者**（把外部"合格"布尔写进 `node_tenure`）+ **消费者**（tier 权重进份额）。
- **③** `settle` **单池改两池**：`B(n)` 拆 Work/Node pool + phase 比例切换；Layer 0 池按 tenure-tier 加权（`tenure.PoolShare` 已建）。

### 6.4 关于"三个必然缺口"

我上一轮提到它们，**但这不属于本文档**（本文档是 incentive 实现）。两句最简陈述备查：
1. **无 P2P 发现/gossip**（需带外知道节点 URL —— 与 Nostr 同）；
2. **无共识**（状态由签名 + 确定性重算保证，非全局共识）。
→ 这两者属 `ARCHITECTURE.md` 路线图（发现层 G1–G5），**不在本批**。

### 6.5 给决策者的三个问题

1. **①的观测者是谁**？（A verifier / B 邻居 / C 挑战）——**这决定 Layer 0 能不能做**。
2. **X 最终值**（`incentive.md §10.2`）——PoSR 乘数上限，节点数建模后定。
3. **farm ROI 建模**（§10.3，**launch-blocking**）——50% 池下须先证明矿场无利可图。

> **一句话**：**Layer 0 卡在【设计】，不卡在【代码】。** 在 6.5 的三问答上前，写 Layer 0 代码无处下手。

