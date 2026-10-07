# Layer 0 心跳协议提案（Q1 = A）

> **状态：已裁决并开工（2026-10-08）。** 决策者结论：**Q1=A**、**F1**（独立 verifier + A≠B）、**M1**（1.25× 条件化，须绑 receipts）、**F2 = v2 升级路径**、**X 先取 95% 基准**。
> 关联：`ADR-0004`（节点无密钥）、`ADR-0009 D3`（per-key domain 白名单）、S10-0（节点身份）、S4-6（防串谋模板）、`incentive.md §3/§10.3`。
>
> **决策已落入本文各节；实现见 `internal/heartbeat`（已建）+ `settle` Layer 0 接线。**

---

## 0. 先说最硬的一件事：Q1=A 的**伪造/自证**隐患（❌ 须先定）

**节点无密钥**（ADR-0004），S10-0 里 `NodeID` 是**发布但不可自签**的。于是"节点 N 存在且在 slot t 活着"这句话，
**完全由 verifier 一口咬定**。两个后果：

1. **换皮的自我证明**：若运营者**自己持有** attest 其节点的 verifier 钥（自建节点是常态），
   则"verifier 签心跳" ≠ 独立验证，而是**运营者给自己的节点签字** —— 与"不接受自证"（`incentive.md §3`）**同病**。
2. **凭空造节点**：verifier 可以给**根本不存在的 NodeID** 签心跳，零成本刷 Layer 0。

**→ 这是 Q1=A 的核心语义问题，不是实现细节。** 三个选项（**已裁决 = F1**）：

| | 做法 | 代价 |
|---|---|---|
| **F1. Verifier 独立且法定** ✅ **选定** | attest 者（verifier）必须 **≠ 节点运营者**（仿 S4-6 的 **A≠B**）| 复用现有物；把"两洞压成一洞"（见下）|
| **F2. 服务证明（serve-proof）** | 节点必须**承载真实签名流量** | **记为 v2 升级路径**，本版不做（= proof-of-useful-work 本题，scope creep）|
| **F3. 坍缩：节点即 verifier** | Layer 0 直接归 verifier 身份 | ❌ 未选 —— 会使 tenure/pool-share/farm ROI 的假设全塌 |

**F1 已实现**（`internal/heartbeat`）：`Qualified()` 在验签后**硬检查** `signer ≠ nodeOperator`，**自证被拒**（**变异验证**：禁用该检查 → 测试 FAIL）。两洞压成一洞：
换皮自证被 A≠B 直接杀死；**凭空造节点**在 F1 下须**串谋外部 verifier** —— 即 Q1=A 已接受的唯一信任假设（S4-6 同型防线）。**信任假设从两个减为一个。**

---

## 1. 为什么心跳**不新增 domain**（✅ 已核实，安全）

`assertion` 包的 domain 是 `RelayFirst / assertion-1`，EIP-712 **typeHash 进摘要**：

```
digest = keccak(0x1901 ‖ domainSeparator ‖ hashStruct(struct))
```

**新的 struct（`RelayNodeHeartbeat`）在【同一 domain】下，摘要不同 → 不可重放为 verdict。**
因此心跳可用 **`assertion-1` 域、新 struct**，符合 **ADR-0009 D3 的"按 domain 白名单"**（agent/verifier 的白名单不变）—— **不改 signing policy**。

**待你确认**：是把 `RelayNodeHeartbeat` 加进 `internal/assertion`（同域新 struct），还是新建 `internal/heartbeat`（新包、**同域**）。倾向 **`internal/heartbeat`**：与 verdict 逻辑解耦，且便于单独测试。

---

## 2. 协议骨架（✅，与 F* 正交）

```
Slot：        epoch = 7 天；slot 粒度取 10 分钟 → 1008 slots/epoch（参数，可调）
心跳：        每 slot，attest 者签 { nodeId, epoch, slot, kind:"heartbeat" }（EIP-712，assertion-1 域）
存储：        同现 schema —— 一行/（node, slot）；可聚合，不必逐 slot 落库（见 §3）
达标：        单 epoch 在线 slot ≥ 95%（`incentive.md §3`），且单次连续掉线 ≤ X slots
tenure：      达标 → 记 qualified=true 进 node_tenure；否则 false；降档/连续两次清零由 tenure 状态机处理（已建）
```

**X（单次掉线上限）** 未定（`incentive.md §10.3`）：示例 `18 slots ≈ 3h`，防"每天定时掉线"的游戏。**待你定**。

---

## 3. 存储与 schema（✅ 可先做）

- **`node_tenure`（已建）** 保持不变：**只存"合格 epoch"布尔**（每 node 每 epoch 一行），由达标计算写入。
- **心跳明细**（新，若 F1/F2 选读数细粒度）：`node_heartbeats(node_id, slot, signer_id, ...)`。
  **建议先只落 `node_tenure`**（达标是布尔），**明细按需再开** —— 1008 slots × N 节点会放大，
  而 `incentive.md` 的 gas/存储结论（~200KB/epoch/verifier）**前提是 off-chain 聚合**。
- **喂入者**：一个 `relayfirst node heartbeat`（或 verifier 侧子命令）把达标结果写 `node_tenure`。
  **消费者**：`settle` 的 Layer 0 池（见 §5）。

---

## 4. 防串谋（❌ 依赖 §0 的 F*）

若选 **F1**：复用 **S4-6** 同型防线 —— **attest 者 ≠ 节点运营者**（A≠B）、**比例上限**、
**无活动 `fail closed`**。**这回挡的是"运营者自证自己的节点"，与 S4-6 挡"自验"同构。**

若选 **F2**：防线在"**需求方/客户端的观测回执**" —— 节点必须被**他人用到**才算活。

若选 **F3**：无串谋问题（无独立节点），但语义变了。

---

## 5. `settle` 接线（✅ 可先做，与 F* 正交）

```text
Layer 0 池 =  B(n) × nodePoolFraction(epoch)      × tenure.PoolShare(allTenures)
Layer 1 池 =  B(n) × (1 − nodePoolFraction(epoch)) × share_i(work)
```

- `nodePoolFraction`：Phase 1 50% / 2 25% / 3 10%（`MVP.md §6.2c`，已定）。
- `tenure.PoolShare` / `AllTenures`：**已建**（`internal/tenure` + `store.SQLTenureLedger`），只差接线。
- **无合格节点时**：Layer 0 池**不发出**（销毁，与 cap 同向安全）。

---

## 6. 乘数（1.25×）—— **已裁决 = M1（条件化，且须绑 receipts）**

**决策者要点（关键）**：M1 的条件**不能是"测得活着"** —— **农场的机器本来就是活的**，独立 verifier 会如实签，
所以"活着 + 绑定 = 1.25×"对农场**是摆设**。**1.25× 必须绑定 `tenure` + 有效 `receipts`（工作量）**：
**零 receipts 的 tenure 只拿 1.0×**。阈值**按 Layer 1 的 receipts 口径**定。

**提案采用**：

```text
m_i = 1.25  当且仅当：  agent 已绑定（PoSR）  ∧  node.tenure ≥ 3  ∧  agent 本期 receipts > 0
m_i = 1.0   否则
```

- **`receipts > 0`** 用的是 Layer 1 已有的 work/回执口径（`work_records`），**不新造口径**。
- **零 receipts 只 1.0×**：把"跑节点多赚 25%"**限定给真在干活的 worker**，而不是"活着就领"。
- **待定细化**：`receipts` 阈值用"**> 0**"还是某最小值？（先 `> 0`，可调。）
- 与 F1 正交：F1 管"节点算不算活"，M1 管"活了给不给加成"。**两者都不足以单独发钱** —— 还需 work。

---

## 7. r 作为政策武器（决策者 §6.3）

**r 不是待填参数，是唯一的"价值旋钮"**（积分不定价，A5）。它同时决定：
farm ROI 的"无利可图"能否证成（`farmroi.go`）、挖矿激励强度、TGE 分配。

**提案建议**：把 r **从 `incentive.md §10.11` 的"待定参数"重新表述为"TGE 时一次性公布的政策工具"**，
并与 `farmroi.go` 的 break-even **挂钩**（r 定在使 break-even 高于可行值处）。**不改 A5**（不承诺回报）。

---

## 8. 验收线（提案 → 实现）

- [ ] **§0 的 F1/F2/F3 已选**（**倒入**；未选则只做 §2/§3/§5 的存储与接线，心跳"谁签"留空）。
- [ ] X（掉线上限）已定。
- [ ] `RelayNodeHeartbeat` 在 `assertion-1` 域（**不新增 domain**，已核实不可重放）。
- [ ] `node_tenure` 喂入 + `settle` 双池接线（Layer 0 = tenure 加权；Layer 1 = work）。
- [ ] 防串谋约束（按 F* 选）有**变异验证**（禁用 → 测试 FAIL）。
- [ ] M1/M2 已选；r 姿态写入。
- [ ] **不改** ADR-0004 / 不新增未定义结构而不先问。

---

## 9. 决策记录（2026-10-08，全部已定）

| 问题 | 裁决 |
|---|---|
| **Q1 观测者** | **A**（verifier 签心跳），复用现有物 |
| **F1/F2/F3** | **F1**（独立 verifier + A≠B 硬检查）；**F2 = v2 升级路径**；F3 未选 |
| **M1/M2** | **M1**（1.25× 条件化：绑定 ∧ tenure ≥ 3 ∧ **receipts > 0**）|
| **X** | 先取 **95% 基准**（`DefaultSpec`：1008 slots / 95% / MaxGap 18），实现时可调，**不阻塞** |
| **r** | 政策武器（§7），取值待 TGE |

**实现状态**：`internal/heartbeat`（attestation + sign/verify + **A≠B** `Qualified`）✅ **已建**（**变异验证**：禁用 A≠B → FAIL）；
`settle` Layer 0 接线 见 §5。
