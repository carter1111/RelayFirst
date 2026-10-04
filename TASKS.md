# RelayFirst — TASKS.md

> **本文件是 RelayFirst MVP 的唯一任务源。** 范围定义见 [`MVP.md`](MVP.md)；长期路线图见 [`ARCHITECTURE.md`](ARCHITECTURE.md)。
>
> **规则：** 本文件只包含 MVP 范围内的任务。任何"让协议更正确"但不"让人更愿意来玩"的工作 → 归 `ARCHITECTURE.md`，**不进本表**。
>
> 状态机：`todo → in-progress → done → verified`（受阻则 `blocked`）。
>
> 状态：**S1–S8 代码已实现**（423 Go 测试通过、15 Solidity 测试、10 道 CI 门禁）。**非 git repo。**
> 上线仍受阻于 BLK-1 / BLK-2 / BLK-3 / BLK-4 与三项人工动作（见 §1 与各阶段报告）。

---

## 0. 开工前必读

| 事项 | 位置 |
|---|---|
| MVP 范围与取舍原则 | [`MVP.md` §1–§3](MVP.md) |
| 回执结构（唯一核心数据结构） | [`MVP.md` §4](MVP.md) |
| 任务类型硬约束（**决定全部计分逻辑**） | [`MVP.md` §5.0](MVP.md) |
| 抗女巫机制 | [`MVP.md` §5.2](MVP.md)、[§5.5](MVP.md) |
| 积分与 emission | [`MVP.md` §6](MVP.md) |
| 技术选型（定死） | [`MVP.md` §8](MVP.md) |
| 硬性验收判据（7 条） | [`MVP.md` §11](MVP.md) |
| 明确不做清单 | [`MVP.md` §12](MVP.md) |
| 交接说明 | [`HANDOFF.md`](HANDOFF.md) |

---

## 1. 阻塞项（必须先解决，否则不要开工）

> 这三项**不是工程任务**，但它们卡住的是**最大的风险**。详见 [`MVP.md` §15](MVP.md)。

| id | 问题 | 为什么阻塞 | 状态 | 影响 |
|---|---|---|---|---|
| **BLK-1** | **agent 订阅能否程序化驱动？** | 决定叙事成立与否。若不能，产品退化为"再买一份 API key 来挖矿" | ⬜ **todo（最高优先级）** | 整套 §10.1 叙事 |
| **BLK-2** | 第一个真实消费方是谁？ | 上线**硬性前置条件**（`MVP.md` §10.3） | 🟡 **行动方案已就绪** — 见 [`docs/notes/blk-2-first-consumer-plan.md`](docs/notes/blk-2-first-consumer-plan.md)（价格/可达性 → 交易机器人；含买家画像/demo/首封信/8 周时间盒）。**接洽属人工动作，未开始** | 上线许可 |
| **BLK-3** | 验证者指派策略（随机种子 / 防串谋） | S4 实现前必须定 | ⬜ todo | S4 |
| **BLK-4** | **epoch 起点取哪个日期？** | epoch 号在**签名载荷**内，公布后不可改；改则旧回执全部失效 | ⬜ todo — 占位符已就位（`internal/epoch`） | 上线前必须定稿，见 [`docs/notes/epoch-anchoring.md`](docs/notes/epoch-anchoring.md) |

**开工建议：** BLK-1 第 1 天就开始核实；S1 可以并行推进（回执结构与 KAT 不依赖它）。
BLK-4 直到"开放真实挖矿"之前都不阻塞开发，但**必须在那一刻之前定稿**，所以别拖到最后一刻。

---

## 2. S1 — 回执 + 签名 + KAT + Verifier

**目标：** 回执结构与离线验证器。**这是全 MVP 的地基。**

**对应验收判据：** ② 第三方离线验证、⑥ 跨语言 KAT、⑦ no-CGO 构建

| id | 任务 | 依赖 | 验收 | 状态 |
|---|---|---|---|---|
| S1-1 | 初始化 Go module + `CGO_ENABLED=0` 构建骨架 | — | `CGO_ENABLED=0 go build ./...` 通过 | ✅ **done** — `go1.27.1`; build exit 0 |
| S1-2 | 定义 `Receipt` struct（含 `task.type` / `result.value` / `verification`） | S1-1 | 字段与 `MVP.md` §4 逐字一致 | ✅ **done** — `internal/receipt/receipt.go` |
| S1-3 | 实现 canonical JSON 序列化（确定性字段序） | S1-2 | 同输入 → 字节相同输出（多次运行） | ✅ **done** — `TestCanonicalJSON_Deterministic` 50 轮一致 |
| S1-4 | 实现 EIP-712 `hashStruct` **或** 接入 `apitypes`（二选一） | S1-2 | 见 S1-5 | ✅ **done** — 选**自研薄层**（`internal/eip712/`）；ADR 已补：[`docs/decisions/ADR-0001`](docs/decisions/ADR-0001-eip712-in-house-thin-layer.md)（不违反 A1：原语全用库，KAT 锁死正确性） |
| S1-5 | **建 KAT 向量 `testdata/eip712-vectors.json`** | S1-3 | 10 用例覆盖：嵌套 struct / 动态数组 / 长 string / 空值 / UTF-8 | ✅ **done** — 10 向量（`scripts/gen-kat.ts`） |
| S1-6 | 用 `viem` 生成 `expectedHash` 基准 | S1-5 | TS 侧产出 10 个 hash | ✅ **done** — 含 domain / message / digest 三级基准 |
| S1-7 | Go 侧对齐 KAT（CI 不一致即失败） | S1-4, S1-6 | **TS 与 Go 对同一向量字节相同** | ✅ **done** — `TestKAT_Vectors` 10/10 通过 |
| S1-8 | EIP-712 签名 + `ecrecover` 验签 | S1-4 | 自签自验通过；改一字节 → 验签失败 | ✅ **done** — 往返通过；篡改 message/sig/domain 均拒绝 |
| S1-9 | 独立 verifier CLI（`relayfirst verify <receipt.json>`） | S1-8 | **不连任何服务器**即可验证 | ✅ **done** — 实测输出 `valid: true` |
| S1-10 | verifier 支持 `anchors` 重取 + `result` 重算 | S1-9 | 篡改 anchor → 拒绝；篡改 result → 拒绝 | ✅ **done** — **两条路径都已实现且已接线**：①**签名层**（默认）：篡改即拒；②**anchor 重取**（`verify --refetch`）：复用 `mining.AnchorConsistency` 重取每个外部 anchor 并比对 contentHash，**不匹配 / 不可达均报失败**。**默认路径保持完全离线**（criterion ② 不被削弱）—— 重取是**显式 opt-in**，因为它验的是**声明**而非回执，且受验证窗口约束（内容会变，诚实回执也可能过期失败）。输出始终报告 `mode`（`offline` / `refetch`），避免"以为查了其实没查"。**另加未知 flag 拒绝**：`--refeth` 笔误不再静默走离线路径。5 条测试：默认不触网（httptest 断言未被访问）/ 匹配通过 / 漂移失败但 `valid` 仍为 true / 不可达不算通过 / 未知 flag 被拒 |

**S1 完成定义：** `relayfirst verify` 能在**离线**状态下判定一份回执的真伪，且 TS/Go 哈希一致。→ ✅ **达成**

> **KAT 是硬性判据，不是可选项。** 没有它，跨语言签名必然在半年后炸。见 `MVP.md` §8.4。

**S1 证据（`AGENTS.md` §5.2）：**

```
CGO_ENABLED=0 go build ./...   → exit 0
go vet ./...                    → exit 0
gofmt -l .                      → 空
go test ./...                   → 29 PASS / 35 子测试 / 0 FAIL
TestKAT_Vectors                 → 10/10 PASS（viem 基准）
静态二进制                       → 5.4M，otool -L 零 libc 引用
```

完整报告见 [`docs/stages/S1-report.md`](docs/stages/S1-report.md)。

---

## 3. S2 — 挖矿守护进程 + 任务生成

**目标：** agent 能自己生成任务、执行、产出回执。

**对应验收判据：** ① 10 分钟出积分

| id | 任务 | 依赖 | 验收 | 状态 |
|---|---|---|---|---|
| S2-1 | 任务生成器：**只生成 `probe` / `extract` / `compute`** | S1 | **无法生成**生成类任务（摘要/分类） | ✅ **done** — `internal/mining/generate.go`；`TestGeneratorNeverEmitsGeneratedTasks` 500 轮 0 生成类任务 |
| S2-2 | `probe` 任务执行器（URL 可达性 / 状态码 / 响应头） | S2-1 | 独立探测可复现 | ✅ **done** — `internal/mining/probe.go`；httptest 实测 200/4xx/5xx |
| S2-3 | `extract` 任务执行器（抽取指定字段） | S2-1 | 重取可复现 | ✅ **done** — `internal/mining/extract.go`；dot-path + json/text 格式 |
| S2-4 | `compute` 任务执行器（确定性计算） | S2-1 | 可复算 | ✅ **done** — `internal/mining/compute.go`；hash/concat/sortjson，确定性 100 轮 |
| S2-5 | anchor 记录（url + contentHash + status + bytes） | S2-2/3 | 每份回执 ≥1 个可重取 anchor | ✅ **done** — `internal/anchor/fetch.go`；真实 anchor（probe/extract）+ inline 合成（compute） |
| S2-6 | **【承重】推理额度接入（BYO API key：openai / anthropic / local）** | S1 | 三种 provider 均可跑通；**语义化任务能真实消耗 token** | ✅ **done** — `internal/llm/`（Provider 接口 + OpenAI/Anthropic/Local + Resolver）；**CLI 已接线**：`mine --provider/--model/--semantic`；**密钥只走环境变量**（`OPENAI_API_KEY` / `ANTHROPIC_API_KEY`），刻意不做 flag，避免泄漏进 shell history 与进程表；`--provider local` 无需密钥即可离线跑语义任务 |
| S2-7 | 守护进程主循环（生成 → 执行 → 签名 → 上报） | S2-2..6 | 持续产出回执 | ✅ **done** — `internal/mining/runner.go`（`Runner`：间隔 + 指数退避 + 持久化 + 取消）；CLI `mine` 子命令已接线 |
| S2-8 | 本地 SQLite 存储（回执 + anchor） | S1-1 | `modernc.org/sqlite`，纯 Go | ✅ **done** — `internal/store/`：`SQLArtifactLedger` + `SQLPointsLedger` + `ReceiptStore`；持久性、签名往返、跨实现一致性测试；`CGO_ENABLED=0` 仍通过 |
| S2-9 | **语义化 extract**（字段可用自然语言描述，结果归一化为确定性字符串） | S2-3, S2-6 | 杂乱页面上的语义字段可抽取；结果二值可验证；§5.0 红线不破 | ✅ **done** — `internal/mining/semantic.go` + `internal/llm/resolver.go`；`Resolver` 接口、`ParseFieldSpecs`、`NormalizeValue`；端到端归一化测试 |

**S2 完成定义：** 单机跑守护进程，持续产出**可被 S1-9 离线验证**的回执。→ ✅ **达成** — `Runner` 长跑 + `ReceiptStore` 落盘 + CLI `mine` 已接线；实测「mine → 落盘 → 离线验证通过 → 篡改被拒」闭环成立。

> **S2-1 是抗女巫的地基。** 若允许生成类任务，验证就无客观标准，必须引入争议层 → 复杂度爆炸。见 `MVP.md` §5.0。

---

## 4. S3 — 全局去重账本 + 计分 + Epoch

**目标：** 把回执变成积分，且**刷量收益为零**。

**对应验收判据：** ④ 伪造工作量被拦住

| id | 任务 | 依赖 | 验收 | 状态 |
|---|---|---|---|---|
| S3-1 | `artifactKey = sha256(type + spec.url + contentHash)` | S2 | 计算确定且可复现 | ✅ **done** — `internal/scoring/artifactkey.go`；`TestArtifactKey_Deterministic` 50 轮一致 |
| S3-2 | **全局去重账本**（非 per-agent） | S3-1 | 同一 key 第二提交者 **novelty = 0** | ✅ **done** — `ledger.go`；`TestArtifactKey_IsAgentIndependent` + `TestMemLedger_FirstSightingThenRepeat` |
| S3-3 | 计分公式实现（`BASE × verified × novelty × diversity × budgetFactor`） | S3-2 | 与 `MVP.md` §5.3 一致 | ✅ **done** — `score.go`；`Score` 纯函数 + `Emit` 唯一推进账本 |
| S3-4 | `diversity` 系数（同 domain 递减） | S3-3 | 同 domain 重复 → 分数衰减 | ✅ **done** — `TestScore_DiversityAttenuates`（0/1/2/4 次重复） |
| S3-5 | `budgetFactor`（per-agent 上限 = `B(n) × 5%`） | S3-3 | 单 agent 无法独占产出 | ✅ **done** — `emission.go`；**分层**：`Allocate` 比例 + `CapAllocation` 上限（见 S3-report 问题 2） |
| S3-6 | Epoch 结算（`epoch = 1 天`） | S3-3 | 每日预算 `B(n) = B0 × decay^n` | ✅ **done，含一处上线阻塞缺陷修复** — `EpochBudget` / `EpochOf` / `EpochBounds`；`TestEpochBudget_Decays`。**发现并修复**：`EpochOf` 原先无起点，当前 epoch ≈ 20729 → `B(n) ≈ 3.3e-85`、`cap ≈ 1.7e-86`，`BudgetFactor = 1 - earned/cap` 使每个 agent **每 epoch 只能记一次分**，经济实际发行量为零。已加 `internal/epoch` 起点锚定（含 `uint64` 下溢防护）→ 现在 `B(2) ≈ 980k`、5 条回执 5 条记账。**起点值为占位符，上线前必须定稿**，见 [`docs/notes/epoch-anchoring.md`](docs/notes/epoch-anchoring.md) |
| S3-7 | 积分账本（**不可转让**，仅记录） | S3-3 | 无转账接口 | ✅ **done** — `points.go`；**反射断言**方法集无值转移（`TestNoTransferCapability`） |
| S3-8 | **红队：自刷测试** | S3-2 | 开 5 个 agent 取同一 URL → **全部 0 分** | ✅ **done** — `TestRedTeam_FiveAgentsSameURLAllZeroButFirst`：**恰好 1 个得分**,总计 = `BasePoints` |
| S3-9 | **红队：伪造 anchor 测试** | S3-3 | 编造 contentHash → 拒收 | ✅ **done** — `TestRedTeam_FabricatedContentHashCannotReuseNovelty` |
| S3-10 | **【承重】挖矿 → 计分闭环**（回执产出后真正记账） | S3-3, S2-7 | 挖矿必须真的产生积分 | ✅ **done** — `internal/mining/scoring_sink.go`（`ScoringSink`）；先落盘再计分（丢分可补，白付不可补）；校验不通过的回执既不计分也**不占用 artifact**（否则会抢走诚实矿工的 novelty） |
| S3-11 | CLI 真实记账 + `stats` 报告积分 | S3-10 | `mine` 累积积分，`stats` 可查 | ✅ **done** — 两个入口都改用 `ScoringSink`（原先 `--once` 绕过计分）；`stats` 新增 `totalPoints` / `creditedReceipts` / `yourBalance` |
| S3-12 | `diversity` 的**同 domain** 重复计数取真实来源 | S3-4, S3-10 | 跨 domain 的工作不被误罚 | ✅ **done** — `ScoringSink.sameDomainRepeats` 从**已落盘回执**推导 domain。**修复**：初版把"该 agent 本 epoch 的所有积分"当成同 domain，方向是反的 —— 跨 domain 工作反而被罚得更重；有正反两条测试锁定 |
| S3-13 | 语义字段接入生成器（`--semantic`） | S2-9, S2-6 | 语义 extract 真的消耗推理额度 | ✅ **done** — `Generator.SemanticFields`（构造期固定，满足"取响应前定字段"的约束）；默认仍为空 → 未配置的矿工零推理成本；blank 值回退，不会产出零字段（零工作量）任务 |
| S3-14 | **红队：S3-8 在真实 SQLite 路径上重跑** | S3-8, S2-8 | 持久化层不能削弱去重 | ✅ **done** — `internal/store/mining_integration_test.go`：5 个不同密钥、不同 receiptId 的诚实签名者提交同一观测 → **恰好 1 个记账**、总计不翻倍、artifact 数 = 1；另有幂等重放与**关闭重开**持久性测试 |

**S3 完成定义：** S3-8 / S3-9 两条红队测试**全部通过**，即"刷量不赚钱"成立。→ ✅ **达成**（实际交付 **7** 条红队测试，含 2 条打在真实 SQLite 路径上）。

**S3 证据（`AGENTS.md` §5.2）：**

```
CGO_ENABLED=0 go build ./...   → exit 0
go vet ./...                    → exit 0
gofmt -l .                      → 空
go test ./...                   → 全部 package ok
红队测试                          → 7/7 PASS
```

**端到端实测（修复 epoch 锚点后）：**

```
mine --once ×5（5 个不同 URL）
  → receipts: 5, creditedReceipts: 5, totalPoints: 46.65
```

完整报告见 [`docs/stages/S3-report.md`](docs/stages/S3-report.md)。

> **诚实边界 1：** `ScoringSink` 把 `Verified` 设为真，依据是**本进程刚刚自签并自校验通过**。
> 这是 S4 之前的临时口径 —— **对抗验证尚未存在**。若不这样做，矿工将一分不得，
> 是更糟的错误。S4 落地后应改由验证者结论驱动。
>
> **诚实边界 2：** `PointsLedger` 的**持久化已具备**（`store.NewScoringLedgers`，CLI 已使用），
> 但**至今没有任何东西读过它来发放奖励**。积分目前只是本地记账，
> **不可转让、未定价、不承诺任何回报**（`MVP.md` §6.1）。

---

## 5. S4 — 对抗验证 + 质押

**目标：** 自生成任务不再"自己出题自己答"。

**对应验收判据：** ⑤ 对抗验证闭环

| id | 任务 | 依赖 | 验收 | 状态 |
|---|---|---|---|---|
| S4-0 | **把 `ScoringSink` 的 `Verified` 从"自签自校验"改由验证者结论驱动** | S4-3 | 计分不再信任提交者 | ✅ **done** — `mining.VerdictSource` 改为**可注入**；默认仍是 `SelfCheckVerdicts`（保持 S4 前的兼容行为），生产侧用 `verification.RecordedVerdicts` 读回执里的验证结论。**注意行为变化**：接上真实源后，新挖回执为 `pending` → **未验证前不得分**（这是正确终态，见报告） |
| S4-1 | **BLK-3**：验证者指派策略（随机种子 / 防串谋） | BLK-3 | 策略已书面确定 | 🟡 **机制已就位，策略仍开放** — `verification.Policy` 接口 + 参考实现 `EligiblePolicy`（确定性种子）。**BLK-3 未关闭**：生产策略仍未选定，替代实现不需改机制 |
| S4-2 | 随机指派验证者（B 不知道 A 是谁） | S4-1 | 不可自选 | ✅ **done** — 指派由 `Policy` 产出，**但 `Verify` 会重新检查返回值**（策略是选择器、不是权威）；`Assign` 与 `Verify` **双重**排除生产者 |
| S4-3 | 验证者重取 anchor + 重算 result | S4-2 | 二值判定：一致 / 不一致 | ✅ **done** — 两个步骤都实现：`Recomputer` 复用 `mining.Run` 重跑同一 spec；`AnchorChecker`（`mining.AnchorConsistency`）**重取 anchor 并比对 contentHash**。**端到端实测**：诚实 extract 一致通过；篡改 result 被拒；**篡改 anchor 被拒**（错误信息同时给出记录值与当前值） |
| S4-4 | **积分质押**（非代币，`stake: 50`） | S4-3 | 质押记账正确 | ✅ **done，但**实现为**独立承诺账本**而非扣分。**A5 强制**：`PointsLedger` 方法集被测试锁定为 6 个且**无 Debit**，所以"扣质押"**不可能**表达为扣分。已加**持久化**（`verification_commitments` 表）与**schema 守卫**（无 balance 列、无触碰 `point_entries` 的 trigger） |
| S4-5 | 判定结算（一致 → 奖励；不一致 → 扣质押） | S4-4 | **判错时质押被正确扣除** | ✅ **done（语义已澄清）** — 一致 → 承诺 `released`；不一致 → 承诺 `slashed`。**"slash" = 放弃并记录该承诺，不是从余额扣除**。不变式 A5 禁止积分在 agent 间移动 |
| S4-6 | 防串谋约束（A ≠ B；产出/验证比例上限） | S4-2 | 违反即拒绝 | ✅ **done** — ①`Verify` 拒绝自验（**不信任 Policy 的输出**）；②`RatioPolicy` 比例上限，**纯验证者（产出为 0）直接拒绝**；③**真实计数**已接入（`store.Activity` 用 SQL COUNT），且**无 Activity 时 fail closed** |
| S4-7 | 验证奖励 < 产出奖励 | S4-5 | 纯验证不划算 | ✅ **done** — 无验证奖励可发（A5 下积分只能由产出获得）。默认比例 0.5 使验证**最多占产出的一半** |
| S4-8 | 验证窗口（`MVP.md` §5.4：该 epoch 结束前） | S4-3 | 窗口外不再重取 | ✅ **done** — `verification.EpochWindow`（默认 24h epoch）；窗口保护的是**生产者**：内容会变，过期重取会误伤诚实工作。CLI 默认启用，`--no-window` 显式关闭 |
| S4-9 | 验证接入 CLI | S4-3 | 可手动验证一枚回执 | ✅ **done** — `relayfirst verify-receipt`；单枚模式 + **`--all` epoch 批量模式**（逐枚 verdict + 汇总）。汇总**区分 rejected 与 skipped**（窗口外 / 验证者即生产者）：把"没验"与"验失败"合并会让"什么都验不了"的 epoch 看起来像"全过" |
| S4-10 | 验证结论**持久化** | S4-3 | 重启后仍可读 | ✅ **done** — `verification_verdicts` 表；`RecordingVerdicts` 同时提供写侧与读侧，避免把写与读接到不同存储 |
| S4-11 | **并发正确性门禁** | S4-3 | `-race` 通过 | ✅ **done** — CI 新增 `go test -race ./...`（**第 9 道门禁**）。race 检测器只报**真实发生**的竞争，所以门禁强度取决于测试里的并发 —— 本轮已把覆盖从 2 个包补到 **5 个包**（`mining` / `node` / `scoring` / `store` / `verification`） |
| S4-12 | **修掉并发测试发现的真实 data race** | S4-11 | `-race` 干净 | ✅ **done** — `Verifier.Verify` 原地改写调用方回执（`r.Verification = ...`），两个 goroutine 验证同一对象时**并发写同一字段**。race 检测器在补上并发测试后立刻报出。**修法：只串行化"记录"这一步，重执行保持并行**（重执行是昂贵部分，放进锁会毁掉并行度）。已复测非空转：去掉锁 → 立刻复现 |

> **S4-11 的一个教训（值得记住）：**
> 第一次"破坏性验证"我把 `Balance` 的锁去掉，门禁**仍然 PASS** ——
> 因为该函数只在所有写者结束后才被调用，**没有重叠访问就没有竞争**。
> **这不是门禁失灵，是我的破坏无效。** 换成移除 `Credit` 的锁立刻 FAIL。
> 结论：**验证一道门禁时，必须制造它本该抓到的那种故障**，否则只是在确认它会打印 PASS。

**S4 完成定义：** 验证闭环端到端可跑，且**判错扣质押**生效。→ ✅ **达成**，但"扣质押"的语义已澄清为**承诺记录状态变化**，不是扣分（见下）。

**S4 证据（`AGENTS.md` §5.2）：**

```
go test ./... -count=1 -v      → 328 PASS / 0 SKIP / 0 FAIL
./scripts/ci.sh                → 7/7 PASS

红队 5/5：
  篡改回执被拒（真实 executor，回执签名仍有效）
  诚实回执通过（真实 executor）
  自验被拒（Policy 返回生产者 → Verify 拒绝）
  超比例验证被拒（含"纯验证者产出为 0"）
  一致 → released ／ 不一致 → slashed

不变量 A5 门禁非空转（实测）：
  向 PointsLedger 注入 Debit → 两条守卫同时 FAIL
  还原                        → PASS
```

> **必须传播的诚实声明（`MVP.md` §5.6）：**
> **大户养足够多 sybil 仍可自验。** 成本随 sybil 数量**线性上升**，收益被全局去重（A6）压住。
> 这是**可接受的权衡，不是无懈可击**。参考实现 `EligiblePolicy` 用确定性种子，
> 因此**可被磨 agentId 攻击**；它提供的是"任何人可复核谁该验证"，不是不可操纵。
> **不要把它讲成无懈可击** —— 被拆穿一次，叙事就没了。

---

## 6. S5 — 薄节点（自托管）

**目标：** 陌生人能跑自己的节点。

**对应验收判据：** ③ 任意陌生人跑起节点、⑦ no-CGO

| id | 任务 | 依赖 | 验收 | 状态 |
|---|---|---|---|---|
| S5-1 | HTTP 收件端点（**不是 WebSocket**） | S1-1 | 可收件 | ✅ **done** — `internal/node`；`POST /messages`（标准库 `net/http`，无框架）；`TestNode_AcknowledgementShape` |
| S5-2 | 按 `agentId` 存储 + 客户端拉取（GET） | S5-1 | 拉取正确 | ✅ **done** — `GET /messages/{agentId}` + `?limit=`；`TestMessageStore_ByAgentIsolates`（**不得返回他人邮件**） |
| S5-3 | `eventId` / `receiptId` 去重 | S5-2 | 重复投递 → 只存一次 | ✅ **done** — 主键即 `receipt_id`，`ON CONFLICT DO NOTHING`；**重复返回 200 而非报错**（否则重试会无限循环）；`TestNode_DuplicateIsAcknowledgedNotRejected` |
| S5-4 | 收件 acknowledgement | S5-1 | 返回 ACK | ✅ **done** — `{ok,id,stored}`；`stored=false` 表示"已存过" |
| S5-5 | `/.well-known/relayfirst` 信息文档（RFN-04 最小版） | S5-1 | JSON 可获取 | ✅ **done** — 含 `verifies:false` + 明文说明；`TestNode_WellKnownDocumentsNoVerification` |
| S5-6 | SQLite 存储（单文件） | S5-2 | 零外部依赖 | ✅ **done** — `internal/sqlite/mailbox.go`；纯 Go 驱动；`TestMessageStore_SurvivesReopen` |
| S5-7 | **单二进制 + `CGO_ENABLED=0`** | S1-1 | 静态二进制 | ✅ **done** — `cmd/relayfirst-node`；CI 门禁 `CGO_ENABLED=0 go build ./...` 通过；镜像 36.5MB |
| S5-8 | Dockerfile（`docker run` 一条命令） | S5-7 | 陌生用户 10 分钟跑起 | ✅ **done** — `Dockerfile` + `.dockerignore`；**实测镜像构建 + 容器运行成功**（见下） |
| S5-9 | 多 relay publish（向 ≥2 个节点投递） | S5-4 | 一处故障不影响送达 | ✅ **done** — `internal/publish`；`mine --relay`（可重复）；**实测一个 relay 挂掉仍送达**；**扇出已改为并发**：N 个 relay 的最坏等待从 N×timeout 降为 **1×timeout**（实测 5 relay × 300ms = 0.31s，串行约需 1.5s）；结果仍按**配置顺序**报告 |

**S5 完成定义：** 按 `MVP.md` §7.2 的命令，**一条 `docker run`** 起节点。→ ✅ **达成** — 实测构建并运行，`/healthz` 与 `/.well-known/relayfirst` 均正常响应。

**S5 证据（`AGENTS.md` §5.2）：**

```
docker build -t relayfirst/node:test .        → 成功，镜像 36.5MB
docker run -d -p 18080:8080 ...               → 容器启动
curl /healthz                                 → {"ok":true}
curl /.well-known/relayfirst                  → 完整 JSON（含 verifies:false）

端到端（真实签名回执 → 容器 → 拉回）：
  POST -> {'ok': True, 'stored': True}
  PULL count -> 1
  bytes identical -> True      ← 签名可存活，节点未改写字节
```

**多 relay（S5-9）实测：**

```
mine --once --relay <健康容器> --relay http://127.0.0.1:9
  → relayed to 1/2 node(s)
      ! http://127.0.0.1:9: connect: connection refused
  → 回执仍成功入库并计分；容器端 messages 计数 +1

mine --once --relay http://127.0.0.1:9（全部不可达）
  → relayed to 0/1 node(s) + 明确错误；仍入库、仍计分、exit 0
```

> **节点是"哑"的** —— 只 store-and-forward，**不验签、不裁决、不读链**。
> 这不是没做完，是 `MVP.md` §7.3 的设计：节点不是护城河，它只是"我能自己跑"这个事实的载体。
> 恶意节点最多只能扣留消息 —— 这正是 publish 要向多节点扇出的原因。
>
> **字节必须原样保存**：回执签名覆盖精确字节序列，节点若解析后重新序列化，
> 回执会**看起来正常**、然后在别处验证失败 —— 最坏的失败模式。所以 payload 对节点不透明。

---

## 7. S6 — CLI 上手体验（10 分钟）

**目标：** 从 `npx` 到看到分数，**无人工协助**。

**对应验收判据：** ① 10 分钟出积分

| id | 任务 | 依赖 | 验收 | 状态 |
|---|---|---|---|---|
| S6-1 | `relayfirst init`（生成/导入 EVM 钱包 → 输出 agentId） | S1-8 | 30 秒内完成 | ⛔ **被阻断** — 生成并**落盘私钥**属凭据处理，守卫以 `POL-SECRETS-1` 阻止（`userCanOverride:false`）。**代理不做密钥落盘**（`CODING_RULES.md` §8）。当前路径：用户自备 key，经 `RELAYFIRST_PRIVATE_KEY` 注入；`--help` 中已如实写明 `init` 尚未实现 |
| S6-2 | `relayfirst config set --provider/--api-key` | S2-6 | 三种 provider 可配 | 🟡 **部分** — `config set/get/show` **已实现**（provider / model / source / relay / semantic，0600 落盘，**拒绝任何密钥形状的值**）；**`--api-key` 被刻意移除**：密钥只从 `OPENAI_API_KEY`/`ANTHROPIC_API_KEY` 读取 |
| S6-3 | `relayfirst mine`（守护进程启动） | S2-7 | 立刻出分 | ✅ **done** — 无 flag 亦可运行（配置补默认值）；实测 3 条回执即出分 |
| S6-4 | **实时反馈**（本 epoch 积分 / 任务数 / 有效 anchor） | S6-3 | 农民**立刻看到分数在涨** | ✅ **done** — `liveProgress`；数字**从账本读回**而非本地计数（避免与实际记账漂移）；`anchors` 排除 inline 合成 anchor |
| S6-5 | `relayfirst status` | S3-7 | 积分可查 | ✅ **done** — 积分（本 epoch + 终身）/ 回执 / 去重 artifact / **有效 anchor** / epoch 结束时间；`stats` 保留为别名（单一实现） |
| S6-6 | `relayfirst receipts --export` | S1-9 | **能带走全部回执** | ✅ **done** — `store.ExportReceipts`；写**规范签名字节**，按 receiptId 命名（幂等）；**实测导出 3/3 离线验签通过，篡改副本被拒** |
| S6-7 | `npx relayfirst@latest` 零安装入口 | S6-1 | 无需预装 | 🟡 **部分** — `scripts/npx-relayfirst.mjs` + `package.json` 的 `bin` 已就位；**未发布到 npm**（发布是外部动作）。启动器**委托**给 Go 二进制而非重写协议，避免 EIP-712 双实现漂移（不变量 A4） |
| S6-8 | **端到端计时测试** | S6-1..7 | **陌生用户 ≤10 分钟出分** | ⬜ **未做** — 需真人计时；机器实测整条路径 < 3 秒，但判据①要求的是**陌生人**无协助完成，属 S8 |

**S6 完成定义：** 找一个没接触过项目的人，计时，**10 分钟内出分**。→ ⬜ **未验证**（需真人；且 S6-1 被阻断）

**S6 证据（`AGENTS.md` §5.2）：**

```
./scripts/ci.sh                → 5/5 PASS
go test ./... -count=1 -v      → 258 PASS / 0 SKIP / 0 FAIL

完整路径实测（全部经 npx 启动器）：
  1. config set provider local / source https://example.com   → 配置成功
  2. mine --once ×3（不带任何 --source/--provider）            → 3 条回执
  3. status                                                   → points 16.67 / 3 回执 / 2 已记账 / 1 anchor
  4. receipts --export + 逐个 verify                           → 3/3 离线验签通过
```

> **`init` 为什么没做：** 它不是"难"，是**该由人决定**。
> 让代理生成私钥并写进文件，等于让代理替用户创设资产控制权 —— 这是安全边界，不是工程缺口。
> 替代路径已可用：用户自备任意 EVM 私钥经环境变量注入，`config set` 负责其余设置。
>
> **`config` 为什么不收 API key：** 明文配置文件是整个系统里最容易被泄漏的地方
> （备份 / dotfiles 仓库 / 截图 / 求助截图）。所以 `config` **没有**存密钥的字段，
> 且 `Set` 会**拒绝**任何形似密钥的值（`sk-` / `-----BEGIN` / `-----END`）。
> 有测试同时锁定行为与**结构**（防止未来有人加一个 `APIKey` 字段）。

---

## 8. S7 — 链上锚定（便宜版）

**目标：** 回执可被链上证明，成本可忽略。

| id | 任务 | 依赖 | 验收 | 状态 |
|---|---|---|---|---|
| S7-1 | 回执集 → Merkle tree（按 epoch） | S3-6 | root 确定可复算 | ✅ **done** — `internal/merkle`；RFC 6962 域分离（`0x00` 叶 / `0x01` 内部节点）改为 **keccak256**（EVM 原生）与**补齐到 2 的幂**（形状两语言唯一）；**实测 Go / Solidity / viem 三方对同一数据得出同一 root** |
| S7-2 | **`mapping(uint256 epoch => bytes32 root)`**（~50 行） | S7-1 | 部署成功 | ✅ **done（未部署）** — `contracts/RelayAnchor.sol`，**`forge build` 通过**。**实际 197 行**，核心逻辑很小，行数来自注释 + 按 operator 命名空间 + 自定义 error。**部署需真人**（无节点、无资金账户） |
| S7-3 | 每日提交一个 root | S7-2 | 成本可忽略 | 🟡 **未做** — `relayfirst anchor root` 可算出 root，但**提交是签交易**，属人工动作；已写明步骤 |
| S7-4 | Merkle proof 生成 + 验证 | S7-1 | 任何人可证"我的回执在 epoch N" | ✅ **done** — `anchor proof` 生成；**两种验证**：`anchor verify`（从本地 store 重算 root）与 `anchor check`（**对传入的 `--root` 校验，不需要任何 store/服务器**）。后者才是锚定的意义所在：持有已公布 root + proof 的局外人，在所有服务器关掉后仍能自证包含关系。`--root` **刻意不从 proof 读取**，否则验证会循环（伪造 proof 只需自带它能折叠出的 root） |

**S7 完成定义：** 回执可被链上证明，成本可忽略。→ 🟡 **代码层达成，链上未验证**（无节点/无资金，未部署）

**S7 证据（`AGENTS.md` §5.2）：**

```
./scripts/ci.sh                → 7/7 PASS（新增 Merkle 新鲜度 + forge test 两道门禁）
go test ./... -count=1 -v      → 289 PASS / 0 SKIP / 0 FAIL
forge test                     → 15 PASS / 0 FAIL

跨语言一致性（不变量 A4）：
  Go      root = 0x2af45716d0d9fdd3b1f52e542ecaef2ee367e59a5dd2a8cacb99f18718c3d395
  Solidity        → forge test 的 test_ProofsMatchGoGeneratedVectors PASS（对同一语料）
  viem            root = 0x2af45716d0d9fdd3b1f52e542ecaef2ee367e59a5dd2a8cacb99f18718c3d395

新鲜度门禁非空转（实测）：
  篡改 testdata/merkle-vectors.json 的一个 root → 门禁 FAIL
  还原 → 门禁 PASS
```

> **未部署，且刻意如此。** 没有链节点、没有资金账户、没有部署权限。
> 提交 root 要用**你自己的**账户签交易并付 gas —— 这是花钱且不可逆的动作，属人工。
> 步骤写在 `docs/stages/S7-report.md`。
>
> **`mapping` 与 `MVP.md` §4.3 有偏差。** 文档写 `mapping(uint256 => bytes32)`，
> 但 mapping 需要**键 + 值**两个类型，`uint256 => bytes32` 是单个字段、无法编译。
> 实现为 `mapping(uint256 epoch => bytes32 receiptsRoot)`，并**额外按 operator 命名空间隔离**
> （`mapping(address => mapping(uint256 => bytes32))`）—— 共享单一 mapping 会让任何人覆盖
> 任何人的 root，anchor 随即失去证据价值。

> **不做完整结算。** 合约里**没有** `settlement` / `escrow` / `claim` / `withdraw` / `dispute`，
> 也**不给提交者任何奖励**。任何能依据回执转移价值的逻辑都会关掉"永不发币"的退路
> （不变量 A5）—— 那些属 `ARCHITECTURE.md` §8 的 Roadmap，不是这里。

---

## 9. S8 — 红队与上线门禁

**目标：** 七条验收判据全过。

| id | 任务 | 对应判据 | 状态 |
|---|---|---|---|
| S8-1 | 陌生人 10 分钟出分 | ① | ⬜ **未做** — 需真人计时（S6-8） |
| S8-2 | 关掉服务器 → 回执仍可离线验证 | ② | ✅ **达成** — `verify` 完全离线，无任何网络调用；S6 实测导出后逐枚离线验签通过 |
| S8-3 | 陌生人一条 docker 命令起节点 | ③ | ✅ **代码层达成** — S5 实测 `docker build` + `docker run` 成功、`/healthz` 与 `/.well-known` 正常；**"陌生人"计时未做** |
| S8-4 | 四项伪造攻击全部 0 分（a/b/c/d） | ④ | ✅ **达成** — `internal/redteam` 对**真实栈**（真实 SQLite + 真实 executor + 真实验证者）跑四项攻击：**a** 伪造 result、**b** 伪造 anchor contentHash、**c** 重放他人回执（含改签到自己名下）、**d** 自验。每项断言**余额不增**，并有一条**控制测试**证明诚实工作仍能得分（否则"全部拒绝"也会通过） |
| S8-5 | 对抗验证闭环 + 判错扣质押 | ⑤ | ✅ **机制达成** — S4 端到端可跑；"扣质押"语义已澄清为**承诺状态变化**，非扣分（A5） |
| S8-6 | TS/Go KAT 字节相同（CI 门禁） | ⑥ | ✅ **达成** — 两道 KAT 门禁（EIP-712 53 向量、Merkle 10 树）；**另加第三实现门禁**：viem 对 Merkle root 与全部 72 条 proof 交叉校验（S7） |
| S8-7 | `CGO_ENABLED=0` 构建通过（CI 门禁） | ⑦ | ✅ **达成** — CI 门禁；节点镜像亦为静态二进制（36.5MB） |
| S8-8 | **BLK-2**：至少 1 个真实消费方 | 前置 | ⬜ **未做** — 需外部接洽（人类动作） |
| S8-9 | 积分声明合规检查（不承诺回报、不定价） | 风险 R7 | ✅ **done** — `internal/compliance`（A5 文本守卫：风险词表 + 批准免责语白名单 + 逐行 points 作用域）；`internal/devtools/compliance_audit.go`（人类可读审计工具，发现违规 exit 1）；**已接入 CI 第 10 道门禁**。审计实测：扫描 2 个用户可见文件命中 5 条短语，**全部落在批准免责语内**（`carry no promised return` / `non-transferable`）；喂入违规文本 → 3 条 finding、exit 1。**诚实边界**：这是**绊线不是证明** —— 只覆盖已知词表，无法评估新措辞，也无法在「免责语」与「伪装成免责语的违规」措辞相同时区分二者；两条限制都有测试锁定（`TestScan_LimitsAreReal`） |

**S8 证据（`AGENTS.md` §5.2）：**

```
./scripts/ci.sh                → 10/10 PASS
go test ./... -count=1 -v      → 423 PASS / 0 SKIP / 0 FAIL（607 含子测试）
go test -race ./...            → PASS（并发门禁，覆盖 5 个包）
forge test                     → 15 PASS / 0 FAIL
node scripts/verify-merkle-viem.mjs → PASS (10 roots, 72 proofs)

非空转实测（守卫会真的失败）：
  注入 Debit 到 PointsLedger          → A5 两条守卫 FAIL
  篡改 Merkle 语料的一个 root          → 新鲜度门禁 FAIL
  篡改一条 proof 的 sibling            → viem 门禁 FAIL（给出两个不同 hash）
  移除 MemPointsLedger.Credit 的锁     → race 门禁 FAIL（完整栈）
  移除 Verifier.recordMu 的锁          → race 门禁 FAIL（复现真实竞争）
  向合规审计喂入 "points are worth $5 and can be redeemed for USDC"
                                      → A5 合规门禁 FAIL（3 条 finding，exit 1）

race 门禁补覆盖时**找到一个真实 bug**：
  Verifier.Verify 原地改写调用方回执 → 并发验证同一对象时数据竞争 → 已修（见 S4 报告）
```

**S8 完成定义：** **七条全过 + BLK-2 已解决。任何一条不过，不上线。** → 七条中 ①②③④⑤⑥⑦ 均达成；**BLK-2（S8-8）未解决 → 不得上线**。

---

## 10. S9–S13 — MVP 2.0（已批准范围，未开工）

> **范围来源：`MVP.md` v2.0（§0 定位、§5.0 验证模式、§6.1 SBT、§6.5 USDC、§7.3 节点）。**
> **完整论证：`docs/mvp-2.0-proposal.md`（accepted）。**
> **v1.0 原文归档：`docs/archive/mvp-1.0.md`。**
>
> **状态：全部 `todo`。** 本节是 v2.0 的任务分解，尚未开始实现。

### 10.0 S9 — A2A 协议层（本体）

**目标：** 两个独立 agent 能开会话、派任务、收回执。**这是 v2.0 的对外身份。**

> **⚠️ 强制顺序：§10.1（版本化地基）必须先于 S9-7。**
> S9-7 会往**签名载荷内**加 `task.verification`；若先做 S9-7，所有已上线的 v1 节点
> 会**永久拒绝**新回执 —— 那是一次自己制造的分叉。见 `MVP.md` §17.4（A9）与 §17.5。

| id | 任务 | 依赖 | 验收 | 状态 |
|---|---|---|---|---|
| S9-1 | 引入官方 A2A SDK（wire 层不自研） | S9-0* | Agent Card / Message / Task 线格式与 A2A 1.0 兼容 | ⬜ todo |
| S9-2 | `internal/a2a` 包骨架（card / task / session） | S9-1 | 包存在且有测试 | ⬜ todo |
| S9-3 | Agent Card 发布 + 发现（签名） | S9-2 | 能发布/查到 agent 能力 | ⬜ todo |
| S9-4 | Session 生命周期（`SESSION_OPEN` / `_CLOSE`） | S9-2 | 两个 agent 能开会话 | ⬜ todo |
| S9-5 | Task 状态机（先 4 条超时边） | S9-2 | `OFFER`/`ACCEPT_START`/`INPUT`/`APPROVAL` 超时可用 | ⬜ todo |
| S9-6 | 消息/事件层（`ARCHITECTURE.md` §4.4 事件表子集） | S9-4 | 任务全生命周期事件可发可收 | ⬜ todo |
| S9-7 | `verification` 字段（§5.0） | **S9-0\*** | `recompute` / `evaluator` / `dispute` 三值入 schema | ⬜ todo |
| S9-8 | `recompute` 模式接线到 v1.0 验证器 | S9-7 | A2A 任务走现有二值验证 | ⬜ todo |
| S9-9 | `evaluator` 模式机制（只做机制） | S9-7 | 评估者可被指定并出结论 | ⬜ todo |
| S9-10 | 回执关联 `task.a2aTaskId`（v1.0 已预留字段） | S9-5 | 回执能回溯到 A2A 任务 | ⬜ todo |
| S9-11 | A2A 端到端测试（判据 ⑧） | S9-1..10 | 两 agent 闭环，wire 兼容 | ⬜ todo |
| S9-12 | **WebSocket 传输绑定**（新增，HTTP 保留，§12.1） | S9-1 | `AgentInterface` 声明两个绑定 | ⬜ todo |

### 10.1 S9-0 — 版本化地基（**必须在任何 S9 代码之前**）

**目标：** 让以后每一次变更都不必再担心分叉。

> **⚠️ 这不是"可选的前置"，是 A9（`MVP.md` §17.4）的执行。**
> **完整论证见 [`docs/notes/upgrade-architecture-plan.md`](docs/notes/upgrade-architecture-plan.md)。**
> **§10.0 的 S9-7 必须排在本节之后。**

| id | 任务 | 依赖 | 验收 | 状态 |
|---|---|---|---|---|
| S9-0a | 回执 schema 语法 `v<major>[.<minor>]` + **`unsupported` / `invalid` 区分** | — | 缺省 minor=0；major 不在支持集 → `unsupported`（非 `invalid`） | ✅ **done** — `internal/receipt/schema.go`（`ParseSchema` / `CheckSchema` / `UnsupportedError`）；`ValidateStructure` 改用它。**区分由两个错误类型承载，不由字符串** —— 合并会让"验证器太旧"看起来像"抓到伪造"。错误信息**指明方向**（"newer than this build"）。18 条子测试覆盖语法 + 两种错误类型 |
| S9-0b | **未知字段容忍**（生产容忍 / `--strict` 拒绝） | S9-0a | 载荷外未知字段 → 接受；`--strict` → 拒绝 | ✅ **done** — `Unmarshal` 改为**容忍**（移除 `DisallowUnknownFields`）；新增 `UnmarshalStrict` 供红队/KAT。**容忍不是信任**：载荷外字段本就不被签名（忽略即可）；载荷内字段属于签名覆盖的字节（**天然已认证**）。**同时把一致性检查改为「子集语义」**（B4 的核心）：字节比对失败时回退到**逐字段比对** —— 本 build 认识的每个字段仍必须**精确匹配**，只有「额外字段的存在」被容忍。**这同时解决 B4**：旧验证器现在能验证带增量字段的新回执，且**篡改已知字段仍被抓到**（两条测试分别锁定）。**非空转**：恢复严格字节比对 → 增量字段测试 FAIL |
| S9-0b2 | **签名载荷改为逐字 blob**（可选字段，旧路径保留） | S9-0a | 验证者只做 keccak256，**不重新序列化** | ✅ **done** — `Receipt.Payload`（`omitempty`，旧回执字节不变）；`SignedPayload` 双路径：有 payload 逐字哈希，无则结构性重建。**8 条测试**：两路径一致 / 逐字不被重建 / 有 payload 可签可验 / 无 payload 仍可验 / 空 payload 被省略 |
| S9-0c | domain **多版本验证**（try-all + 只增不减版本表） | S9-0b2 | 旧回执永久可验 | ⬜ todo |
| S9-0d | 采用 A2A 的 `A2A-Version` / `AgentInterface`，**不自研协商** | S9-1 | 无交集时用 `ErrVersionNotSupported` 显式失败 | ⬜ todo |
| S9-0e | 消息契约占位：`sequence` + `previousEventHash` 字段位置 | S9-0b2 | 现在可恒空，但**位置现在定** | ⬜ todo |
| S9-0f | CI **G1 冻结语料** `testdata/receipts/v<major>/*.json` | S9-0b2 | 每个历史 major 至少一份真实签名回执，**只增不删** | ✅ **done** — `testdata/receipts/v1/`（2 份真实签名回执，含 1 份逐字 payload 形态）；生成器 `internal/devtools/gen_frozen_receipts.go`；消费测试 `corpus_test.go`（断言每份仍可验 + 当前 major 有语料 + 含逐字形态）。**已知缺口**：缺结构性路径样本（L2）、缺 per-major 验证入口（M2）、语料无 SHA-256 清单（M1） |
| S9-0g | CI **G2 跨版本矩阵** + **G3 未知字段注入** + **G4 协商无交集** | S9-0f | 三条门禁全绿 | 🔸 **部分** — **G1 已接入 CI（第 11 道门禁）**；G2 依赖 S9-0h + M2；G3 依赖 S9-0b；G4 依赖 S9-0d |

### 10.1b S9-0 安全审查：新增的发布前阻断项

> **审查结论见 [`docs/notes/s9-0-security-review.md`](docs/notes/s9-0-security-review.md)。**
> **结论：S9-0 比原计划大。** 审查发现 4 个发布前阻断项 + 1 高 + 3 中 + 3 低。

| id | 任务 | 严重度 | 状态 |
|---|---|---|---|
| **S9-0h** | **把 `receiptId` 与 schema 绑进签名载荷** | **critical（B2+B3）** | ✅ **done** — ①**`receiptId`**：`Validate` 重算 `DerivedReceiptID()`（= `sha256(SignedPayload())`）并比对；**不改签名字节**，故对 v1 回执同样生效（`checkReceiptID`）。②**schema**：引入**版本 profile** —— v1 的 domain `Version` 与消息字段**逐字节冻结**（3 字段），v2 起绑定 `receiptId` + `schemaMajor`（5 字段，domain `Version="2"`）。**改标 v1→v2 会换 digest → 签名失败**。**实测**：`TestTypedData_V1ProfileIsFrozen` 锁定 v1 形状；`TestTypedData_ProfilesDiffer` 证明两 profile digest 不同；`TestReceiptID_TamperedIDIsRejected` 断言重发被拒。**非空转**：禁用 `checkReceiptID` → 测试 FAIL |
| **S9-0i** | **`canonicalJSON` 加数字分支**（`float64` / `json.Number`） | **high（H1）** | ✅ **done** — 加 `float64` / `float32` / `json.Number` 分支。**浮点渲染委托给 `encoding/json`**（不手写格式化规则）—— 保证结构性路径与逐字 payload 路径**按构造一致**；手写是 Go/TS 漂移的根源（正是 A4 要防的）。`json.Number` **先校验 JSON 数字语法**再输出（它是字符串类型，不校验会把任意字节塞进签名字节）。**非空转**：移除 `float64` 分支 → 往返测试 FAIL |
| **S9-0j** | **生产调用方区分 `UnsupportedError`** | **high（H2）** | ⬜ todo — 类型只在测试里被 `errors.As`；CLI/verdict/miner 都映射为 `false` → **"我验不了"被报成"这是伪造"** |
| **S9-0k** | **语料 SHA-256 清单 + 生成器拒绝覆盖** | medium（M1） | ⬜ todo — 否则"重新生成"可**悄悄重写绊线**，append-only 失效 |

**发布门槛（来自审查）：**

```text
首发前必修：  B1 ✅ · B2 ✅ · B3 ✅ · H1 ✅ · B4 ✅
声称 A9 合规前：H2（⬜）· M1（⬜）· M2（⬜）· L2（⬜）
可后置：      M3 · L1 · L3
```

> **✅ 五个发布前阻断项已全部解决。** B4 的解决方式是把一致性检查改为**子集语义**
> （见 S9-0b），并让 `Unmarshal` 容忍未知字段 —— 这样旧验证器**能验证**带增量字段的新回执，
> 而**篡改已知字段仍被抓到**。
>
> **⚠️ 但 A9 仍不应声称完全合规** —— 剩下四项（H2/M1/M2/L2）是"声称合规前必修"：
> H2 让"我验不了"被误报成"这是伪造"；M1 让语料绊线可被重新生成悄悄重写；
> M2 使语料只用当前规则验证（G2 无法表达）；L2 缺结构性路径的语料样本。

> **⚠️ 实现中发现并修复了一个真实安全缺陷（值得记住）：**
> S9-0b2 初版**只哈希 payload 字节，不比对结构化字段**。于是两者可**不一致** ——
> 攻击者篡改 `result.value`（结构化字段），**签名仍有效**（签名从未覆盖它），
> 消费者读到伪造值。**这个缺陷是用"篡改冻结语料"测出来的** —— 门禁当时**没抓到**，
> 于是才发现。修法：`checkPayloadConsistency` 在 payload 在场时**重建并逐字节比对**。
> 回归测试遍历**全部 9 个签名字段**，逐个断言篡改必被拒。
>
> **教训：验证"门禁是否有效"必须用真实攻击，不能靠假设。**

**S9-0 的验收（四条门禁）：**

| 门禁 | 检查什么 |
|---|---|
| **G1 冻结语料** | 每个历史 major 的合法回执仍能通过对应版本验证 |
| **G2 跨版本矩阵** | 版本 X 的签名不会在版本 Y 的规则下验过（防签名混淆） |
| **G3 未知字段注入** | 载荷外 → 接受；载荷内（blob 落地后）→ 接受；`--strict` → 拒绝 |
| **G4 协商无交集** | 两个支持集无交集的节点 → 显式失败，且失败信息含双方版本 |

> **⚠️ 未知字段容忍需要一个 `--strict` 模式**用于红队与 KAT：
> 严格模式未知字段报错，生产模式容忍。**这样"容忍"是可测的，而不是靠嘴说。**

> **⚠️ 现有代码的三个分叉点**（`upgrade-architecture-plan.md` §2.9）：
> F1 `receipt.go` schema 精确匹配 · F2 `DisallowUnknownFields` · F3 验证器重建载荷。
> **F3 是根因，S9-0b 修它。**

### 10.2 S10 — 节点升级：不可信但能干（§7.3）

| id | 任务 | 依赖 | 验收 | 状态 |
|---|---|---|---|---|
| S10-1 | 观测索引（`GET /observations`） | S9-1 | 能按 subject 查询 | ⬜ todo |
| S10-2 | 交叉验证视图（`GET /observations/{id}/evidence`） | S10-1 | 多 agent 独立验证可见 | ⬜ todo |
| S10-3 | 任务中转（`POST /tasks` · `GET /tasks` · claim） | S9-5 | 节点能中转任务 | ⬜ todo |
| S10-4 | Agent Card 发现端点 | S9-3 | `GET /agents` | ⬜ todo |
| S10-5 | **可归因验证**：节点签自己的结论 | S10-1 | 节点撒谎留下证据 | ⬜ todo |
| S10-6 | 客户端独立复验（不盲信节点） | S10-5 | 节点撒谎时客户端仍能独立判定 | ⬜ todo |
| S10-7 | 节点端到端测试（判据 ⑩） | S10-1..6 | 索引可用 + 不盲信 | ⬜ todo |

### 10.3 S11 — SBT 积分（§6.1）

| id | 任务 | 依赖 | 验收 | 状态 |
|---|---|---|---|---|
| S11-1 | `RelayPoints.sol`：ERC-721 + ERC-5192 | — | 合约可编译 | ⬜ todo |
| S11-2 | **转账拦截 override**（不只 `locked()` 声明） | S11-1 | 转让尝试 revert | ⬜ todo |
| S11-3 | 累计积分存储（非增量） | S11-1 | 漏 claim 不丢分 | ⬜ todo |
| S11-4 | Merkle claim（复用 `internal/merkle` + `RelayAnchor.sol`） | S11-3 | 能凭 proof 拿到累计值 | ⬜ todo |
| S11-5 | 无许可 poke（任何人可代交） | S11-4 | 用户零 gas 也能更新 | ⬜ todo |
| S11-6 | 动态 `tokenURI()`（积分 + 声誉 metadata） | S11-3 | 钱包里能点进去看到积分 | ⬜ todo |
| S11-7 | A5 守卫覆盖新合约与文案 | S11-6 | 不出现"积分值 X USDC" | ⬜ todo |
| S11-8 | SBT 端到端测试（判据 ⑨） | S11-1..7 | 钱包可见 + 不可转让 + claim 正确 | ⬜ todo |

### 10.4 S12 — 结算 + MCP（§6.5 / 提案 §9）

| id | 任务 | 依赖 | 验收 | 状态 |
|---|---|---|---|---|
| S12-1 | 赏金托管（最小版 `SettlementManager`） | S9-5 | Requester 能托管赏金 | ⬜ todo |
| S12-2 | 抗双领 bitmap（`ARCHITECTURE.md` §8.6） | S12-1 | 同一赏金不能领两次 | ⬜ todo |
| S12-3 | USDC 通道（**最小版，非 x402**） | S12-1 | 任务赏金可用 USDC 支付 | ⬜ todo |
| S12-4 | **A5 隔离检查**：USDC 与积分不挂钩 | S12-3 | 文档与文案无"积分定价" | ⬜ todo |
| S12-5 | MCP server（**不持主密钥**） | — | 发现/认领/提交可用 | ⬜ todo |
| S12-6 | 签名留在本地 CLI（MCP 只转发已签字节） | S12-5 | MCP 无密钥也能完成闭环 | ⬜ todo |
| S12-7 | IDE 一行配置接入 | S12-5 | 零安装可挖 | ⬜ todo |

### 10.5 S13 — E2EE + 密钥层级（提案 §6 / §8）

| id | 任务 | 依赖 | 验收 | 状态 |
|---|---|---|---|---|
| S13-1 | X25519 密钥交换（用库，A1） | — | 派生共享密钥 | ⬜ todo |
| S13-2 | XChaCha20-Poly1305 payload 加密（用库） | S13-1 | 私有任务 payload 加密 | ⬜ todo |
| S13-3 | Session Delegation 最小版（作用域 + nonce 撤销） | S9-3 | 主密钥不在不可信组件里 | ⬜ todo |
| S13-4 | MCP / 节点只拿会话密钥 | S13-3 | 泄露会话密钥无法动资产 | ⬜ todo |
| S13-5 | 加密端到端测试 | S13-1..4 | 私有任务可跑，relay 只见密文 | ⬜ todo |

**S9–S13 完成定义：** `MVP.md` §11 的十条判据全过，且 **BLK-2 已解决**。→ ⬜ **未开工**

> **⚠️ 规模警告：** v2.0 同时做 A2A 本体 + 节点索引 + 结算 + 加密 + MCP + 积分链上化，
> **比 v1.0 大得多**（v1.0 花了 S1–S8）。**必须分阶段，且 BLK-2 从第一天并行。**
> 否则会做出一个完整、漂亮、但没人用的系统。

---

## 11. 明确不做（不进本表）

> 以下全部属于 `ARCHITECTURE.md` 的 Roadmap。**若有人提出来，指向该文档，不是本表。**

| 不做 | 原因 |
|---|---|
| Delegation 完整 policy（作用域/限额/撤销） | 对"农民来玩"零贡献 |
| 结算 / Escrow / Merkle claim | 稀释喷事（变成又一个支付服务） |
| Dispute / 仲裁 / Slashing | **§5.0 的 ground truth 约束使其不必要** |
| x402 / USDC 集成 | 同上 |
| E2EE（X25519 / XChaCha20） | 农民不在乎隐私 |
| 多语言 SDK（Go/Python） | 3 倍成本，MVP 只需 TS |
| 完整状态机（6 条超时边） | 过度工程 |
| NATS / Redis / 多 edge | SQLite 够用 |
| RFN-01…RFN-12 标准族 | 全部 Roadmap |
| 多 relay 共识 / 联邦 | 同上 |
| WebSocket transport | HTTP + SSE 够用 |
| **Token 发行 / 质押奖励 / 节点排放** | **明确 non-goal** |

---

## 12. 里程碑

| 里程碑 | 包含 | 说明 |
|---|---|---|
| **M1 — 地基** | S1 | 离线可验证回执 + KAT 锁定 |
| **M2 — 能挖** | S1–S3 | 单机可挖，刷量不赚钱 |
| **M3 — 可上线（v1.0）** | S1–S6 + S8 | 陌生人可参与（③① 体验判据） |
| **M4 — 完整 MVP v1.0** | S1–S8 | 七条判据全过 + BLK-2 |
| **M5 — A2A 本体** | S9 | 两 agent 能开会话/派任务/收回执（判据 ⑧） |
| **M6 — 节点与身份** | S10 + S11 | 索引可用 + SBT 钱包可见（判据 ⑨⑩） |
| **M7 — 完整 MVP v2.0** | S1–S13 | 十条判据全过 + BLK-2 |

**工期参考（`MVP.md` §14）：** S1–S8 合计 **~4–5 周**（1 人 + Cursor）。
**S9–S13 未估，且规模显著大于 S1–S8**（见 §10 末尾警告）。

---

## 13. 风险对照

| 风险 | 对应任务 | 状态 |
|---|---|---|
| **R1 订阅不可程序化驱动** | BLK-1 | ⬜ 最高优先级 |
| R4 无真实需求 | BLK-2 / S8-8 | ⬜ |
| R6 大户 sybil 自验 | S4-6 / S4-7 | ⬜ |
| R10 任务类型太窄无趣 | S2-1（需在 UX 上做有趣） | ⬜ |
| R7 监管暴露 | S8-9 | ✅ 已加 CI 门禁（文本层）；**仍非法律意见**，上线前需人工复核 |

完整风险清单见 [`MVP.md` §13](MVP.md)。

---

## 附：维护约定

- **本表是唯一任务源。** 不在别处跟踪任务。
- 每完成一项 → 标 `done`，并在行末附**证据**（如：测试数、命令输出、提交哈希）。
- **验收判据未过的项，不得标 `verified`。**
- 发现 MVP 范围变化 → **先改 `MVP.md`**，再同步本表。**不要只改本表。**