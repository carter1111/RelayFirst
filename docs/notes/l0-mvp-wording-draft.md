# L0-1 / L0-2 `MVP.md` 措辞稿（**待批准，未应用**）

> 状态：**提案**。按 `AGENTS.md §5.1`，改 `MVP.md`（L0）须**先获批**。**尚未改动 MVP.md。**
> 批准后：逐条应用到 `MVP.md`，再解锁 IMP-1（L0-1）。
> 相关：`impl-tasks.md` L0-1/L0-2；裁决见 `impl-planning.md §0.2`。

---

## L0-1：两个 root 的命名与分工（E 的裁决落地）

**拟在 `MVP.md §6.2`（settle 段）之后新增一小节**，并在 §S7 呼应：

> ### 6.2b 两个 Merkle root（**不同物，不同用途**）
>
> 积分系统产生**两个** Merkle root。它们**不得混为一个**。
>
> | 名称 | 叶 | 输入 | 用途 |
> |---|---|---|---|
> | **balance root** | `keccak256(agentId ‖ total_micro ‖ epoch)` | 某 epoch 的 **settled 数字**（`Settle` 的输出）| 用户凭 proof **claim 积分**（`RelayPoints.sol` 即用此公式）|
> | **receipt root** | `receiptId` | 某 epoch 的**回执集** | 证明"我的回执**在** epoch N 里"（**包含性**）|
>
> - **balance root** 由 `relayfirst settle` 之后从 settled map 计算（确定性；agent 排序）。
> - **receipt root** 是 §S7 已定义的 `mapping(epoch => receiptsRoot)`，用于**发现**遗漏。
> - **不在同一个 root 里表达两件事**：积分 claim 只需 balance；omission 检测只需 receipt。
> - **完整性 vs 真实性**：root 保证"结算结果未被改"，**不**保证"输入回执为真"（后者由 work 函数 + verifier 负责）。

**对 §S7 的一行呼应**（拟加）：

> §S7 的 root 是 **receipt root**（回执包含性）；积分的 **balance root** 见 §6.2b。两者服务不同问题，不共用一个 mapping。

**（可选）非包含证明**：本身属 P1-4；此处只加一句指针——
> 证明某回执**不在** epoch 中（omission）需排序树 + 非包含证明或"重算 root 不符"的争议路径，见 `incentive.md §10.10`。

---

## L0-2：双池 + phase 比例（纯同步，不重议）

**拟在 `MVP.md §6.2` 的 emission 段补充**（把 `incentive.md §2/§3/§4` 的定案同步进 L0）：

> ### 6.2c epoch 预算的拆分（Layer 0 / Layer 1）
>
> 每 epoch 预算 `B(n)` 拆为两池，比例**按 epoch 高度预定、自动切换**（无治理投票）：
>
> | 阶段 | Epoch | Work pool（Layer 1）| Node pool（Layer 0）|
> |---|---|---|---|
> | Phase 1 | 0–25（约 6 个月）| 50% | 50% |
> | Phase 2 | 26–51（约 6 个月）| 75% | 25% |
> | Phase 3 | 52 起 | 90% | 10% |
>
> - **Layer 1（工作池）**：`share_i = work_i × m_i / Σ(work_j × m_j)`，`m_i = 1.25`（绑定）/ `1.0`；per-agent 上限 **5%**，余量**销毁**。
> - **Layer 0（节点池）**：**tenure-tier 加权** `share_i = weight_i / Σ(weights)`；权重 1×（3–5 epoch）/ 1.1×（6–11）/ 1.25×（12+）；**资格 tenure ≥ 3**。
> - **资格与测量**：Layer 0 要求"连续在线"（slot 心跳 + liveness 抽查）——**协议属 P1 设计**；P0 只落地 **tenure 记账**（吃外部"合格 epoch"布尔）。

**§6.3 呼应**：per-agent 5% 上限现措辞可保留；补一句"**余量销毁**（未分配预算不增发）"。

---

## 待你确认的点（批准前）

| # | 问题 | 我的默认建议 |
|---|---|---|
| 1 | **balance root 叶**是否沿用 `keccak256(agentId‖total_micro‖epoch)`（含 epoch）| ✅ 沿用（与 `RelayPoints.sol` 对齐；含 epoch 防跨期重放）|
| 2 | **§6.2b 放位置**：新小节 vs 并入 §6.2 | 新小节（不打断现 §6.2）|
| 3 | **L0-2 是否现在做** | 可做（纯同步）；但 Layer 0 **分配**实现仍可留后，P0 只做记账 |
| 4 | Phase 边界（`≈6 个月`）表述是否照抄 `incentive.md` | ✅ 照抄（epoch×7 天 = 25×7 ≈ 175 天 ≈ 6 个月）|

**批准即应用；未批准不动 `MVP.md`。** IMP-1 依赖 L0-1。
