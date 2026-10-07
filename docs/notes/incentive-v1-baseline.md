# incentive v1 基线：SBT 规则 / sunset / sink / claim UX（可上线、可升级）

> **性质：设计（非代码）。** 决策者 2026-10-08："设计一个合理 + 基本 + 上线后可升级的版本"。
> 把 `incentive.md §10` 的四个未决项收敛到一个**可上线的最小版**，并标明**升级路径**。
> 关联：`incentive.md §5/§10.6/§10.7/§10.12/§10.13/§10.14`、`MVP.md §6.1`、S10-0、S11。

---

## 0. 三条设计原则（决定后面每一项的形状）

1. **不上链**（除非 gas 无解说明）→ 链下记账 + TGE 时体现。依据：`incentive.md` 的 gas 结论（每 epoch mint 无解）。
2. **参数预锁定 > 灵活性**（可预测性优先）→ sunset 写成固定 epoch，不引治理投票。
3. **不引入争议层**（A2 非目标）→ 吊销/欺诈走 2.1，v1 一律"只增不减"。

---

## 1. SBT 发放 / 吊销（v1）

**现状**：Agent 工作 SBT = S11（ERC-721 + ERC-5192，**mint 一次/身份**）已实现；Node tenure tier = **纯链下**（`node_tenure` + `internal/tenure`）。

| | v1 基线 | 升级路径 |
|---|---|---|
| **Agent 工作 SBT 发放** | **首个有效回执即 mint**（现状即此）| Milestone SBT（"首个 10k""连续 12 epoch"）→ **EAS off-chain attestation**（附录 B "值得偷的一条"）|
| **Agent SBT 吊销** | **不吊销**（只增不减）| 欺诈吊销 → 属 2.1 dispute，**v1 不做** |
| **Node 徽章（铜/银/金）** | **链下 tier**（`tenure.Tier` 已算：3+/6+/12+）；**不上链、不 mint** | TGE 时以 `tenure_bonus` 体现；未来 EAS attestation 让钱包可见 |

**为什么不上链**：每身份每 epoch 上链 = gas 无解（既有结论）；且"先发后 gate"（`§5`）要求 v1 只**记录**、不 gate 奖励。

**唯一硬约束**：徽章**不可兑换**（`§5`）—— 一旦可兑，时间门槛即崩，女巫防御失效。

---

## 2. Sunset 治理（v1）

**已定原则**：12 个月后**无决议则自动停止** Layer 0。

**v1 基线**：**写死一个 sunset epoch**（`LAST_NODE_POOL_EPOCH`），到点后 `NodePoolFraction` 自动归零（Layer 0 停止，预算全给 Layer 1）。

```text
epoch <= 25      → node pool 50%
26 <= e <= 51    → node pool 25%
e > 51           → node pool 0%（sunset；12 个月 ≈ 52 个 7 天 epoch）
```

**为什么写死**：预锁定、可预测、**无需治理投票**（`§2` 的"可预测性 > 灵活性"）。**无决议 = 自动停**，不依赖任何人记得去投票。

**升级**：引入治理决议流程（谁决议、提前终止、续期）。

❌ **待定**：sunset 是 epoch 52 还是另一个数；是否留"续期"口子。

---

## 3. Sink（v1）—— ⚠️ 前提：账本要支持 burn

**⚠️ 前提（必须先解决，非本设计可绕）**：`point_entries.Credit` **拒绝 `≤0`**，`Balance = SUM(micro_points)` —— **今天无法记一笔"烧掉"**。所以**任何 sink 都先要一条 burn 路径**。

**建议 v1 burn 形态**：**独立 `point_burns(agent_id, amount_micro, reason, epoch, at)` 表**，`可花余额 = credits − burns`。
比"允许负 entry"更好：**可审计**（burn 不与 credit 混流），且不会把一次惩罚误记成负 credit。

**v1 基线 sink（一个，最小）**：

| sink | 机制 | 依赖 | 选它/不选 |
|---|---|---|---|
| **Relay ID / 命名空间注册 burn** | 烧 points 换一个名字（S10-0 chargeable namespace）| **S10-0 收费机制**（❌ 待定）| `§10b` 明列的 P0 sink；**最贴现成** |
| 手续费折扣（出示 balance proof）| 不 burn，只出示余额换折扣 | 无（可先做）| **更易落地**、可作 v1 的备选 |

**v1 建议**：先落 **burn 表**（前提），**sink 选"手续费折扣"**（不依赖 S10-0，立即可用）；**Relay ID burn 待 S10-0 收费机制就绪后接入**同一张 burn 表。

**升级**：更多 sink（优先级/配额/治理权重）。

---

## 4. Claim UX（v1）

**已有**：`relayfirst claim`（产 proof，**无 key**，读本地账本）。

| | v1 基线 | 升级 |
|---|---|---|
| **签名 / 提交** | **CLI 优先**（key 不出本机）；**链上提交 defer**（需 signer = D2）| D2 批准后接 signer |
| **展示** | **Web ticket 配对**（`incentive.md` proposed）：CLI 出一次性 ticket → Web 只读展示待领数据 → 回 CLI 签名 | 网站只读 dashboard + claim 发起页 |
| **红线** | Web **永不接触**私钥/助记词；ticket 一次性 + 短过期 | — |

**v1 不做的**：gas 代付（relayer）、多身份聚合 UI（延后）。

---

## 5. 四项的共同结论

| 项 | v1 基线一句话 | 升级路径 |
|---|---|---|
| **SBT 规则** | Agent 徽章首个回执即发、不吊销；Node tier **只链下记账** | EAS attestation / milestone / 2.1 吊销 |
| **Sunset** | **写死 epoch 52 自动停** Layer 0 | 治理决议流程 |
| **Sink** | 先建 **burn 表**；v1 sink = 手续费折扣（Relay ID burn 待 S10-0）| 更多 sink |
| **Claim UX** | CLI 签名 + **web ticket 只读展示**；**提交 defer(D2)** | 网站 dashboard |

**共同贯穿**：**不上链、预锁定、不引争议层**；**升级点全部预留、不破坏 v1**。

---

## 6. 给决策者的两个待定（其余 v1 已可落）

1. **sunset epoch 号**（建议 52）+ 是否留续期口子。
2. **burn 表 vs 负 entry**（建议独立 `point_burns` 表）——这是 sink 的前提，需你点头再实现。

（`r` / `X` / farm ROI 判据已归"平台币设计"，不在本设计内。）
