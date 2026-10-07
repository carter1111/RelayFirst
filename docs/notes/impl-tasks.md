# 实现任务清单：激励机制落地（Stage 4）

> **输入**：[`impl-planning.md`](impl-planning.md)（Stage 3）。**可直接派给 coding agent。**
> 每条：目标一句 / 涉及文件 / 验收（含测试）/ 依赖。按依赖排序，标注可并行。
>
> **全局红线（每个任务都适用）**：不碰 ADR-0004/0007/0008 硬性质；不违反 A1–A8；
> `CGO_ENABLED=0` 可构建；不越过 D2/no-init；不重议任何 ✅ 锁定项。

---

## 依赖图

```text
DOC-1..4（文档，无依赖）──────────────┐
L0-1..2（MVP 修订，无依赖）───────────┤
                                      ▼
IMP-1 (root 接线)     IMP-2 (cap 断言)
IMP-3 (tenure)        IMP-4 (claim CLI)     IMP-5 (显示层)
   ↑需 PRE-1 口径         ↑无             ↑无         ↑各自并行
                                      │
                                  TEST-1 (汇总门禁)

BLOCKED：D2 批前不派（见末节）
```

---

## A. 前置：文档修正（先做，纯文档，可并行）

### DOC-1 — 修正 ADR-0009 D7 的三方矛盾 ✅ **已裁决 + 已修（2026-10-08）**
- **目标**：让 `bind` 的"是否已定义"在仓库里只有一个答案。
- **裁决**：**选 (iii)** —— D7 已改写收录（delegation-1 意向 + assertion-1 确认）；无独立文档；两处引用现有效。
- **验收**：✅ D7 改写；`incentive.md:177` / `user-registration-flow.md:86,145` 一致（:145 已改为"已定"）；ADR-0009 后果 #6 已同步。
- **依赖**：无。

### DOC-2 — `incentive.md` 版本号
- **目标**：头版本 "v0.1" → "v0.21"（与 changelog 一致）。
- **文件**：`docs/notes/incentive.md:3`
- **验收**：头版本 == changelog 最新版本。
- **依赖**：无。

### DOC-3 — 把 P0 登记进 `TASKS.md` ✅ **done（2026-10-08）**
- **目标**：incentive 的实现任务在唯一任务源可见。
- **文件**：`TASKS.md`
- **动作**：新增 `## 14. 激励实现`，列 IMP-1..5 + TEST-1 + P1 + BLOCKED。
- **验收**：✅ TASKS 有 §14；与本文 id 对齐（IMP-1..5/TEST-1）。
- **依赖**：无。

### DOC-4 — 澄清 root 定义（前置 L0-1 的说明文）
- **目标**：文档层面统一"root 输入 = settled map"。
- **文件**：`docs/notes/impl-assessment.md`（已记）、`MVP.md`（见 L0-1）
- **依赖**：与 L0-1 合并。

---

## B. 前置：L0 修订（**须先改 `MVP.md`**，§5.1）

### L0-1 — MVP 明确两个 root 的命名与分工（**两 root，各司其职**）
- **目标**：`MVP.md` 写清 **balance root**（settled map，叶 `keccak256(agentId‖total‖epoch)`）
  与 **receipt root**（回执集，叶 = receipt ID）**分开、不混**。
- **文件**：`MVP.md`（§S7 / §5.3 / §6.2）
- **动作**：给两个 root 命名并写用途：**balance root → claim 验积分**；**receipt root → 包含性（可发现）**。
- **验收**：MVP 有两个 root 的定义；IMP-1 据此建 balance root（receipt root 已有）。
- **依赖**：无（纯文档）。**改 L0 前须你确认**（属范围/定义）。—— **已裁决（2026-10-08）：赞成两 root**

### L0-2 — MVP 明确双池 + phase 比例（**若决定实现 Layer 0 分配**）
- **目标**：把 `incentive.md` 的 Work/Node pool + 50/75/90 ∶ 50/25/10 落进 L0。
- **文件**：`MVP.md`（§6.2）
- **验收**：MVP 表述与 `incentive.md` 一致。
- **依赖**：无。**注**：P0-3 只做 tenure **记账**，不等它；**分配**才需 L0-2。
- **定性**：**纯同步工作**（不重议）。
- **备注**：**范围变化，必须先改 MVP，不得只在代码里改。**

---

## C. P0 实现任务

### PRE-1 — 裁决"qualified 如何喂入 tenure" ✅ **已裁决（2026-10-08）：选 (a)**
- **结论**：tenure **只吃外部"合格 epoch"布尔值**；**slot 门槛逻辑放 P1**（与 liveness 协议一起）。P0 的 tenure 模块保持**干净接口**。
- **影响**：IMP-3 据此实现 `Advance(prev, qualified bool)`，**不**内建 slot 测量。
- **依赖**：无。**已解除 IMP-3 阻塞。**

### IMP-1 — settled map → Merkle root 接线
- **目标**：`settle` 后据 settled 数字产出 root，供离线验证。
- **文件**：`cmd/relayfirst/main.go`（`runSettle`）；`internal/merkle/`（如需叶辅助）
- **接口**：`func RootFromSettled(epoch uint64, settled map[string]float64) (merkle.Hash, error)`
- **硬约束**：叶 = `keccak256(agentId ‖ total_micro ‖ epoch)`（对齐 `RelayPoints._leaf`，**含 epoch**）；**agent 排序**（canonical）。
- **验收（测试）**：
  1. 同输入两次 → 同 root（确定性，防 map 乱序）。
  2. 叶编码与 `contracts/` 一致（跨语言向量或复用 `RelayPointsAnchor.t.sol` 的哈希）。
  3. 同 leaf 在 epoch 10 vs 20 → 不同 root（防重放）。
  4. `relayfirst settle` JSON 新增 `root` 字段（非 TTY 稳定）。
- **依赖**：L0-1。

### IMP-2 — 5% cap + 销毁 的显式断言
- **目标**：把"cap 截断 = 销毁 = 不增发"钉成测试。
- **文件**：`internal/scoring/emission_test.go`
- **验收（测试）**：
  1. 单 agent 占 20% work → 其 points == `PerAgentCap(epoch)`。
  2. `TotalAllocated(settled) < EpochBudget(epoch)`（有截断时）。
  3. 无第二次写入（`settle` 重跑不改变 points；已由 `settle:<epoch>:<agent>` 保证 —— 断言之）。
- **依赖**：无。**可与 IMP-1/3/4/5 并行。**

### IMP-3 — tenure 状态机 + tier 权重（记账，非分配）
- **目标**：由"合格 epoch 序列"推 tenure 与 tier。
- **文件**：新建 `internal/tenure/tenure.go`；`internal/store`（新表 `tenure`）
- **接口**：
  - `func Tier(tenureEpochs int) float64` → 3–5:1× / 6–11:1.1× / 12+:1.25×
  - `type State struct { Tenure int }`
  - `func Advance(prev State, qualified bool) State`（合格 +1；单次不合格 −1（降档，不清零）；连续两次 → 0）
- **CI 约束**：包**无 crypto、无 scoring 导入**（可被抓任何二进制引用）。
- **验收（测试）**：
  1. +1 / 降档 / 连续两次清零。
  2. 边界：2→3、5→6、11→12 的 tier 切换。
  3. 持久化：写读一致；幂等（同 (node,epoch) 不重复计）。
- **依赖**：**PRE-1**（输入契约）。

### IMP-4 — `relayfirst claim`（证明生成 + 展示，不碰 key）
- **目标**：给定 (agent, epoch) 产出可验的 Merkle proof。
- **文件**：`cmd/relayfirst/main.go`（新 `claim`）；复用 `internal/merkle.Proof`
- **接口**：`relayfirst claim [--epoch N] [--agent 0x…] [--to 0x…] [--web]`
- **输出**：`{agentId, epoch, total, proof:[…], root}`（JSON；非 TTY 冻结）
- **边界**：**证明生成不需密钥**（读本地账本）→ 可做；**链上 tx 提交 defer**（需 signer，BLOCKED）。
- **验收（测试）**：
  1. 产出的 proof 过 `merkle.Verify`。
  2. 非 TTY JSON 键稳定；无 ANSI。
  3. 无密钥读取路径（断言不经 signer）。
- **依赖**：IMP-1（root 定义）。

### IMP-5 — `status` 待结算预估（显示层）
- **目标**：区分"已结算 points"与"本 epoch 待结算预估（work 聚合）"。
- **文件**：`cmd/relayfirst/main.go`（`status` TTY 路径）
- **接口**：读 `SQLWorkLedger.Totals(epoch)`，**不写**。
- **验收（测试）**：
  1. 非 TTY 输出**保持今天 JSON**（字段不变）—— `TestStatus_NonTTYStaysJSON` 已锁，扩展断言。
  2. TTY 面板含"待结算预估"行并标注"未结算"。
  3. 读路径无写入（无 `Record`/`Credit` 调用）。
- **依赖**：无。**可与 IMP-1..4 并行。**

---

## D. P1（**不进本批工期**，列出备查）

| id | 目标 | 依赖/阻塞 |
|---|---|---|
| P1-1 | liveness challenge 协议 + slot 参数 | 协议未设计 |
| P1-2 | VPS farm ROI 建模 | **launch-blocking（上线门槛）** |
| P1-3 | SBT 发放/吊销规则 | 规则未定 |
| P1-4 | omission 强制纳入机制 | 机制未设计 |

---

## E. BLOCKED（**D2 未批不得派**）

| id | 目标 | 阻塞 |
|---|---|---|
| BLK-D2-1 | `relayfirst init` | D2 |
| BLK-D2-2 | keystore 存储 | D2 |
| BLK-D2-3 | signer daemon | D2（且已裁定 v2 延后）|
| BLK-D2-4 | `relayfirst bind` | D2 **且** 机制未定义（DOC-1）|

> **规则**：D2 未批 → 仅存于文档，不落代码。

---

## F. 最终门禁

### TEST-1 — 全量验证
- **目标**：所有 P0 合并后 CI 全绿。
- **命令**：`CGO_ENABLED=0 go build ./...`、`go test ./...`、`go test -race ./...`、`bash scripts/ci.sh`
- **验收**：全过；导入图门禁仍成立（node 无 crypto；verifier 无 mining/scoring；dashboard 只读）。
- **依赖**：IMP-1..5 全部。
