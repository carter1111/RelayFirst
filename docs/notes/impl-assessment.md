# 实现评估：激励机制落地（Stage 2）

> **性质：评估，不写代码。** 输入 = `incentive.md` v0.21 + `user-registration-flow.md` v0.6 + `ADR-0009`(proposed)
> + `MVP.md §5.3/§6.2/§6.3` + 代码现状。输出 = 现状打分 + 文档/代码不一致清单 + 风险 + P0 工作量分层。
>
> 下游：[`impl-planning.md`](impl-planning.md)（Stage 3）→ [`impl-tasks.md`](impl-tasks.md)（Stage 4）。
> 方法：全部结论**对照代码取证**（`file:line`）。**未改动任何实现代码。**

---

## 0. 一句话结论

**D1 结算骨架已实现且参数已同步（本会话 `a348a1d`）；但"激励层"（Layer 0/1 分池、tenure、
PoSR 绑定、claim CLI/Web）几乎全是**文档已定、代码为零**。已实现的是一台通用记账机；
缺的是**这台机器缺少的那半张经济设计图**。

### 0.1 ⚠️ 完成度账本（2026-10-08 复核 —— 别把"P0 done"读成"incentive done"）

后续 IMP-1..5 让 Layer 1 的**代码链路**闭环了。**但 `incentive` 整体远未完成：**

| 层 | 内容 | 代码状态 |
|---|---|---|
| **Layer 1（工作奖励）** | `mine→work→settle→points→balance root→claim proof` | ✅ **已端到端闭环**（单池）|
| **Layer 0（节点经济）** | **两池/phase 分配** | ❌ **未实现** —— `settle` 是**单池**（grep `pool|phase` 空）|
| | **tenure tier 加权份额** | ❌ **未接线** —— `internal/tenure` **只有库、无生产调用方** |
| | **"合格 epoch" 的【输入源】** | ❌ **不存在** —— 依赖 liveness 协议（未设计，见 §Layer 0 设计）|
| | **PoSR 绑定 + 1.25× 乘数** | ❌ **无 `bind` 命令、无乘数** |
| **参数/设计** | X 值、SBT 发放/吊销、sunset、P0 sink、Claim UX、外层 bridge | ❌ 未决（`incentive.md §10`）|
| **上线门槛** | **VPS farm ROI 建模** | ❌ **未启动**（`incentive.md §10.3` 自标 launch-blocking）|

**一句话**：**Layer 1 记账链路可用；Layer 0 几乎全空 —— 且先于"接线"缺的是【设计】，不是代码。**

---

## 1. D1 结算：实现到什么程度？

| 环节 | 状态 | 证据 |
|---|---|---|
| `work` 计量（BASE×verified×novelty×diversity）| ✅ 已实现 | `internal/scoring/score.go`（`v.Work = BasePoints * ...`，L117）|
| `RecordWork`（记 work，**不记 points**）| ✅ 已实现 | `score.go:134`；写 `WorkRecord`（含 artifactKey，可审计）|
| work 账本（SQL，按 epoch 聚合）| ✅ 已实现 | `internal/store/ledger.go` `SQLWorkLedger`（L251）、`Totals(epoch)`（L294）|
| `Settle` = `Allocate` + `CapAllocation`（纯函数）| ✅ 已实现 | `internal/scoring/emission.go`（`Settle` / `Allocate` / `CapAllocation`）|
| 5% cap（`B(n)×5%`）| ✅ 已实现 | `PerAgentCap` / `CapAllocation`；`PerAgentCapFraction = 0.05` |
| cap 余量 → **销毁**（不增发）| ✅ 已实现（**涌现性质**）| `CapAllocation` 把 >cap 的份额**截断丢弃**，`TotalAllocated` 可 < 预算 → 即"不写、不增发"。**无独立开关**，是截断的自然结果 |
| `relayfirst settle`（显式、幂等）| ✅ 已实现 | `cmd/relayfirst/main.go:916` `runSettle`；幂等键 `settle:<epoch>:<agent>`（`scoring_sink.go:214`）|
| 结算幂等（重跑不重复记）| ✅ 已实现 | `Finalize`（`scoring_sink.go:177`）+ points 账本主键 |
| 参数 **`epoch=7天` / `B0=7,000,000` / `decay=0.85^⌊n/4⌋`** | ✅ **已接线**（本会话 `a348a1d`）| `emission.go`：`EpochLength = 7*24h`、`BaseBudget = 7_000_000`、`Decay = 0.85`、`DecayPeriod = 4`；`EpochBudget = B0 * Decay^⌊n/4⌋` |
| `MVP.md §6.2` 与代码一致 | ✅ 已同步 | `MVP.md §6.2` 已改为 7 天 / 7M / 0.85^⌊n/4⌋ |
| Merkle root 来源 | 🔶 **不一致** | `anchorRoot`/`epochReceipts`（`main.go:1764,1789`）建的是 **receipt 树**（`merkle.IDFromHex(receiptID)`），**不是** "settled map → root"。`Finalize` 注释承诺"调用方可据 settled 建 root"，但**无生产代码这样做** |

**小结**：D1**已解决的**是"work→points→账本"这条链 + 参数。**未解决的**是 S7 的
"settled 数字 → Merkle root → 发布"——今天 root 建在 receipt 上，与 `incentive.md` 说的
"settled map 即 root 输入"**不是同一个东西**。

### 1.1 旧值残留排查（0.99^n 等）

| 位置 | 状态 |
|---|---|
| `BaseBudget` / `Decay` / `DecayPeriod` | ✅ 已改 |
| `EpochLength`（scoring / verification / mining）| ✅ 已改（`verification` 按值对齐，`mining` 改为引用 `scoring.EpochLength`）|
| 测试（emission/settle/epoch bounds）| ✅ 已改为新形状（阶跃）|
| **历史叙述**（`incentive.md §10.1` 摊余、changelog、`MVP.md` 修订说明）| 🟡 **故意保留**（记录沿革，非活参数）|

**结论：无功能性旧值残留。** 0.99^n 只出现在"这就是旧值"的说明文字里。

---

## 2. 文档 ↔ 代码：不一致清单（逐条）

> 规则：只指出问题，不自行发挥。

| # | 不一致 | 文档 | 代码 | 性质 |
|---|---|---|---|---|
| **A** | **ADR-0009 D7 三方矛盾** | `ADR-0009 D7`：`bind` **拆出、未定义**（"与节点无密钥冲突，需单独设计"） | `incentive.md:177` / `user-registration-flow.md:86`：**引用 "ADR-0009 D7"**，声称"机制已定"（delegation-1 意向 + assertion-1 确认） | **文档内部矛盾**。D7 标题是"拆出"，被引为"已定"。**绑定机制目前"无处定义"** |
| **A'** | 同文件**自相矛盾** | `user-registration-flow.md:86`（"机制已定"）vs **`:145`**（"❌ PoSR bind 重设计，机制待定"） | — | 同一文件两处互斥 |
| **B** | **两池模型不在 L0** | `incentive.md §3/§4`：epoch 预算拆 **Work pool (50/75/90%) + Node pool (50/25/10%)** | `MVP.md §6.2`：**单一预算 `B(n)`**，无分池、无 phase 比例 | **L0 缺口**。分池/phase 是 `incentive.md` 新增，MVP 未定 → 若要实现须先改 MVP（§5.1）|
| **C** | **PoSR 乘数未接线** | `incentive.md §4`：`m_i = 1.25`（绑定）| `Params`（`score.go:59`）**无 multiplier 字段**；`RecordWork` 不乘 `m_i`；无 `bind` 命令 | 机制未实现 |
| **D** | **tenure/tier 无实现** | `incentive.md §3`：tenure ≥3 epochs、tier 1×/1.1×/1.25×、slot 心跳 | 全仓**无** `tenure`/`tier`/`slot` 代码 | 机制未实现 |
| **E** | **root 来源不一致** | `incentive.md`：Merkle root 输入 = **settled map** | 生产 `root` 建在 **receipt 树** 上（`main.go:1764`）| 已实现物与承诺不符 |
| **F** | **claim 未实现（CLI/Web）** | `incentive.md §4`：`relayfirst claim`（CLI 优先）+ web ticket 配对 | CLI **无 `claim` 命令**；合约侧 S11 claim 已做（不同层）| 未实现 |
| **G** | **显示层未实现** | `incentive.md §4`：积分显示"每分钟刷新"、区分**已结算 vs 待结算预估** | `status` 只读 points 账本；**无** work 聚合预估 | 未实现 |
| **H** | **版本号自相矛盾** | `incentive.md` 头写 **"v0.1 — 框架版"** | 同文件 changelog 到 **v0.21** | 文档元数据过时 |
| **I** | **incentive 未进 TASKS** | `incentive.md` 自称 "dev-blocking" | `TASKS.md` **无**任何 incentive 实现任务（tenure/PoSR/池/claim）| 规划条目缺失 |
| **J** | **快照摊余描述模糊** | `incentive.md §10.1`："首年约 1.64 亿（28M × …）" | 7M/epoch × 52 ≈ 3.64 亿原始；摊余后 ≈1.6–1.7 亿 | 不算错，但算式对读者不透明 |

---

## 3. 风险点

### 3.1 与 CI 门禁的冲突点

| 门禁（`scripts/ci.sh`） | 风险 | 说明 |
|---|---|---|
| **节点不得链接 crypto**（ADR-0004）| 🟡 中 | Layer 0 的 tenure/心跳若被塞进 `relayfirst-node`，会拖入 `scoring`/签名 → **门禁 FAIL**。tenure 记账**必须在 node 之外**（settler/verifier 侧）|
| **verifier 不得链接 mining/scoring** | 🟡 中 | PoSR 绑定要用 `assertion-1`（verifier 侧）—— verifier 现在**不能** import `scoring`。绑定的"确认"只能走 `assertion`，**不能**顺手引 scoring |
| **MCP 零密码学**（ADR-0008）| 🟢 低 | claim 的签名在 CLI，MCP 不碰 |
| **A3 `CGO_ENABLED=0`** | 🟢 低 | 无新增 CGO 需求 |

### 3.2 缺失模块（全无实现）

```
tenure 记账        （连续在线、降档/清零）
tier 权重          （1×/1.1×/1.25×）
slot 心跳 + liveness challenge（协议未设计）
Layer 0 node pool + phase 比例  （L0 未定）
PoSR 乘数 m_i=1.25 + bind 命令
claim（CLI 命令 + Merkle proof 生成 + web ticket 配对）
settled-map→root + root 发布
待结算预估显示层
```

### 3.3 难度与依赖

| 模块 | 难度 | 主要成本 |
|---|---|---|
| settle 公式接线 | 低 | 主要已做；剩 settled→root |
| 5% cap + 销毁 | 低 | 已实现（验证即可）|
| tenure/tier 记账 | 中 | 状态机 + 时间窗口 + slot 心跳；**口径要先定**（§3.3 风险）|
| PoSR bind | 中 | 新域签名组合（delegation-1 + assertion-1）；**机制无处定义**（清单 A）|
| claim CLI | 中 | Merkle proof 生成 + payout 地址 + `--to` |
| claim Web（ticket）| 中 | 一次性 ticket + 只读 API；**不碰 key** |
| liveness challenge | 中-高 | 协议**未设计**；防游戏参数（X slots）未定 |
| farm ROI 建模 | 高（非代码）| 经济建模，**launch-blocking** |

---

## 4. 结论：P0 任务分层与工作量

> "P0" 定义：**可开工**（不被 D2/no-init、不被 L0 未定阻塞）。

### 4.1 "改参数"类（小，≤ 1 天）

| 任务 | 工作量 | 备注 |
|---|---|---|
| ~~settle 公式接线 / 常数~~ | ✅ 已完成 | `a348a1d` |
| 5% cap + 销毁 **验证**（补一条"cap 触发时总量<预算且不增发"的测试）| 0.5 天 | 行为已对，缺显式断言 |
| 文档修正（清单 A/A'/H/I：D7 引用、版本号、TASKS 登记）| 0.5 天 | **纯文档** |

### 4.2 "新写模块"类

| 任务 | 工作量（天）| 是否受 L0 阻塞 |
|---|---|---|
| settled-map → Merkle root + 发布接线 | 1–2 | 否（E 是既有物与承诺不符）|
| 待结算预估显示层（work 聚合）| 1 | 否 |
| tenure 记账（连续在线 + 降档/清零）| 2–3 | **是** —— slot 参数/challenge 未定；**且要先定口径** |
| tier 权重（1×/1.1×/1.25×）| 1 | 否（但依赖 tenure）|
| Layer 0 node pool + phase 比例 | 2 | **是** —— MVP 无分池（清单 B）→ 先改 L0 |
| PoSR 乘数 + `bind` | 2–3 | **是** —— 机制无处定义（清单 A）|
| `relayfirst claim`（CLI）| 2 | 否（合约侧已在）|
| claim Web ticket 配对 | 2–3 | 否（但属 post-launch 体验）|

**合计（不含 launch-blocking 的 farm ROI 建模）**：P0 可开工约 **8–12 天**；
受 L0/机制未定阻塞的约 **6–8 天**。

---

## 5. 给 Stage 3 的输入（不预定结论）

1. **必须先修文档**再谈实现：清单 **A/A'/H/I** 是"文档问题"，按约束**先指出**。
2. **L0 缺口**：清单 **B（分池）/ E（root 来源）** —— 实现前须走 `MVP.md` 先行。
3. **D2 未批**：`init`/keystore/signer/`bind` 中**至少 `init`/keystore/signer 属 BLOCKED**；
   `bind` 还额外卡在机制未定义（清单 A）。
4. **可立即并行**：settled→root、显示层、claim CLI、cap 测试断言。
