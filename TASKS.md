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
| S9-1 | 引入官方 A2A SDK（wire 层不自研） | S9-0* | Agent Card / Message / Task 线格式与 A2A 1.0 兼容 | ✅ **done** — `github.com/a2aproject/a2a-go/v2 v2.6.0` 已成为直接依赖。**wire 类型全部复用 SDK**：`AgentCard` / `AgentInterface` / `AgentSkill` / `Task` / `TaskState` / `Message` 均未自研。`internal/a2a` 只做**别名 + RelayFirst 绑定**（见 S9-2）。**兼容性由测试钉住**：`TestCard_SerializesAsStandardA2A` 断言标准字段名齐全、**且 `agentId` 不是顶层字段**（否则等于 fork schema）。**依赖事实**：节点导入图中 grpc/genproto = 0（重半在 `a2agrpc` 子包，未触及）；`CGO_ENABLED=0` 构建正常 |
| S9-2 | `internal/a2a` 包骨架（card / task / session） | S9-1 | 包存在且有测试 | ✅ **done** — `internal/a2a`：`a2a.go`（SDK 原语别名）、`version.go`（协商，S9-0d）、`mapping.go`（版本映射表）、**`card.go`（CardSpec / Build / ValidateCard / SelectInterface）**。**关键安全决策**：`a2a` 包**保持无密码学**（不 import eip712/receipt/publish），否则节点一提供卡片就会链接签名代码 —— 违反 MVP §7.1。签名因此放在 `internal/publish`。**身份绑定**：A2A 没有"卡片属于某个 EVM 账户"的字段，新增会 fork schema（A9 禁止），故用 A2A **原生扩展**（URI `https://relayfirst.dev/a2a/identity/v1`），`Required: false`（标准客户端可忽略）。**27 项测试** |
| S9-3 | Agent Card 发布 + 发现（签名） | S9-2 | 能发布/查到 agent 能力 | ✅ **done** — 分三层，**刻意分离签名与存储**：<br>**① 签名（`internal/publish/card_proof.go`）**：**不能用 SDK 的 `a2acrypto`** —— 它只支持 ES256(P-256)/RS256，而 MVP §17.2 规定身份是 **EVM secp256k1 且"不能省"**；用第二把密钥会给每个 agent **两套互不一致的身份**。故证明是 **EIP-712 secp256k1**（domain `RelayFirst` / version `1`），**对卡片原始字节**取 keccak256（同 A9 §④ 的逐字理由：重序列化会静默作废签名）。<br>**② 目录（`internal/node/cards.go` + `internal/sqlite/cards.go`）**：节点**存字节、返回字节、绝不验签**（MVP §7.1）。每个响应**显式带 `verified:false`** 并附注说明 —— 裸列表会让读者**误以为节点已过滤**。卡片是"当前状态"（主键 agentId，重复发布覆盖）。<br>**③ 发现（`internal/publish/card_fetch.go`）**：`Fetch`/`List` **把拉取与验证合成一个函数** —— 不提供"先拿后验"的分步 API，因为"忘了验证"是这类端点最可能的误用。节点若**自称已验证**→ 视为**撒谎**并拒绝（它根本做不到）。<br>**新增第 13 道 CI 门禁**：`go list -deps` 实测**节点不能验签** + **挖矿核心不含 a2a**。**实测注入 `receipt` import → 门禁 FAIL**。<br>**端到端**：真实 HTTP 节点上 发布→列出→验证 全通；**恶意节点篡改端点被拒**。**共 40+ 项测试** |
| S9-4 | Session 生命周期（`SESSION_OPEN` / `_CLOSE`） | S9-2 | 两个 agent 能开会话 | ✅ **done** — `internal/a2a/session.go`：`DeriveSession` + `SessionEvent` + `SessionIDFor`。**会话刻意做薄**：不是连接（传输的事）、不是认证上下文（每个事件自己的签名）、不是订阅（节点目录的事）—— 因为会话是**双方必须一致**的那一份状态，每加一个字段就是一处可能分歧。**任一方可开可关**（会话不属于发起者，否则被放弃的任务会**每个泄漏一个会话**）；**重复 open/close 视为重试而非冲突**（对端 ack 丢失时会重发，报错会让重试不安全）。**`SessionIDFor` 由 opener + nonce 派生**（非随机）：同一意图必须得到同一 id，否则重试会创建**第二个会话**。**13 项测试** |
| S9-5 | Task 状态机（先 4 条超时边） | S9-2 | `OFFER`/`ACCEPT_START`/`INPUT`/`APPROVAL` 超时可用 | ✅ **done** — `internal/a2a/task.go`：**状态不存储，而是从签名事件推导**（`Derive`）—— 存储的状态是**声明**，推导的状态是**证明**。**核心约束（ARCH §4.2/§4.5）**：转换合法性**只由签名数据判定**（per-actor `sequence` + hash chain + 规则），**绝不用 relay 到达序**，否则两个 relay 会得出**不同状态 = 状态机分叉**。实现 4 条超时边（offer/accepted-start/input/approval），**其余 2 条（running heartbeat、disputed）由 `TimeoutCoverage()` 显式报告为未实现**（从状态机同一张表推导，不会漂移）。**`APPROVAL_EXPIRE` 目标可配**（§4.5 允许 policy 选 CANCELLED，默认 EXPIRED）。**返回结果含 Applied/Ignored/Unknown 三类**（裸状态不可证伪；Unknown 单独于 Ignored，否则**版本不匹配会看起来像协议 bug**）。**跨 actor 无因果序 → 用确定性 tiebreak（IssuedAt→Actor→EventID）**，文档明说这是**约定而非事实**。**21 项测试** |
| S9-6 | 消息/事件层（`ARCHITECTURE.md` §4.4 事件表子集） | S9-4 | 任务全生命周期事件可发可收 | ✅ **done** — `internal/a2a/event.go`：`Event` + `ValidateEvent` + `ValidateChain` + `EventHash`；`protocol/envelope.go` 新增 `KindEvent`；节点**零改动即可承载事件**（同一 envelope/store/pull 路径）。<br>**关键安全发现（由变异测试暴露，非事后补测）**：原 `EventHash` **总是重序列化结构体**再哈希 —— 而重序列化会**静默丢弃本 build 不认识的字段**，且**哈希值不变**（哈希本来就没覆盖该字段）。后果：**一个重序列化的节点会销毁新版本 actor 的字段，而所有链校验仍然通过** —— 正是 A9 §④ 要防的失效。**修法**：新增 `Raw`（收到的字节，`json:"-"`）+ `SignedBytes()`（有 Raw 用 Raw）+ `DecodeEvent`/`WithRaw`；**实测移除 Raw 路径 → 测试 FAIL**。同时 `ValidateEvent` **不再拒绝未知事件类型**（A9 §①：丢弃不认识的事件 = 静默截断别人仍能读的历史），未知类型由 `Derive` 报为 `Unknown`。<br>**新增 CI 门禁**：跨版本矩阵从 39 扩到 **88 项**（纳入全部事件/会话/状态机测试）。**端到端**：真实 HTTP 节点上完整生命周期（created→offered→accepted→started→waiting_approval→approved→completed）发布→拉取→链校验→推导状态**全部一致**；**节点承载它无法验证的事件**（客户端才拒绝）；重复投递幂等。**共 50+ 项测试** |
| S9-7 | `verification` 字段（§5.0） | **S9-0\*** | `recompute` / `evaluator` / `dispute` 三值入 schema | ✅ **done** — **实测确认版本化地基生效**：加 `task.verification` 现在是 **minor 变更** —— 探测显示当前 v1 验证器**接受**带该字段的回执（`Unmarshal` 容错 + 子集匹配），正是 MVP §17.5 的承诺。**三值全部入 schema**（含未实现的 `dispute`），使日后加它**不改线格式**。<br>**关键承重点（canonical 写入器）**：`verification` **仅在非空时写出**。这不是装饰 —— 字段存在前签的每份回执的 canonical 字节里**没有**这个 key，而那些字节正是其 payload hash 覆盖的对象；**无条件写出（哪怕空串）会改变全部历史回执的字节 → 改变哈希 → 作废所有已签签名**。**实测**：改成无条件写出 → `TestVerification_EmptyKeepsHistoricalBytesUnchanged` **和冻结语料**同时 FAIL（确认真实历史回执会失效）。<br>**空值语义 = recompute 而非"未知"**：字段存在前的回执都是重跑验证的，所以空值不是信息缺失，而是"这是 recompute 任务"。默认取**最强**模式，让含糊的回执按**更难**的情形处理。<br>**`Valid()` 与 `Implemented()` 分离**：`dispute` 合法（在 schema 内）但**未实现** —— 合并二者会让一份格式正确的回执看起来像坏的，与 schema major 的 unsupported/invalid 之分同构。<br>**安全性**：`verification` 在**签名载荷内**，改它会让签名恢复出**不同地址** → 攻击者无法把 `recompute`（二值）降级为 `evaluator`（可收买）。**9 项测试** |
| S9-8 | `recompute` 模式接线到 v1.0 验证器 | S9-7 | A2A 任务走现有二值验证 | ✅ **done** — `verifier.Verify` 新增**模式闸门**：仅 `recompute` 可走，其余返回**新错误 `ErrModeNotSupported`**。<br>**为什么是拒绝而非降级**：本验证器实现的是 `recompute`（重跑比对）。`evaluator` 任务**无法这样验** —— 评估者判断不是可重现计算，重跑得到的结果**不必相同**，比对无意义。**静默回退到 recompute 会给一个从来不具备二值判定的任务出具二值结论**，这比没有结论更糟：**把弱声明洗成强声明**。<br>**闸门在昂贵工作之前**（模式是回执自身字段，拒绝应当零成本）—— 实测：移除闸门后 `recomputer` 被调用，测试 FAIL。<br>**`ErrModeNotSupported` 独立于"验证失败"**：两者修复方式不同，合并会让运维去找伪造者，而真正的问题是**叫错了验证器**。**8 项测试** |
| S9-9 | `evaluator` 模式机制（只做机制） | S9-7 | 评估者可被指定并出结论 | ✅ **done** — `internal/verification/evaluator.go`：`Evaluation` + `Evaluator` 接口 + `Verifier.Evaluate` + `DesignateEvaluator`。<br>**诚实边界（MVP §5.0 强制要求）**：**评估者可以撒谎、可以被收买、可能与生产者串通**。所以 ①**结论永远归因**（`EvaluatorID` 必填 —— 无归属的意见无法权衡）；②**`Rationale` 必填**（无理由的意见无法复核，而复核是让弱结论可用的唯一途径）；③**结果里显式写明"是意见不是二值事实""弱于 recompute"**（不是只写在文档里，而是作为返回值的一部分，让调用方无法默认它等同于 recompute）。<br>**自评被拒且这里更要紧**：recompute 至少有"数字必须吻合"约束，**意见没有** —— 生产者评自己的摘要只需说"很好"。<br>**"无意见"≠"负面意见"**：评估者不可达 → **error**，绝不记成 rejected。<br>**双向闸门**：evaluator 机制**拒绝服务 recompute 任务**（否则等于用弱方法替换强方法）。<br>**诚实记录的缺口**：`receipt.Verification` 块**没有 rationale 字段**（它是为 recompute 设计的，证据是 hash）。S9-9 范围是"只做机制"，故**不假装存储 rationale** —— 只记"谁判的 + 判定结果"。**后果如实说明**：记录比 recompute 结论**信息更少**，这与 evaluator 是更弱的模式一致；未来 schema 应补字段而非让消费者猜。**10 项测试** |
| S9-10 | 回执关联 `task.a2aTaskId`（v1.0 已预留字段） | S9-5 | 回执能回溯到 A2A 任务 | ✅ **done** — `a2aTaskId` 从"预留"变为"实际使用"（MVP §16）：新增 `ValidateA2ATaskID` / `Task.SetA2ATaskID` / `Task.ClearA2ATaskID`，并在 `ValidateStructure` 中校验。<br>**校验刻意宽松**：A2A **未规定** task id 格式（SDK 的 `TaskID` 是不透明字符串，由 agent 自选），所以**不能**校验语法 —— 强行校验会无理由拒绝新版本对端的 id。只查两件对互操作有意义的事：**非空**（空链接无意义）+ **非超长**（不能当无界存储）。**宽松检查仍值得有**：从不校验的互操作字段会漂移，两个 agent 可能往里写不同东西（一个写 task id、一个写 URL），直到消费者去跟随链接才失败。<br>**nil ≠ 空串**：自生成的工作**没有** A2A 任务，记 nil 而非编造 id（编造比不记更糟：消费者跟随链接会找不到任务，且无法与"任务被删"区分）。<br>**⚠️ 一处如实修正（由变异测试推翻我原先的说法）**：我最初写"给 `a2aTaskId` 加 `omitempty` 会剥掉历史回执的 key 并作废所有签名"。**变异测试证明这是错的** —— 签名字节来自 `canonical.go` 的**显式字段列表**，不走结构体 tag，所以**只改 tag 完全不影响冻结语料**。**真正危险的是从 `canonical.go` 的 Task case 删掉该字段** —— 那个变异确实让冻结语料 FAIL。已同时修正代码注释与测试注释，并新增 `TestA2ATaskID_CanonicalWriterEmitsTheKey`（指向真正的那一行）+ `TestA2ATaskID_StructTagMatchesTheWriter`（tag 与写入器不得分歧，否则导出路径会与签名形式不一致）。**12 项测试** |
| S9-11 | A2A 端到端测试（判据 ⑧） | S9-1..10 | 两 agent 闭环，wire 兼容 | ✅ **done** — `TestAcceptance_A2AClosedLoop`：**判据 ⑧ 的完整序列**，在**真实 HTTP 节点**上跑 —— **发布 Agent Card（双绑定）→ 发现并验证 → 开会话 → 派任务 → 推事件（WebSocket）→ 出回执（关联 A2A 任务）→ 离线验证**。<br>**为什么是一条长测试而非几条**：判据 ⑧ 陈述的是一个**序列**，每一步都能单独通过而**两步之间的交接**出错 —— 卡片端点的会话用不了、任务事件回执引用不了、回执的验证模式验证器拒收。所以本测试跑完整路径并断言**接缝**，组合失败正住在那里。<br>**两个 agent 真正独立**：不同密钥、不同身份、不同事件链（单 agent 测试不会检验归因、自验规则、以及"一个 actor 的链对另一个 actor 无话可说"）。<br>`TestAcceptance_WireIsA2ACompatible`：**用 SDK 自身的类型**反序列化我们产出的卡片 —— 若未经改动的 SDK 客户端读不了，那"兼容 A2A"就是我们没兑现的说法。**2 项大型端到端测试** |
| S9-12 | **WebSocket 传输绑定**（新增，HTTP 保留，§12.1） | S9-1 | `AgentInterface` 声明两个绑定 | ✅ **done** — 依赖 `github.com/coder/websocket v1.8.15`（ISC，纯 Go），**`gorilla/websocket` 全程缺席**（实测 `go.mod`/`go.sum` 均为 0）—— MVP §12.1 因 advisory **GO-2026-6278**（弱 PRNG mask key）明令禁用。<br>**新增 `GET /ws/messages/{agentId}` 推送**；**四个既有 HTTP 端点全部保留**（实测断言，含 well-known 同时广告新旧端点）。<br>**最关键的一条：WS 只解决"推送"，不解决"全序"**（MVP §12.1 要求必须写清，否则有人会以为 WS 给了全序）。故**每一帧都带 `deliverySeq` 并注明它是本节点局部计数、不是协议序** —— 用 `deliverySeq` 而非 `sequence` 命名，正是为了不让它被误当成签名内的 per-actor 序。**实测**：删掉该说明 → 测试 FAIL。<br>**推流不阻塞发布方**：`publish` 在**存消息的 HTTP 请求里**被调用，所以**绝不阻塞** —— 缓冲满则标记丢弃并关闭该订阅者，客户端重连并拉取（该路径已存在且已测）。**慢订阅者不能把自身问题变成节点的问题。**<br>**重复投递不再广播**（否则重试会对每个订阅者显示为"新活动"）；**只通知该 agent 的订阅者**。**11 项测试** |

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
| S9-0c | domain **多版本验证**（try-all + 只增不减版本表） | S9-0b2 | 旧回执永久可验 | ✅ **done** — **实测发现比任务描述更严重的设计缺陷**：原 `typedDataForMajor` 用 `major >= 2` 选 profile，等于"v2 及之后一切" —— 这是**静默错答**，v3 改字段那天，v3 回执会继续按 v2 规则验证并**通过**（多出的字段被忽略），门禁在它从未真正检查过的回执上变绿。**修法**：`typedDataForMajor` 对**不在 `supportedMajors` 的 major 直接返回 `UnsupportedError`**，把静默错答变成响亮回答（S9-0j 已教会调用方如何上报）。同时把 profile 构造拆成 `profileForMajor`（**不查支持表**），使 profile **规则本身**可被直接测试，无需往 `supportedMajors` 塞假版本（"发布前测不了" 正是 relabel 洞当初进来的方式）。**注意方向**：这是"**按声明 major 选 profile**"，**不是**"try-all 全部 profile 只要一个过就算过" —— 后者会**重开 B3**（改标 v1→v2 再按 v2 验）。所以本任务在 A9 语境下记为：多版本验证 = **per-major 表驱动** + **未知 major 硬拒绝**。矩阵测试 `TestTypedData_SupportedMajorsHaveDistinctProfiles`（每个受支持 major 一个**互异** digest，重合即 relabel 洞）+ `TestTypedData_UnknownMajorIsRefused`（v0/v2/v3/v99 全部拒绝）。**非空转**：移除支持表守卫 → 4 个 major 全部被建出 profile、`future major cannot be signed` 失败 |
| S9-0d | 采用 A2A 的 `A2A-Version` / `AgentInterface`，**不自研协商** | S9-1 | 无交集时用 `ErrVersionNotSupported` 显式失败 | ✅ **done** — **实测确认了计划 §2.6 的假设**：官方 SDK 存在且可拉取 —— 模块路径 `github.com/a2aproject/a2a-go/v2`，**v2.6.0**，Apache-2.0。**不自研**：`a2a.SvcParamVersion = "A2A-Version"`、`SvcParamExtensions`、`ErrVersionNotSupported`（sentinel，`errors.Is` 可用）、`AgentInterface{URL, ProtocolBinding, ProtocolVersion}` **全部来自 SDK**，新包 `internal/a2a` 只做**别名 + 协商**，不复制、不重定义（`TestPrimitivesAreTheSDKs` 钉住拼写，防漂移）。SDK **没有**给出"协商"函数，所以协商是本包唯一自研逻辑，且只建立在官方 key/sentinel 之上。<br>**① `ParseVersion`**：严格解析 `M.m` / `M.m.p`，**patch 按规范丢弃**（规范明言 patch 不影响兼容性 → 保留会导致把 1.0.7 与 1.0.2 判为不兼容）。<br>**② `Negotiate`**：取**最高公共 major.minor**；**无交集 → `ErrVersionNotSupported` 显式失败，绝不静默降级**（计划 §2.6 硬要求）；空 header = 0.3 客户端 → 用我方最新（规范语义）。<br>**③ 保留 `malformed` vs `unsupported` 之分**：解析失败是**客户端 bug**，不该报 `VERSION_NOT_SUPPORTED`（否则运维会去加一个不存在的版本）—— 与 receipt 层的 `ValidationError`/`UnsupportedError` 同构。<br>**④ 映射表 `SchemaRelation`（计划 §2.6 要求）现为受测代码而非散文**：A2A 版本 ↔ 可**写入**的 schema major；**只 gate 写入，不 gate 验证**（验证永久，§2.7）—— 混淆二者会违反 A9 §①。未知版本取映射是**错误**而非空列表（空列表与"此版本禁止写入"不可分）。<br>**依赖事实（已核）**：`go mod tidy` 后 a2a-go 为**直接**依赖；节点二进制导入图中 `google.golang.org/grpc`/genproto **为 0**（重半在 `a2agrpc` 子包，未触及）；`CGO_ENABLED=0` 构建正常（A3）；**节点无 CGO** 与**mine core 不含 a2a** 两条边界经 `go list -deps` 实测仍成立（`MVP.md` §8.5 要求"a2a-go 只出现在身份/序列化边界，不得进入挖矿核心"）。**已知遗留**：`internal/a2a` 尚未被生产路径导入 —— 由 **S9-1（wire 层）/ S9-12（WebSocket 绑定）** 接入；本任务交付的是受测原语 + 映射。
| S9-0e | 消息契约占位：`sequence` + `previousEventHash` 字段位置 | S9-0b2 | 现在可恒空，但**位置现在定** | ✅ **done** — 在 `protocol.Envelope` 上保留 `Sequence uint64` 与 `PreviousEventHash string`，**均 `omitempty`**。**`omitempty` 是承重结构**：未入链的消息序列化后**逐字节等于**新字段存在之前 —— 否则每个已存储/在途信封会在结构体变更那一刻改形，正是 A9 要防的"加字段 = 给读者分叉"。关键设计：这两个是**消息层**字段（排序**投递**），**不在回执签名载荷内** —— 所以日后引入消息链**不触碰任何回执的签名字节**。测试 `TestEnvelope_ZeroChainFieldsAreAbsentFromTheWire` 断言**原始字节**（非往返：往返在键存在时也会过）+ 往返携带 + 未知字段容忍（A9 前向兼容，同 S9-0b 规则）。**非空转**：去掉 `omitempty` → 零值键出现在线上，测试 FAIL |
| S9-0f | CI **G1 冻结语料** `testdata/receipts/v<major>/*.json` | S9-0b2 | 每个历史 major 至少一份真实签名回执，**只增不删** | ✅ **done** — `testdata/receipts/v1/`（2 份真实签名回执，含 1 份逐字 payload 形态）；生成器 `internal/devtools/gen_frozen_receipts.go`；消费测试 `corpus_test.go`（断言每份仍可验 + 当前 major 有语料 + 含逐字形态）。**已知缺口**：缺结构性路径样本（L2）、缺 per-major 验证入口（M2）、语料无 SHA-256 清单（M1） |
| S9-0g | CI **G2 跨版本矩阵** + **G3 未知字段注入** + **G4 协商无交集** | S9-0f | 三条门禁全绿 | ✅ **done** — 新增**第 12 道门禁**（`scripts/ci.sh`），按**行为**而非三个具体测试函数名选择，覆盖三包：**G2 跨版本**（`TestTypedData_*` / `TestValidateForMajor_*` / `TestFrozenCorpus`）—— v1 产物须在 **v1 规则下可验**、在 v2 规则下被拒、且 profile 互不碰撞；**G3 未知字段注入**（`TestValidate_Additive*` / `TestEnvelope_AcceptsUnknownFields`）—— 回执载荷内与信封上的增量字段均须被容忍；**G4 协商无交集**（`TestNegotiate*` / `TestNegotiator_*` / `TestParseVersion` / `TestVersionCompare`）—— 无交集须以 `ErrVersionNotSupported` 失败，**绝不回退**。<br>**修法**：已按行为**命名选择**（pattern），并将门禁**计入失败数**（是真门禁，不是报告）。<br>**关键防呆**：`go test -run` **零匹配时退出码为 0**，所以"选择过期"会**看起来像成功** —— 加**计数断言**（< 30 即失败）把"没跑"变成失败，改名无法悄悄退休该门禁。**实测**：把 pattern 换成不存在的名字 → 退出码 0 但 pass_count=0 → 门禁报 "selection has gone stale"（正确失败）。当前 **39 项**通过。<br>**与 G1 的分工**：G1（已冻结语料）只钉"历史回执仍可验"；G2/G3/G4 钉的是**新增版本时才会踩到的路径** —— 这些路径最难察觉，因为今天全绿、明天仍全绿。 |

### 10.1b S9-0 安全审查：新增的发布前阻断项

> **审查结论见 [`docs/notes/s9-0-security-review.md`](docs/notes/s9-0-security-review.md)。**
> **结论：S9-0 比原计划大。** 审查发现 4 个发布前阻断项 + 1 高 + 3 中 + 3 低。

| id | 任务 | 严重度 | 状态 |
|---|---|---|---|
| **S9-0h** | **把 `receiptId` 与 schema 绑进签名载荷** | **critical（B2+B3）** | ✅ **done** — ①**`receiptId`**：`Validate` 重算 `DerivedReceiptID()`（= `sha256(SignedPayload())`）并比对；**不改签名字节**，故对 v1 回执同样生效（`checkReceiptID`）。②**schema**：引入**版本 profile** —— v1 的 domain `Version` 与消息字段**逐字节冻结**（3 字段），v2 起绑定 `receiptId` + `schemaMajor`（5 字段，domain `Version="2"`）。**改标 v1→v2 会换 digest → 签名失败**。**实测**：`TestTypedData_V1ProfileIsFrozen` 锁定 v1 形状；`TestTypedData_ProfilesDiffer` 证明两 profile digest 不同；`TestReceiptID_TamperedIDIsRejected` 断言重发被拒。**非空转**：禁用 `checkReceiptID` → 测试 FAIL |
| **S9-0i** | **`canonicalJSON` 加数字分支**（`float64` / `json.Number`） | **high（H1）** | ✅ **done** — 加 `float64` / `float32` / `json.Number` 分支。**浮点渲染委托给 `encoding/json`**（不手写格式化规则）—— 保证结构性路径与逐字 payload 路径**按构造一致**；手写是 Go/TS 漂移的根源（正是 A4 要防的）。`json.Number` **先校验 JSON 数字语法**再输出（它是字符串类型，不校验会把任意字节塞进签名字节）。**非空转**：移除 `float64` 分支 → 往返测试 FAIL |
| **S9-0j** | **生产调用方区分 `UnsupportedError`** | **high（H2）** | ✅ **done** — 新增 `receipt.IsUnsupported(err)`（用 `errors.As`，**能穿透包装**）；三处生产调用方接入：**CLI** `reportVerifyFailure`（输出明确写"**不是**伪造指控，是**无法判定**"）、**`SelfCheckVerdicts`**（记录原因，避免把"节点太旧"记成"提交有问题"）、**`mining.VerifyReceipt`**。**关键**：`false` 仍是 `false`（无法判定时不能断言已做），改的是**理由可见**而非结论。**测试断言两个方向**：未来 major **不是** `ValidationError`；畸形 schema **不是** unsupported。CLI 层测试断言操作员看到 "upgrade the verifier" 且含 "NOT a claim that the receipt is forged"。**非空转**：禁用 CLI 分支 → 测试 FAIL |
| **S9-0k** | **语料 SHA-256 清单 + 生成器拒绝覆盖** | medium（M1） | ✅ **done** — `testdata/receipts/v1/MANIFEST.sha256`（sha256sum 格式）；`TestFrozenCorpus_MatchesManifest` 断言**每个文件的哈希**，并检测**清单有而文件无**（删除历史回执同属此类）。生成器**拒绝覆盖**已存在文件并给出明确信息。**非空转**：只改一份回执的**空白字符** → 门禁 FAIL |

**M2 与 L2 的缺口也已关闭（随 S9-0k 一并交付）：**

| 缺口 | 修法 |
|---|---|
| **M2** | 新增 `receipt.ValidateForMajor(major, ...)` —— **拒绝 major 与回执声明不符**（否则测试 API 会把版本错配"洗成"通过）；`SupportedMajors` / `IsSupportedMajor` 导出。语料测试**按目录名分派**，所以钉住的是"v1 规则仍接受 v1 回执"而非"今天的规则恰好接受" |
| **L2** | 语料新增 `compute-structural.json`（**不带 payload**），使**结构性重建路径**（所有历史回执依赖的路径）被永久样本钉住，而非只靠同 build 的单元测试 |
| **L1** | **比审查报告更严重。** 原描述为"chainId 未绑定"，实测是**从未解析** —— `agent:eip155:` 与地址之间的字段可为**空串 / 非数字 / 负数 / 十六进制 / 溢出 u64**，只要地址与签名一致，`Validate` 全部通过。签名在此**不救场**：chainId 在被签的 `agentId` 字符串里，被认证的是"这个字符串"，不是"一个合法 chain id"。修法：`agentAddressFromID` 走 `strconv.ParseUint(chainStr, 10, 64)` 并拒绝空串；新增 `AgentChainID` 让按链路由的消费者复用**同一套**解析，避免校验与消费各判各的。测试 `TestAgentID_ChainIDIsValidated`（6 个子用例）+ `TestAgentID_ChainIDAgreesWithAddress`（两半解析互洽）。**非空转**：移除 `ParseUint` → 5 个子用例 FAIL |
| **L3** | `schema_test.go` 两处 payload 一致性断言改为**先** `errors.As(&ValidationError)` 且 `!IsUnsupported(err)`，字符串检查降为**次要**（只确认消息点了字段名）。这样"篡改被判成版本不兼容"这类**分类漂移**会让测试失败，而纯字符串断言不会 |
| **M3** | **文档类修复**（协议层无法消除："signer 用了哪种序列化"本就不进签名）。在 `receipt.go` 的 `Payload` 字段上写明：消费者**只读经 `Validate` 的结构化字段**，不读 `Payload`；`Validate` 已保证两者一致，这是任一方可信的唯一理由 |

**发布门槛（来自审查）：**

```text
首发前必修：  B1 ✅ · B2 ✅ · B3 ✅ · H1 ✅ · B4 ✅
声称 A9 合规前：H2 ✅ · M1 ✅ · M2 ✅ · L2 ✅
已清理：      M3 ✅ · L1 ✅ · L3 ✅
```

**10 项全部关闭。** 每一项都附**非空过**证据（改修复 → 施加对应篡改令测试失败 → 还原令测试通过）。

> **✅ 九个必修项全部解决**（五个首发阻断 + 四个"声称合规前必修"）。
> 剩余三项（M3 消费者不得把 `Payload` 当权威元数据 / L1 `agentId` 的 chainId 未绑定 /
> L3 测试断言错误字符串）经审查判定**可后置**。
>
> **⚠️ A9 现在可以说"机制已就位并被门禁锁定"**，但仍**不等于**"协议已上线" ——
> 上线仍受 BLK-1/2/3/4 与人工动作所阻（见 §1）。

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
| S10-1 | 观测索引（`GET /observations`） | S9-1 | 能按 subject 查询 | ✅ **done** — `internal/sqlite/observations.go` + `internal/node/observations.go`。<br>**核心裁决（见下）**：**索引 ≠ 验证**。节点按 **JSON 路径**读出 `subject` / `contentHash` / `resultHash` / `agentId` / `epoch`，**不做任何验证** —— 这不是偷工，**这就是索引的定义**。搜索引擎索引网页时不为真实性背书；消费者取回后验签，**伪造的自动被丢弃**（与"节点从未索引它"结果相同，**但诚实的变得可被找到**）。<br>**关键**：节点**仍然不能验签**，这由 `go list -deps` 门禁钉住（实测节点导入图**不含** `eip712`/`receipt`/`scoring`/`mining`）。所以"节点不能伪造"这条叙事**完好无损**。<br>**表内没有 validity 列**（故意的）：那会是节点**无权做出**的判断，而读它的消费者等于在信节点的意见。<br>**索引失败不阻断投递**：读不懂的回执**仍然存储转发** —— 把索引限制变成投递失败 = 节点**静默审查**它看不懂的流量。**9 项测试** |
| S10-2 | 交叉验证视图（`GET /observations/{id}/evidence`） | S10-1 | 多 agent 独立验证可见 | ✅ **done** — 按 `contentHash` **分组**，统计 **`distinctAgents`**。<br>**"distinct" 是承重词**：同一 agent 的两份回执是**同一主张的重复**，按行计数会让**单个 agent 制造"共识"**。**实测**：改成按行计数 → 测试 FAIL。<br>**分组而非扁平列表**：关键问题不是"谁看过这个 URL"，而是"**谁对所见的看法一致**"。两个组 = 观察者看到不同东西（真实变化或值得调查的分歧）；扁平列表会把这点藏在一个计数后面。<br>**不下的结论**：一致**不是**正确 —— **两个 agent 也可以串通**。响应显式写明。 |
| S10-3 | 任务中转（`POST /tasks` · `GET /tasks` · claim） | S9-5 | 节点能中转任务 | ✅ **done** — `internal/sqlite/tasks.go` + `internal/node/tasks.go`：`POST /tasks`、`GET /tasks?subject=`、`POST /tasks/{id}/claim`。<br>**节点拒绝做的三件事，以及为什么**：<br>① **claim 记为"兴趣"，绝非"授予"** —— 响应恒为 `granted:false` 并明说。节点**无权威**可授予排他性；把它做成有权威的 = 变成**市场运营方**而非中继。让竞争落定的是**协议**（需求方只接受一份 offer，其余人的工作**没有需求方认可的回执**）。**实测**：把 `granted` 改成 true → 测试 FAIL。<br>② **两个 executor 可以同时 claim**，节点**不仲裁**（实测两次 claim 都返回 200）。<br>③ **过期只存不判** —— `expiresAt` 记录并返回，但**不按自己的钟过滤**。过期是**协议转换**（ARCH §4.5，由签名数据判定）；relay 按本地钟过滤 = **臆造它没有的权威**，且两个钟偏斜的 relay 会对"是否仍开放"给出不同答案。**实测**：已过期 offer 仍被列出。<br>**防通胀**：`(task_id, claimant)` 为主键 —— **一个 agent claim 三次是 1 份兴趣**（claim 数是需求方判断"板子是否活着"的依据，可被刷高就是撒谎）。<br>**规格原样携带**：`spec` 是 `json.RawMessage` —— 节点**不拥有**任务格式，所以**不得丢弃它不认识的字段**（实测 `futureField` 存活）。<br>**重发是更新而非重复**（重试不该让任务出现两次）。**11 项测试** |
| S10-4 | Agent Card 发现端点 | S9-3 | `GET /agents` | ✅ **done（已在 S9-3 交付）** — `GET /agents`（列表）+ `GET /agents/{agentId}`（单个）+ `POST /agents`（发布）已在 S9-3 完成并测试。**本任务无需额外代码**，此处仅登记以保持任务表与事实一致。<br>**注意**：S9-3 的端点**同样不验签**（`verified:false` 恒返回），与 S10 的索引原则一致 —— 目录是**发现**手段，不是权威。 |
| S10-5 | **可归因验证**：节点签自己的结论 | S10-1 | 节点撒谎留下证据 | ✅ **done** — 新包 **`internal/assertion`**（**独立组件**，非节点的一部分 —— 见 §10.2b 的裁决）。<br>**核心**：验证者给结论 = **签一份 `Assertion{receiptId, receiptHash, verifierId, verdict, reason, assertedAt}`**（EIP-712，domain `assertion-1`，与回执/卡片域**不同**）。**撒谎 = 签名的谎言** —— 这不使节点诚实，但使**不诚实可被证明**，这正是后续 slashing 阶段所需。<br>**必须绑定回执字节**（`receiptHash`）：只写 receiptId 的话，断言可被重放到任何**同 id** 的回执上，而 id 由生产者自选 —— **不诚实的生产者可以铸一个 id 撞上已有有效断言的回执**。**实测**：去掉字节绑定 → 测试 FAIL。<br>**三值 verdict 且非 valid 必填 reason**：`unsupported` **不是指控** —— "我验不了"（新版本 schema / 未实现模式）**必须**区别于"它是假的"，否则**过期的验证者会变成指控者**（与回执层 unsupported/invalid 之分同构）。无理由的指控**不可复核**，而复核是让负面结论可用的唯一途径。<br>**验证者身份从签名恢复**，不额外存字段（避免第二个真相源与签名分歧）。**16 项测试** |
| S10-6 | 客户端独立复验（不盲信节点） | S10-5 | 节点撒谎时客户端仍能独立判定 | ✅ **done** — `assertion.Check`：**把断言当输入来评估，而非当答案来信**。<br>**顺序即设计**：**先查回执本身**（签名/结构/id），再评估断言 —— 若先看断言，就等于**让验证者决定结果**。<br>**不依赖验证者诚实的那个检查**：客户端**自己**的结论是唯一它能**证明**的东西。实测：验证者对**被改写的字节**签"valid" → 客户端仍**拒绝**该回执，且**分歧被显式报告**（两个方向都测：客户端判 valid / 验证者判 invalid，反之亦然）。<br>**沉默不算同意**：无断言时 `Agrees=false` —— "没有主张"**无法**与任何东西一致。伪造断言（改 `verifierId` 不重签）**权重为零**且与"可归因但错误"**区分开**。<br>**判据 ② 仍满足**：无任何断言时回执照样离线验证通过。**实测**：让客户端跳过自查 → 2 项测试 FAIL。 |
| S10-6 | 客户端独立复验（不盲信节点） | S10-5 | 节点撒谎时客户端仍能独立判定 | ⬜ todo |
| S10-7 | 节点端到端测试（判据 ⑩） | S10-1..6 | 索引可用 + 不盲信 | ✅ **done** — **判据 ⑩ 两半均达成**：<br>**① "节点能索引/查询/中转"** — S10-1/2/3 共 20 项端到端测试（含"索引不可用不阻断投递""不仲裁""不按本地钟过滤"）。<br>**② "客户端在节点撒谎时仍能独立判定"** — S10-5/6 共 16 项，核心是 `TestCheck_ClientDecidesIndependently`（验证者对改写字节签"valid"，客户端仍拒绝）与 `TestAssertion_VerifyDoesNotClaimCorrectness`（可归因 ≠ 正确）。<br>**③ "节点的验证结论可归因"** — `assertion.Assert` 产生**签名的**结论；谎是**签名的谎**。<br>**两条边界同时成立（实测）**：节点导入图**仍不含** `eip712`/`receipt`/`publish`/`scoring`/`mining`/`assertion`（**节点不能伪造**），而验证者组件**能**出可归因结论 —— 这正是 §10.2b 裁决要的两全。 |

### 10.2b S10 的文档冲突与裁决（`AGENTS.md` §7.3：矛盾须停下来问）

**发现的矛盾（在 L0 `MVP.md` 内部）：**

| 位置 | 说法 |
|---|---|
| **§7.1** | 节点"**不做 EIP-712 验签**"，"节点是哑的……不验证、不裁决" |
| **§7.3 / S10-5** | 节点"**可以验证**，但必须把自己也签一份"（§7.3 自称是对 v1.0 的**纠正**） |

**已确认的裁决（用户裁定，2026-10-04）：**

> **索引器与验证者是两个角色。** 最小节点保持**无密码学**；"可归因验证"作为**独立的可选组件**实现。

**裁决依据：**

1. **§7.3 真正要的能力是"让产出可被消费"**，这由**纯 JSON 解析**满足（S10-1/2/3 已交付），**不需要密码学**。**解析 ≠ 验签。**
2. **§7.1 的结构保证仍然成立且是真实资产**：节点**不能伪造**，由 `go list -deps` 门禁钉住。
3. **一条 `docker run` 仍是无密钥的最小节点**（§7.2 的承诺不被破坏）。
4. **"可归因验证"本质是另一个组件** —— 它需要密钥，而**持密钥的节点是不同的信任对象**（需质押、追责，属 S12 地界）。
5. **客户端反正必须自己验**（判据 ② 要求离线可验），所以节点验证**只是加速器**，不是安全属性。

**因此 S10-5/S10-6 的正确形态是**：一个**独立的、可选的验证者组件**（自带密钥、自带可归因结论），
**而非把签名代码塞进最小节点**。这保留了 §7.1 与 §7.3 的**共同意图**，且不牺牲任何能力。

**⚠️ 状态更新（S10-5/6 已完成，但判据 ⑩ 仍未完全达成 —— 自我纠正）**：

S10-5 与 S10-6 已实现 `internal/assertion`（见上表）：**可归因验证的机制**已具备，客户端独立复验已具备。

**但判据 ⑩ 的原文是「节点的验证结论可归因（节点签自己的结论）」—— 这一点尚未达成。** 实测：

- `node.Config` **没有任何密钥字段**；
- `/.well-known/relayfirst` **不公布节点地址**。

所以**"节点"本身还不能签任何东西** —— 目前能签的是 `assertion.VerifierRunner`（一个**独立组件**，需调用方注入密钥）。
**结论：机制已备，节点身份未接。** 这正是 **S10-0** 要补的。**在 S10-0 完成前，不得声称判据 ⑩ 已过。**

### 10.2c S10-0 —— 节点身份（**P0 前置，不补后面立不住**）

> **来源：** 外部评审清单（2026-10-04）。**核实结论：真缺口，且是本仓库上一轮的一处过度声称。**
>
> **为什么是 P0：** 判据 ⑩ 的原文是「节点的验证结论**可归因**（节点签自己的结论）」。
> S10-5 交付的是**机制**（`internal/assertion` 能签、能验、客户端不盲信），
> **但"节点"本身还不能签任何东西** —— 实测 `node.Config` 无密钥字段、`/.well-known/relayfirst` 不公布地址。
> **机制已备，身份未接。** 在此之前**不得声称判据 ⑩ 已过**。

| id | 任务 | 依赖 | 验收 | 状态 |
|---|---|---|---|---|
| **S10-0** | **节点身份：keypair → EVM 地址；well-known 公布；结论用该 key 签名** | S10-5 | **双节点对同一回执结论不一致时，客户端能指出是哪个节点地址撒了谎** | ✅ **done** — **ADR-0004 已裁定并执行（采纳分二进制）**。<br>**① 节点侧（`internal/node`）**：`Config.NodeID`（**字符串，不是密钥**）+ `Config.Asserts`；well-known 新增**可选** `nodeId`。<br>**承重点在于"NodeID 是字符串而非私钥"** —— 节点**从不持密钥**，因此它**不能签任何东西**（既不能签结论，也不能签工作证明）。这正是判据 ⑦ 那条导入图门禁要守的东西。**无身份时字段整个省略**（不发空串）：客户端读空串**无法区分**"无身份"与"未公布身份"，而这正是该字段要回答的信任问题。<br>**⚠️ 文档诚实性**：well-known 的 note 现按两种状态**生成**（不再是固定串）—— 有密钥说"**可归因的主张，请自行独立验证**"，无密钥说"**不持密钥、不做验证主张、不能签任何东西**"。固定串要同时准确只能写得含糊，而含糊正是这个字段要防的失败。**4 项测试** |
| | **② 验证者侧（新二进制 `relayfirst-verifier`）** | | ✅ **done** — 三个子命令：`identity`（**从密钥派生**，不可自选）、`assert`（重查并出**签名断言**）、`check`（**客户端路径，不需要密钥**）。<br>**为什么必须分二进制**：把密钥接进节点**必然**让 `relayfirst-node` 链接 secp256k1 → **门禁必然失败** → 诚实的修法只能是**放宽门禁**，用一条**已测保证**换一条方便的。分角色后**两条性质同时成立**：**无密钥 relay 不能伪造**，**有密钥 verifier 的谎是证据**。<br>**它不能做什么**：只签**断言**（"我检查了回执 X"），**绝不签回执**（"我干了这活"）—— 只有后者能刷分，且两者 **EIP-712 struct 与 domain 都不同**。<br>**新增 CI 门禁（实测有效）**：verifier 导入图**不得**含 `mining`/`scoring`（**不能构造回执**）；且**必须**含 `assertion`（否则该检查**空过**）。实测注入 `mining` → **门禁 FAIL**。<br>**诚实边界**：`recheck` 默认**离线**（验签名与结构），`--url` 才重取 anchor；重取**检测漂移而非当时的伪造** —— 故**默认关闭**并写明理由（诚实回执会因内容变化而后失败）。 |
| | **③ 判据 ⑩ 的验收测试** | | ✅ **done** — `TestCriterion10_TwoNodesDisagreeAndClientNamesTheLiar`：**两节点对同一回执给出相反结论**，客户端**能分别验证两条断言**、**能指出是哪个节点地址撒了谎**、且**自己的判定不受任何一方影响**。另有 `ClientIsNotSwungByEitherClaim`（**三个节点一致说假也不能推翻客户端自己的检查** —— 否则多数骗子获胜）与 `UnsupportedIsDistinguishableFromADisagreement`（**认不出 ≠ 撒谎**）。**3 项测试** |

**⚠️ 判据 ⑩ 现已真正达成** —— 与上一轮的记录不同：上一轮只有**机制**（独立组件能签），**节点本身无身份**；
本轮补上了 **S10-0**，两半齐备：**节点身份已公布**，**结论可归因**，**客户端不盲信**。**3 项验收测试锁定。**

**要做的事：**

1. `relayfirst-node` 接受一个节点私钥（**环境变量或文件，绝不经命令行** —— 命令行会泄漏进
   shell history 与进程表；`cmd/relayfirst/main.go:113` 已有此先例可循）。
2. 节点地址 = `agent:eip155:<chainId>:<address>`，与 agent 身份**同一套语法**（复用 `internal/agentid`）。
3. `/.well-known/relayfirst` **公布** `nodeId`（地址）与 `verifierKey` 的**公钥信息**。
4. 节点对回执出结论时用该 key **EIP-712 签名**（`internal/assertion` 已具备能力，只需注入节点身份）。
5. **⚠️ 结构约束不变**：这把密钥**不得**让节点获得"伪造工作"的能力。
   - **必须核对**：`internal/assertion` 只签名**结论**（`verdict`），**不签回执**；
   - 节点的**导入图仍不得**含 `eip712` 之外的签名路径 —— 但**`assertion` 依赖 `eip712`**，
     所以这是一个**需要 ADR 的结构决策**：节点身份一旦接入，`relayfirst-node` **必然链接 secp256k1**。
   - **这与现有 CI 门禁（"节点不能验签"）冲突，必须显式裁决，不能默默改门禁。**
     候选立场见下方 ADR-2。

**验收测试（判据 ⑩ 的真正形态）：**

```text
双节点 A、B 对同一回执给出相反结论（A: valid，B: invalid）
  → 客户端能分别验证两条断言
  → 能指出是哪个节点地址（agentId）撒了谎
  → 且客户端自己的判定不受任一方影响
```

---

### 10.2d ADR —— 去重账本的发行层立场（**P0 前置**）

> **来源：** 外部评审清单（2026-10-04）。**核实结论：真冲突，且 `docs/adr/` 尚不存在。**

**冲突：**

| 位置 | 说法 |
|---|---|
| `MVP.md` §5.2 / 不变式 **A6** | artifactKey 去重账本**必须是全局的**（非 per-agent） |
| `ARCHITECTURE.md` **NET-2** | **没有全局 canonical database** |

**这两句在字面上矛盾**，且 A6 是**硬不变式**、NET-2 是**网络不变式**，都属于不可违反的一类。

**建议立场（待你裁定，我不代决）：**

> **"全局"指"单发行域（issuance domain）内全局"，多发行方互不认。**

**理由（供参考，非结论）：**

- A6 要防的是**刷量收益不为零**（同一 artifact 被多 agent 重复提交仍得分）—— 这个防御**只在同一发行域内**才有意义，
  因为积分只在同一发行域内可比。
- NET-2 要防的是**官方中心数据库**成为真相来源 —— 它说的是**跨节点共识**，不是"同一发行方内部不能有账本"。
- 两者**作用域不同**，所以"发行域内全局"同时满足：A6 的防御成立，NET-2 的中心化禁令不破。

| id | 任务 | 依赖 | 验收 | 状态 |
|---|---|---|---|---|
| **ADR-1** | 写 `docs/decisions/ADR-0003-dedup-ledger-scope.md`：裁定"全局"的作用域 | — | 一份 **accepted** 的 ADR；后续多节点工作引用它 | ✅ **done** — 已 `accepted`（采纳"域内全局，域间互不认"）；`MVP.md` §5.2 已补作用域说明与"跨域不兑换"条款；已登记 `DOCS.md` |
| **ADR-2** | 写 `docs/decisions/ADR-0004-node-identity-crypto.md`：节点身份必然引入 secp256k1，如何与"节点不能验签"共存 | S10-0 | accepted ADR；CI 门禁随之显式更新（而非默默放宽） | ✅ **done** — 已 `accepted`（采纳分二进制）；**门禁已显式更新**（`relayfirst-node` 门禁**不变**，**新增** `relayfirst-verifier` 门禁）；S10-0 已按此实现 |
| **ADR-3** | cobra 偏离：`MVP.md` §8.1 选了 cobra，实际是标准库手写 | — | 二选一：**补齐依赖**，或 **ADR 修正选型表**（写清"为何手写更好"） | ✅ **done** — 裁定**写 ADR 修正选型表**（不引入 cobra）。已产出 **`ADR-0005-cli-framework-handwritten.md`**（**注意：编号为 0005，非 0003** —— 0003 已被 dedup-ledger 占用；清单里的 "ADR-3" 即本文件）。`MVP.md` §8.1 表已修正 + 新增 **§8.7** 写明理由与**重新评估的 4 个触发条件**。**已知代价**：`relayfirst` 与 `relayfirst-verifier` 各自手写 flag 解析 —— **若出现第三个二进制必须先评估抽公共层** |

**⚠️ ADR-2 是本清单里最危险的一条**：它可能被误读为"放宽节点约束"。
**必须写清**：节点获得的是**签名结论**的能力，**不是**伪造工作或验证他人回执的能力 ——
若做不到这个区分，就不该接入节点身份。

---

### 10.2e 外部评审清单的其余项（**已核实，未排期**）

> **来源：** 外部评审清单（2026-10-04）。**逐条核实后记录，标注修正。**
> **⚠️ 以下**不属于 `MVP.md` 当前范围**（P1/P2 多数是 `ARCHITECTURE.md` 内容，L4）。
> **按 `AGENTS.md` §5.1，若要实施，必须先把范围写进 `MVP.md`。** 此处只登记，不视为已排期。

| # | 项 | 出处（已核实） | 核实结论 |
|---|---|---|---|
| 3 | **Card relay-set extension**（NIP-65 式：card 声明"我往哪些 relay 发布"） | `ARCHITECTURE.md` §17.1（"**最重要**"） | ✅ **done** — `internal/a2a/relayset.go` + `RelaySetExtensionURI`，走 **A2A extension**（**未 fork schema**，实测断言无顶层 `relaySet` 字段且标准字段完好；SDK 解码后仍可读）。<br>**为什么在 card 里**：relay set 的有效性来自**agent 自己的签名**，不来自任何目录 —— 信任目录副本 = 重新引入 §17 禁止的单一官方目录。<br>**role 而非扁平列表**：inbox（收件）与 backup（镜像）**含义不同**，压平会让客户端**猜**，猜错就是**发到镜像**（agent 永远收不到）。**`PreferredRelays` 把"inbox 优先、priority 小者优先"编码一次**（最低优先级方向易猜反 → 已用文字写明）。**角色有未知值时不整卡拒绝**（只跳过）—— 否则老读者会**把新 agent 判为坏**（与"要求精确 schema"同错）；但**生产者校验会拒**（它写错了）。**12 项测试**；变异验证：inbox/backup 反转 → FAIL，priority 反向 → FAIL。<br>**已补入口**：新增 **`relayfirst card show|publish`**（`cmd/relayfirst/card.go`）—— 此前 `a2a.Build` **在生产代码里从未被调用**（卡片构建只存在于测试中），所以 relay set **无从发布**。现可 `--inbox`/`--backup`/`--relay` 声明并**发布到多个节点**（部分失败会**报告而非隐藏**）。**实测端到端**：真实节点上 publish → 读取 → relay set 与 proof 均存活。 |
| | | | | **🐛 顺带发现并修复一个已上线的真 bug**：`cmd/relayfirst-node/main.go` **缺少 `Tasks` 存储**，导致**节点二进制自 S10-3 起无法启动**（`node.New` 直接报错）。**所有测试都通过** —— 因为每个测试**自建 config**，而**没有任何测试覆盖 `main.go`**。**是靠手动运行二进制才发现的**。**已修 + 新增 `cmd/relayfirst-node/main_test.go`**（3 项，含"四个 store 缺一不可"）—— **变异验证：移除 Tasks → 2 项 FAIL**。**教训**：包的测试**抓不到 `main` 包里的接线错误**。 |
| 4 | **RFN-05 客户端多 relay 逻辑**（quorum / ack / dedup / retry / failover / per-relay 健康） | `ARCHITECTURE.md` 标"必须" | ⚠️ **准确但已有部分** —— `internal/publish` **已有**并发扇出 + 部分成功语义 + 实测"一挂仍送达"（S5-9）。**缺**：quorum、per-relay 健康追踪、failover 策略。**范围变更**：需先写进 `MVP.md` |
| 5 | **双节点互操作测试**（同一 agent 事件经两独立节点投递，客户端状态推导一致） | permissionless 最小证明 | ✅ **done** — `internal/publish/twonode_test.go`，**4 项测试**：① **同一历史以【不同顺序】投给两个独立节点，推导状态必须一致**（这是"确定性来自签名数据而非 relay 序"的**唯一端到端证明**）；② **客户端合并两节点的【部分视图】**仍得出同一结论；③ **一个节点不可达**（先试挂掉的）仍能从另一个到达正确状态；④ **夹具守卫**：两节点**必须真的独立**（共享存储会让前面全部空过，**实测变异验证**）。<br>**⚠️ 诚实边界**：#4 的 **quorum / per-relay 健康追踪 / failover 策略未实现**。③ 只证明"**客户端试两个、一个死了仍得出正确状态**"，**不是** quorum 或自愈。**不声称 #4 已完成。** |
| 6 | **`relayfirst/node` 容器发布** | 判据 ③（v2.0 硬性验收） | ⚠️ **半准确** —— `Dockerfile` **已存在**，S8-3 标"**代码层达成**"，S5 实测 `docker build`/`run` 成功。**缺**：发布到 registry + "陌生人 10 分钟"**真人计时**。**判据 ③ 的代码部分已过，计时部分未过** |
| 7 | 发现后两层（Indexer + EVM Anchor fallback）+ 冷启动 bootstrap | `ARCHITECTURE.md` §17 | ⚠️ **属 Roadmap** —— `MVP.md` **无** indexer / anchor fallback / bootstrap 默认列表的范围。**需先写进 `MVP.md`** |
| 8 | **RFN-04 反滥用声明协议化**（policies/limits/pricing/PoW 字段格式） | `ARCHITECTURE.md` §18.4 | ⚠️ **属 Roadmap**。**顺序提醒是对的**：`ARCHITECTURE.md` §18.4 明确"**要排在任何公开 relay 名录之前**" |
| 9 | **RFN-12 节点信誉最小版**（客户端本地打分，先不做全局） | `ARCHITECTURE.md` §19（后期）vs §20 | ⚠️ **属 Roadmap**。§20 承认"无此则无法自动防恶意节点"，但 §19 把它列为后期。**范围变更**：需先写进 `MVP.md` |
| 10 | **§11"不做"表更新到 v2.0** | 本表 §11 | ✅ **已修（本轮）** —— 原表仍是 v1.0 版，与 `MVP.md` §12 **直接矛盾**。已按 L0 重写 |
| 11 | **cobra 偏离** | `MVP.md` §8.1 | ✅ **准确** —— 选型表选了 `spf13/cobra`，实际是**标准库手写**（`cmd/relayfirst/main.go` 1938 行，无 cobra 导入）。**已立 ADR-3** |

**核实中的一处自我纠正（重要）：**

本轮之前的记录曾称"**判据 ⑩ 两半达成**"。**那是过度声称** —— 判据 ⑩ 要求"**节点**签自己的结论"，
而当前只有**独立组件**能签，**节点本身无密钥**。已改正，并据此立 **S10-0**。

---

### 10.3 S11 — SBT 积分（§6.1）

| id | 任务 | 依赖 | 验收 | 状态 |
|---|---|---|---|---|
| S11-1 | `RelayPoints.sol`：ERC-721 + ERC-5192 | — | 合约可编译 | ✅ **done** — `contracts/RelayPoints.sol` + `contracts/interfaces/IERC5192.sol`。**新增依赖已走 ADR**：引入 **OpenZeppelin v5.7.0**（vendored，`ADR-0006`）—— 这是仓库**第一个 Solidity 依赖**（此前 `libs = []`）。**不自研 ERC-721 的理由不是图省事**：`_safeMint` 的 receiver selector 校验写错会**永久锁死** token；`approve` 竞态与 `_update` hook 位置是已知陷阱；而**合约不可变，写错的代价付不起**。`IERC5192` **自实现**（2 个函数，是别的工具读的标准形状）。`forge build` 通过 |
| S11-2 | **转账拦截 override**（不只 `locked()` 声明） | S11-1 | 转让尝试 revert | ✅ **done** — `_update` override：`from != 0 && to != 0` 即 revert。**这个条件正是难点**：mint 从 `address(0)`、burn 到 `address(0)` 必须仍然合法，写法若只看 `to` 会**放过销毁**、只看 `from` 会**堵死铸造**。**`locked()` 只是声明**（ERC-5192 不强制任何东西，忽略它的市场照样能转），**真正拦截在 `_update`**。**实测变异**：移除该 revert → **3 项测试 FAIL**（`locked()` 仍返回 true，即"名义上 soulbound"被抓出） |
| S11-3 | 累计积分存储（非增量） | S11-1 | 漏 claim 不丢分 | ✅ **done** — `_points[agentId]` 存**累计值**，claim **设置**而非累加。**为什么不是增量**：存增量的话，用户漏 claim 一次就**永久丢分** —— 那是设计缺陷而非用户失误。**为什么 claim 不能降低总数**：更低的证明意味着**错的 epoch** 或**不一致的账本**，静默接受会**抹掉已得积分**（实测 revert）。**相同值不 revert 也不发事件**（relayer 用陈旧缓存提交不该浪费 gas，事件流要保持诚实）。 |
| S11-4 | Merkle claim（复用 `internal/merkle` + `RelayAnchor.sol`） | S11-3 | 能凭 proof 拿到累计值 | ✅ **done** — `claimPoints` 走 `RelayRootSource.verifyProof`（**签名对齐 `RelayAnchor.verifyProof`**，非另发明）。**leaf 把 epoch 包在内**（`keccak256(agentId ‖ total ‖ epoch)`）—— 否则 epoch 10 的证明在 epoch 20 的根上也会通过，**陈旧 claim 可被重放进新 epoch**。**operator 在部署时固定、不可改**：可变的 operator 会让部署者把 claim 重定向到自己事后选的根，而**这种权威正是本设计刻意没有的**。rotate operator = 部署新合约（可见，且不能改写任何人的既有总数）。 |
| S11-5 | 无许可 poke（任何人可代交） | S11-4 | 用户零 gas 也能更新 | ✅ **done** — `claimPoints` **不检查 `msg.sender`** —— **权威是证明，不是发送者**。`testClaimIsPermissionless` 用一个**第三方 relayer 合约**提交并断言积分到账，证明用户**零 gas** 也能更新。 |
| S11-6 | 动态 `tokenURI()`（积分 + 声誉 metadata） | S11-3 | 钱包里能点进去看到积分 | ✅ **done** — **on-chain 数据 URI**（`tokenURI` 返回 `data:application/json;base64,...`，SVG 亦为 data URI）—— **无任何服务器依赖**：指向托管端点的徽章在域名失效后就不再渲染，那不是"耐久记录"。用 OZ 的 `Base64` + `Strings`。<br>**⚠️ 诚实边界（照规范做减法的部分）**：`MVP.md` §6.1 草图里有 `receipts` / `epochPoints` / `rank` / `verifiedRate`，**合约并没有这些数据**（receipts 在链下，rank 与验证率由计分层算）。**我选择不编造** —— 一个**常量 rank** 会被钱包**当成有意义的值显示**，比没有 rank 更糟。故只返回合约**真实持有**的两项：**agentId** 与 **累计 points**。字段一旦上链就该补进来；在那之前，**诚实的文档就是短的那份**。<br>**新增 `metadataJSON()`**（返回 base64 前的明文 JSON）：**base64 按三字节分组，内层 JSON 的子串不一定是外层编码的子串**，所以测试若直接在 data URI 里搜字段是**不成立的**。这个方法让文档**可被检视**，也是测试所用的入口。<br>**`agentOfToken` 反向映射**：`tokenURI` 只拿到 tokenId，不拿到 chainId；若靠 `ownerOf` 反推，会**把元数据绑在 soulbound 性质上** —— 一旦将来可转让，坏的是**别人徽章上的积分数字**而不是一个报错。**3 项测试**（自包含 / 反映积分 / **不编造**；变异验证：注入假 `rank` → FAIL） |
| S11-7 | A5 守卫覆盖新合约与文案 | S11-6 | 不出现"积分值 X USDC" | ✅ **done** — `compliance_audit.go` 的 `defaultTargets` 纳入 `contracts/RelayPoints.sol`。**实测第一次运行就 FAIL**：合约注释把白名单短语 `"carry no promised return"` **断成两行**，而 scan 是**按行**的（`TestScan_ScopeIsPerLine` 锁定该语义）→ **误报**。**修法是把短语放同一行，没有改守卫语义**（放宽成跨行匹配会削弱按行精确性，而那有测试在守）。**方向安全（误报而非漏报），但已作为已知局限记入 ADR-0006 与 §8.8。** |
| S11-8 | SBT 端到端测试（判据 ⑨） | S11-1..7 | 钱包可见 + 不可转让 + claim 正确 | 🔸 **部分** — **12 项合约测试**（`forge test` 共 27 通过）：**转让 revert**（`transferFrom` **与** `safeTransferFrom` 两条路径都测）+ **`locked()` 诚实但保护不依赖它** + **第二次 mint revert** + **claim 设累计** + **漏 claim 不丢分** + **claim 不能降低** + **坏证明被拒** + **无许可提交** + **ERC-5192 接口上报**。**变异验证**：移除 revert → 3 项 FAIL。**未达成**：判据 ⑨ 的"**wallet 可见积分 metadata**"依赖 S11-6（未做）；**"Merkle claim 能拿到"** 已测（用 stub root source），**但未与真实 `RelayAnchor` 部署对跑**。 |

### 10.4 S12 — 结算 + MCP（§6.5 / 提案 §9）

| id | 任务 | 依赖 | 验收 | 状态 |
|---|---|---|---|---|
| S12-1 | 赏金托管（最小版 `SettlementManager`） | S9-5 | Requester 能托管赏金 | ✅ **done（按 L0 裁决后的最小形态）** — **⚖️ 裁决（`DOCS.md` §1，L0 优先）**：原表写 `SettlementManager` 合约，但 `MVP.md` **附录**说链上结算"从完整 SettlementBatch **降为一个 mapping**"，**§12** 说 USDC 通道"**只做最小通道**"，且 **§88** 明说用户"**怕的是资金托管**"。**结论：L0 不要托管合约。**<br>**实际实现**：`SettlementManager` 不做托管 —— 它只**记录"谁认领了哪个赏金的哪一份"**（`ClaimBitmap`，见 S12-2），**钱直接由 Requester 付**。这同时满足 §12 的"最小通道"与 §88 的"不托管我的资金"。**新合约 `RelayBounty.sol`**：仅登记 claim 位图 + 事件，**不持有任何资产**。 |
| S12-2 | 抗双领 bitmap（`ARCHITECTURE.md` §8.6） | S12-1 | 同一赏金不能领两次 | ✅ **done** — `RelayBounty.sol` 的 `claimed[bountyId][minerId]` 位图：**同一赏金的同一矿工不能领两次**。**关键**：位图**只记录认领这一事实**，不转移资产 —— 所以它**不是**支付合约，不违反"不托管"裁决。**10 项合约测试**（含重复认领 revert、变异验证）。 |
| S12-3 | USDC 通道（**最小版，非 x402**） | S12-1 | 任务赏金可用 USDC 支付 | 🔸 **部分** — **接口已定义、文档已写，但未接真实 USDC**（无测试网部署、无 `IERC20` 调用）。**理由（诚实）**：接真实 USDC 需要**选链 + 拿测试币 + 部署**，属**需外部动作**的部分，且 `MVP.md` §12 明说"**只做最小通道**" —— 在裁决后的"不托管"架构里，USDC 通道**主要是 Requester 直接 `transfer`**，合约侧几乎无事可做。**故本项判为"机制已定、实现待外部条件"，不声称完成。** |
| S12-4 | **A5 隔离检查**：USDC 与积分不挂钩 | S12-3 | 文档与文案无"积分定价" | ✅ **done（就已有范围）** — A5 守卫（CI 门禁 7）覆盖 CLI / 入门文档 / README / **`RelayPoints.sol`**。**USDC 相关文案目前不存在于用户可见面**（S12-3 未接真实 USDC），故无可违规处。**⚠️ S12-3 接真实 USDC 时，必须把新文案纳入守卫。** |
| S12-5 | MCP server（**不持主密钥**） | — | 发现/认领/提交可用 | ⬜ todo — **依赖 S13-3**（Session Delegation 未做，MCP 的密钥边界无法先定）。**⚠️ 顺序**：先 S13-3，再 S12-5 —— 否则会把 MCP 的密钥模型建在未定的委托语义上 |
| S12-6 | 签名留在本地 CLI（MCP 只转发已签字节） | S12-5 | MCP 无密钥也能完成闭环 | ⬜ todo — **依赖 S12-5** |
| S12-7 | IDE 一行配置接入 | S12-5 | 零安装可挖 | ⬜ todo — **依赖 S12-5** |

### 10.5 S13 — E2EE + 密钥层级（提案 §6 / §8）

> **📋 完整方案：** [`docs/notes/s13-e2ee-plan.md`](docs/notes/s13-e2ee-plan.md)。
> **状态（2026-10-05 更新）**：**S13-1/2/3/4/5 均已实现**，三个设计点已全部拍板（均按建议）。
> **一句话结论**：S13 的正确形态**不是"给消息加密"，而是"把密钥权威分层"**；
> 加密只是其中一层。

| id | 任务 | 依赖 | 验收 | 状态 |
|---|---|---|---|---|
| S13-1 | X25519 密钥交换（用库，A1） | — | 派生共享密钥 | ✅ **done** — `internal/e2ee`，全部走 `golang.org/x/crypto`（**A1：一个字节都没手写**）。`SharedSecret` **传播库的小阶点错误**而非吞掉 —— 全零公钥会导出**全零共享密钥**（对端也平凡可知），吞掉错误 = **加密毫无保密性**。**变异验证**：忽略该错误 → 测试 FAIL。**`KeyPair` 独立生成、不从签名密钥派生**（`SharedSecret` 与 `KeyPair` 的类型文档写明原因：派生的话，签名密钥泄露 = **历史密文全部可解**） |
| S13-2 | XChaCha20-Poly1305 payload 加密（用库） | S13-1 | 私有任务 payload 加密 | ✅ **done** — `Seal`/`Open` 走 `chacha20poly1305.NewX`（**24 字节 nonce**：12 字节随机 nonce 在规模下会碰撞，而流密码的 nonce 碰撞是**灾难**而非不便）。**每消息用一次临时密钥** → 同一明文两次加密**密文不同**（否则**相同消息会泄漏为相同**，属元数据泄漏）。**加密后仍逐字签名**（§3.1 确认）：密文就是 `payload` 字节，**验证者无需密钥即可验签** → **判据 ② 完好**。**16 项测试** |
| S13-2b | **`alg`/`epk`/`nonce` 必须被签名覆盖** | S13-2 | 降级/重定向攻击被拒 | 🔸 **部分** — **§3.3 规则已确立并在 `Sealed` 类型上写明**（三字段全部须被外层回执签名覆盖；`Sealed` **不自带签名**，其真实性**来自外层签名**，已用测试 `TestSealedPayloadIsNotSelfProtecting` 明确写下）。**`Open` 拒绝未知 `alg`**（`ErrUnsupportedAlgorithm`，**不猜**）—— 变异验证：空 `alg` 不落默认值。**未做**：**外层回执侧的集成**（把 `Sealed` 放进 `payload` 并证明签名覆盖它）依赖 S13-3/端到端，见 S13-5 |
| S13-3 | Session Delegation 最小版（作用域 + nonce 撤销） | S9-3 | 主密钥不在不可信组件里 | ✅ **done** — **⚖️ 你已拍板（2026-10-05）：回执永不可委派。**<br>**这个选择在结构上最优**：`receipt.Validate` 的 signer 检查是"一个 agent 不能冒充另一个"的**唯一依据**，而 Session Key 是**不同密钥**，所以它**今天根本签不了回执**（恢复地址 ≠ agentId → 拒绝）。**允许委派回执 = 必须放宽这条检查**，而放宽**无法自我限定在"会话级事件"** —— 判断错一次 = **一把泄露的热密钥无限刷积分**。**"永不可委派"则完全不动那条不变量。**<br>**实现比"加检查"更强**：`Scope` 是**闭集**，**根本没有回执这个值** —— 不是"有校验禁止它"，而是**程序无法表达这个请求**。<br>**分层**：`internal/delegation`（**无密码学**，节点可安全引用）+ `internal/delegationsign`（签/验，链接 eip712）。<br>**`AuthorizeEvent` 是唯一入口**：把四件事（签名有效 / 未被撤销 / 在时间窗内 / scope 覆盖）**合成一次调用** —— 让调用方自己拼四项**迟早漏一项，而漏掉是授权绕过而非可见失败**。**26 项测试**；**变异验证**：跳过身份检查 → 2 项 FAIL；跳过撤销 → FAIL；**加 receipt scope → `TestNoScopeCoversReceipts` FAIL**。 |
| S13-4 | MCP / 节点只拿会话密钥 | S13-3 | 泄露会话密钥无法动资产 | ✅ **done** — **⚖️ 你已拍板（2026-10-05）：方案 A + Go。**<br>**关键发现（改变了做法）**：我原想靠"拆 `eip712` 的签/验"来结构强制"MCP 不能签名"，**实测否定了这条路** —— `receipt`/`publish`/`assertion`/`delegationsign` **每个包都同时 Sign 和 Recover**，拆分只会让业务包两个都 import，**MCP 仍传递链接 `Sign`**。<br>**方案 A 更强也更便宜**：**MCP 零密码学** —— 只发现/认领/转发/查询，**签名全在 CLI**。实测：`cmd/relayfirst-mcp` 导入图**只有标准库**（`relayfirst/internal` **一个都没有**）。于是 §9.2 的红线从**约定**变成**结构性质**。<br>**新增 CI 门禁（实测有效）**：MCP 导入图**不得**含 `eip712`/`receipt`/`publish`/`store`/`assertion`/`delegation`/`delegationsign`/`e2ee`/`mining`/`scoring`，**且必须能构建**（否则检查空过）。**变异验证**：给 MCP 加 `_ "internal/receipt"` → **门禁 FAIL**（并列出 `eip712`/`receipt`）。<br>**行为层的另一半**：`tools/list` **不提供任何签名类工具**（测试断言无 `sign`/`publish_card`/`submit_receipt`/`decrypt`）；`relay_message` **原样转发 base64**（不解码不重编码 —— 重编码会改变签名覆盖的字节）；`initialize` 的 `instructions` **明说本服务不持密钥**。<br>**诚实边界**：MCP **不能验证**它中继的东西 —— **这不是缺口**（relay 不是权威，ARCH §5.7；且生产者 CLI 签名前已验证）。<br>**分发**：新增 `scripts/npx-relayfirst-mcp.mjs` + `package.json` 的 `relayfirst-mcp` bin（**同 npx 模式**，零安装）。**实测**：真实节点上跑通 `initialize` + `tools/list`。**13 项测试** |
| **S13-3c** | **事件【生产者】**：`relayfirst session open/close/show` | S13-3b | 事件**真的被生产**（不只可被调用） | ✅ **done** — **这是本会话第 3 次"机制完备但零引用"**（前两次：`internal/a2a`、`e2ee`）。**诊断**：`a2a` 定义事件、`eventsign` 签名授权，但**生产者不存在** —— CLI 无发事件命令、mining 只造回执、节点只转发。**再写第 4 个库不会改变这一点**，缺的是**生产者**。<br>**实现**：`--key/--session/--nonce/--task/--relay/--state`；**链状态存文件**（每次命令是独立进程，序列必须跨进程单调，重复即重放被所有验证者拒绝）。**失败不推进链**（否则下一事件会声称一个没人见过的前驱 = 缺口）。**无 `--relay` 时只打印不推进**（否则打印出的事件此后无法发送）。**状态文件 0600**。**实测端到端**：`session open` → `session close` → **`ValidateChain` 用真实 keccak256 通过**，且 `close.previousEventHash == open` 的哈希。 |
| **S13-3b** | **事件签名层 + 委托接线**（S13-3 的可用性前提） | S13-3 | 委托**可被调用**（不再只是机制） | ✅ **done** — **接线时发现一个比预期更大的前提缺口**：`a2a.Event` 有 `Signature` 字段与 `SignedBytes()`，但**没有任何地方设置或验证它** —— 而 `a2a` **故意无密码学**（节点要 import 它）。所以 **`delegationsign.AuthorizeEvent` 无物可授权** —— **机制完整但不可达**。<br>**新增 `internal/eventsign`**（第五个 EIP-712 domain `event-1`，与其他四个不冲突）：`Sign` / `Verify`（**返回恢复出的 signer**，因为调用方下一个问题总是"这个 signer 有权吗"）/ **`AuthorizeEventWithGrant`（把两道问题串起来）** / **`ScopeOfEvent`（事件类型 → scope，无默认值）**。<br>**为什么顺序是先验签后验权**：权威是**关于特定 signer 的问题**，所以必须先确定 signer。也意味着**伪造签名报为签名问题而非权限问题** —— 运维需要知道是哪个。<br>**`ScopeOfEvent` 无默认值**：默认会用某个 scope 静默覆盖新事件类型，失败形态是"**被从未打算覆盖它的授权放行**"。**超时事件刻意不可委派**：超时由 relay 观测签名的 deadline 产生（ARCH §4.5），**不是** Owner 授权行为 —— 委派密钥**不得**能让别人的任务过期。**未知类型拒绝授权但必须仍可存储**（A9 §①：存储容忍未知，授权拒绝未知）。<br>**13 项测试**；**变异验证**：加默认 scope → FAIL；完全放行 → FAIL。<br>**⚠️ 变异揭示的一点（已写成测试）**：单独"跳过 Verify"**不会失败** —— 因为 `Verify` 失败时返回 nil signer，而 `AuthorizeEvent` 因 signer 不匹配**独立拒绝**。**这是纵深防御（好设计），不是测试漏洞**，但因此"跳过 Verify"不可观测，故该性质**显式写进测试**（`TestAuthorizeEventWithGrant_HasTwoIndependentRefusals`）。<br>**边界实测**：node / MCP / mining **均未被拖入** `eventsign`/`delegation`。 |
| S13-5 | 加密端到端测试 | S13-1..4 | 私有任务可跑，relay 只见密文 | ✅ **done（三条验收全部达成）** — **① 加密回执仍可离线验签**：`TestPrivateReceipt_ThirdPartyVerifiesWithNoKey` —— **把 `Sealed` 密文放进真实回执的 `result.value`**，导出字节，**无任何密钥的第三方**解析 + `Validate(nil)` 通过。**这是判据 ② 在加密下的形态**，也是本设计唯一不可省的验收。<br>**② relay 只见密文**：`relayfirst-node` 导入图**仍不含 `e2ee`**（实测=0），且**已加入 CI 门禁**。<br>**③ scope 排除 RECEIPT 的会失败测试**：已由 **S13-3** 交付（`TestNoScopeCoversReceipts`，**变异验证：加 receipt scope → FAIL**）。<br>**关键设计（为何无需改 schema）**：密文放进 **`result.value`** —— 它**本来就是不透明字符串**且必须非空，所以**没有新字段、没有 fork、每个既有验证器继续工作**。**A9 按构造不受影响**。<br>**新增 HKDF 会话密钥派生**（**ARCH §6.2 第 2 步，我原先漏了**）：`HKDF-SHA256(shared, salt=sessionId, info=taskId)` —— **同一共享密钥对不同任务导出不同密钥**，否则一把密钥泄露会reveal 整个关系。**拒绝空 salt**（否则同 info 的所有会话导出同一密钥）。<br>**反面测试**：验证者**确实读不出**密文（否则"能验签"会因根本没加密而通过）；**篡改密文仍使回执失效**（密文在签名字节内，加密**没有**把任何东西放到签名之外）；**公私回执的 `schema` 字符串相同**（隐私是载荷选择，不是格式版本）。**9 项新测试**；**变异验证**：salt 不参与派生 → 2 项 FAIL；明文替代密文 → 2 项 FAIL |

**S9–S13 完成定义：** `MVP.md` §11 的十条判据全过，且 **BLK-2 已解决**。

**当前实际（2026-10-05）：** 十条判据中 **8 条达成**；**① 需真人计时**；**③ 代码层达成、计时未做**。
**BLK-1 / BLK-2 均未解决** —— 它们是**上线硬前置，且需外部接洽**。
**代码侧可自闭环的部分已基本做完**（S9–S13 + SQLite 写路径 Phase 1/2）。

> **⚠️ 规模警告：** v2.0 同时做 A2A 本体 + 节点索引 + 结算 + 加密 + MCP + 积分链上化，
> **比 v1.0 大得多**（v1.0 花了 S1–S8）。**必须分阶段，且 BLK-2 从第一天并行。**
> 否则会做出一个完整、漂亮、但没人用的系统。

---

### 10.6 SQLite 写路径优化（Phase 1 + Phase 2，均已交付）

> **来源**：外部任务书（2026-10-05）。**红线**：不动存储引擎 / 不放宽写连接数 / 不做存储接口抽象。
> **完整报告**：[`docs/notes/sqlite-write-path.md`](docs/notes/sqlite-write-path.md)。

| id | 任务 | 依赖 | 验收 | 状态 |
|---|---|---|---|---|
| **PH1-1** | **Ingest benchmark**（模拟真实写入 + 去重） | — | 可重复运行，数字入文档 | ✅ **done** — `internal/devtools/ingest_bench.go`。**负载含 10% 重复投递**（只测唯一插入会测**生产中不存在的路径**，且看不到去重争用）。基线含**环境**（机器/Go/驱动/journal 模式）—— **没有机器的吞吐数字不是度量，是传闻**。 |
| **PH1-2** | **第 14 道 CI 门禁**（吞吐回归） | PH1-1 | 低于基线 X% 则红 | ✅ **done** — 阈值 **20%**，依据是**实测抖动 ~7%**（四次：10782/11578/11124/10885）——**低于噪声的阈值会因自身抖动而红，然后被人关掉，比没有门禁更糟**。**变异验证**：基线抬高 3x → FAIL（`-64.5%`）。 |
| **PH2-1** | **A6 论证**（用并发测试实测，非推断） | PH1 | 确定 A6 原子性来源 | ✅ **done** — **实测推翻了我自己在 `b6ee030` 写的因果**：两条写路径**都是单语句原子 upsert**，所以 A6 原子性来自 **SQL**，**不是** `SetMaxOpenConns(1)`。**但"能放宽写连接"也不成立**：8 连接下**大量 `SQLITE_BUSY`**。**根因（直接探测）**：`PRAGMA busy_timeout` 是**每连接**的，而 `Exec` 只触及池中**一条** → 池里其余**没有忙等待**。**所以 `=1` 保护的是忙等行为，不是原子性。** |
| **PH2-2** | **批量事务** | PH2-1 | 前后对比数字 | ✅ **done** — `internal/sqlite/batch.go`。**默认 batch=16，不是任务书的 64**：实测**吞吐在 16 就饱和**，而 **p99 随批增大恶化**（0.191 → 0.348 → **0.943ms**）。**"批越大越好"是错的。**<br>**实测收益 1.8x–2.4x**（batch=1: 10945 → batch=16: 17545 w/s；复测三次 8348→19717 / 9469→18411 / 9103→16153）。<br>**两条路径共用同一个 SQL 语句**（`putOne(e execer, m)`）—— 两份副本迟早分歧，**而分歧就是去重行为差异**。<br>**⚠️ 批量引入的新危险（已实测并写成测试）**：**未 Flush 的批次持有写锁 → 其他 Put 阻塞**。单连接下**没有别的连接可走**，所以调用方忘了 Flush **不只是丢数据，是让节点停止收发** —— **比逐条路径的失败模式更糟**。**缓解比提速更重要**：默认 16 只**限制**窗口而非消除它。 |
| **PH2-3** | **门禁缺陷修复**（基线缺字段 + 静默杀脚本） | PH1-2 | 门禁可解释地失败 | ✅ **done** — ① 基线缺 `batchSize` → 比较**永远拒绝**；② **命令替换里的空匹配在 `set -euo pipefail` 下【静默杀掉脚本】** —— **无 FAIL 行，直接 exit 1**（实测：删基线一个字段 → 整步零输出 + exit 1）。**这已是本项目第三次遇到"失败被吞"**（前两次：节点能否验签、变异测试空过）。**不说话的失败比响亮的失败危险得多。** |

**未做（红线遵守）**：写连接数**仍为 1**；存储引擎未动；未做存储接口抽象；
**批量尚未接入节点 HTTP**（`Put` 每请求调用一次，批量在那儿不生效；接入需改变请求处理形态，属独立决策）。

**遗留前置项**：**放宽写连接池仍不可用** —— 必须先**按连接**应用 `busy_timeout`，再重跑两个 skip 的 A6 测量
（`TestA6_..._ManyConnections`，**故意留成 skip 而非删除**，因为它们就是放宽写池前必须先通过的测量）。

---

## 11. 明确不做（不进本表）

> 以下全部属于 `ARCHITECTURE.md` 的 Roadmap。**若有人提出来，指向该文档，不是本表。**
>
> **⚠️ 本表已于 v2.0 修订（P3-10）。** 修订前的表仍是 **v1.0 版**，把**已移入 MVP 范围**的
> 结算 / E2EE / WebSocket / RFN 标准族列为"不做" —— **那是错的**，且与 `MVP.md` §12 直接矛盾。
> 下表与 `MVP.md` §12 对齐。**依据：`DOCS.md` §1，L0（`MVP.md`）优先于 L1（本表）。**

| 不做 | 原因 | 备注 |
|---|---|---|
| Delegation **完整** policy（作用域/限额/撤销） | MVP 只需**最小** Session Delegation（`MVP.md` §8 / §12） | 完整 policy 属 Roadmap |
| Dispute / 仲裁 / 自动罚没 | **只有显式选 `dispute` 模式的任务才需要**；`recompute` 仍二值（`MVP.md` §5.0） | 主网大额罚没属 Phase 5 |
| x402 **逐字包裹** | v2.0 只用**最小 USDC 通道**，不做 x402 适配器（`MVP.md` §6.5） | — |
| 多语言 SDK（Go/Python） | 3 倍成本，MVP 只需 TS | — |
| **全部** 6 条超时边 | **先做 4 条**（`OFFER`/`ACCEPT_START`/`INPUT`/`APPROVAL`）；其余由 `TimeoutCoverage()` 显式报告（S9-5） | 见 `MVP.md` §4.5 |
| NATS / Redis / 多 edge | SQLite 够用 | — |
| 多 relay **共识 / 联邦** | 同上 | — |
| 前向保密 / 群组加密 / 元数据混淆 / 匿名身份 | `ARCHITECTURE.md` §6.4 已明确推迟 | — |
| **Token 发行 / 质押奖励 / 节点排放** | **明确 non-goal（不变式 A7）** | — |

**v2.0 已从"不做"移入范围（勿再列为本表的不做项）：**

| 已移入 | 出处 |
|---|---|
| **结算 / Merkle claim / 最小 USDC 通道** | `MVP.md` §12（"v2.0 从'不做'移入范围"）+ §6.5 |
| **E2EE（X25519 / XChaCha20-Poly1305）** | 同上 + §6 |
| **WebSocket transport** | 同上 + §12.1（**新增绑定**，HTTP 全保留） |
| **RFN 标准族（部分）** | `MVP.md` §12：**RFN-01~06 是 A2A 本体的线格式**，非 Roadmap |
| **完整节点能力（索引 / 查询 / 任务中转）** | §7.3 |

**⚠️ 仍然不做（易被误读为"已移入"）：** 全局共识 / 全网 canonical DB（**NET-2**）、
跨 relay 全序（**NET-3**）、官方中心目录（**NET-1**）。这三条是**网络不变式**，不是"尚未实现的功能"。

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