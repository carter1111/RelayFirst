# Layer 0 心跳协议提案（Q1 = A）

> **状态：提案（待批）。** 依据 `layer0-liveness-decision.md §6`（决策者结论：Q1=A；1.25× 须重审；r 是政策武器）。
> 关联：`ADR-0004`（节点无密钥）、`ADR-0009 D3`（per-key domain 白名单）、S10-0（节点身份）、S4-6（防串谋模板）、`incentive.md §3/§10.3`。
>
> **可直接实现的部分标 ✅；需要先定语义的部分标 ❌（按 `AGENTS.md §7.3`，不发明）。**

---

## 0. 先说最硬的一件事：Q1=A 的**伪造/自证**隐患（❌ 须先定）

**节点无密钥**（ADR-0004），S10-0 里 `NodeID` 是**发布但不可自签**的。于是"节点 N 存在且在 slot t 活着"这句话，
**完全由 verifier 一口咬定**。两个后果：

1. **换皮的自我证明**：若运营者**自己持有** attest 其节点的 verifier 钥（自建节点是常态），
   则"verifier 签心跳" ≠ 独立验证，而是**运营者给自己的节点签字** —— 与"不接受自证"（`incentive.md §3`）**同病**。
2. **凭空造节点**：verifier 可以给**根本不存在的 NodeID** 签心跳，零成本刷 Layer 0。

**→ 这是 Q1=A 的核心语义问题，不是实现细节。** 三个选项（**须你选**）：

| | 做法 | 代价 |
|---|---|---|
| **F1. Verifier 独立且法定** | attest 者（verifier）必须 **≠ 节点运营者**（仿 S4-6 的 **A≠B**）| **谁是法定独立 verifier？它凭什么被信任、有什么激励？** 否则网络里没人愿当 |
| **F2. 服务证明（serve-proof）** | 节点必须**承载真实签名流量**（需求方/客户端可确认"我从 N 收到了有效数据"），verifier 只作聚合 | 需要一个**需求方/客户端的观测回执**；引入新数据流 |
| **F3. 坍缩：节点即 verifier** | 不给"节点"单独发钱 —— Layer 0 直接归 **verifier 身份**（一 verifier = 一个被奖励实体）| **改变了 Layer 0 的语义**（从"奖励节点"变"奖励 verifier"）；须改 `incentive.md §3` 的叙述 |

**⚠️ 在 F1/F2/F3 定之前，心跳协议【写不完】** —— 因为"谁签、凭什么是真的"决定了协议长什么样。
**其余部分（存储、阈值、settle 接线）不受影响，可先做**（见 §3/§5）。

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

## 6. 乘数（1.25×）—— **须重审**（决策者 §6.2）

现状：1.25× 只挂**绑定**，与**可靠性**无关 → **自家/租用挂名 verifier 即拿满**，绕过 §0 的测量。

**提案给出两条路（须你选）**：

| | 做法 | 含义 |
|---|---|---|
| **M1. 条件化** | `m_i = 1.25` 当且仅当**绑定且该节点当期达标**（过 §0 门槛）| 把加成挂回"可靠性"，堵住挂名 |
| **M2. 保持** | 1.25× 仍只挂绑定 | **须书面说明为何挂名绑定不算滥用** |

倾向 **M1**：否则 Layer 0 的整套存活测量**被 PoSR 旁路**，一边认真测可靠性、一边给不靠谱的绑定发满加成。

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

## 9. 给决策者的两个新问题（§0 之外）

1. **F1/F2/F3**：谁是可信的 attest 者？（**这决定心跳协议能否成立**）
2. **M1/M2**：1.25× 是否条件于可靠性达标？

（X、r 的取向如前。）
