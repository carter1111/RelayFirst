# RelayFirst — Requirements（派生需求索引）

> **本文件是派生文档，不是权威。** 它把散落在各文档中的需求**汇总成一份可追溯清单**，每条都标注来源 §。
>
> ```
> MVP.md         = L0 权威（需求的定义在此）
> TASKS.md       = L1 任务与状态
> requirements.md = 派生索引（只汇总与追溯，不改需求）
> ```
>
> **冲突裁决：** 本文件与任何文档冲突时，**以该文档为准**，并修正本文件（见 `DOCS.md` §1）。
>
> **本文件不复制正文。** 它只回答三个问题：**需求是什么、出自哪里、如何验证**。

---

## 0. 如何使用本文

| 场景 | 用法 |
|---|---|
| 想知道"这条需求从哪来" | 查「来源」列 |
| 想知道"怎么算做完" | 查「验证方式」列 |
| 想改需求 | **不要改本文** —— 改 `MVP.md`，再回填本文 |
| 想找未覆盖的需求 | 看 §10 覆盖度审计 |

**需求 ID 规则：** `REQ-<区域>-<序号>`。区域见下表。

| 区域 | 含义 |
|---|---|
| `RCPT` | 回执与签名 |
| `TASK` | 任务类型与生成 |
| `SYBIL` | 抗女巫 |
| `SCORE` | 计分与积分 |
| `INFER` | 推理额度接入 |
| `NODE` | 节点 |
| `CLI` | 命令行体验 |
| `ANCHOR` | 链上锚定 |
| `ENG` | 工程约束 |
| `ACC` | 验收判据 |
| `NG` | 非目标（明确不做） |

**状态图例：** ✅ 已实现 · 🟡 部分 · ⬜ 未开始

---

## 1. 回执与签名 — `REQ-RCPT-*`

**来源：** `MVP.md` §4 / §4.1 / §4.2 / §8

| ID | 需求 | 来源 | 验证方式 | 状态 |
|---|---|---|---|---|
| **REQ-RCPT-1** | 回执 schema 标识为 `relayfirst.receipt.v1` | §4 | 结构校验拒绝其他值 | ✅ |
| **REQ-RCPT-2** | 含 `task.type` / `spec` / `specHash` / `selfGenerated` / `a2aTaskId` | §4 | 字段存在且对齐 | ✅ |
| **REQ-RCPT-3** | 含 `work.provider/model/tokensIn/tokensOut/startedAt/finishedAt` | §4 | 同上 | ✅ |
| **REQ-RCPT-4** | 含 `result.value`（确定性结果）+ `result.hash` | §4, §4.1 | 字段存在；hash 32 字节 | ✅ |
| **REQ-RCPT-5** | **至少 1 个 anchor**，含 `url/contentHash/fetchedAt/status/bytes` | §4, §4.1 | 无 anchor → 拒收 | ✅ |
| **REQ-RCPT-6** | `verification` **不在签名覆盖内**（验证者事后填写） | §4.2 | 改 `verification` 后签名仍有效 | ✅ |
| **REQ-RCPT-7** | 签名用 **EIP-712**，且 offchain 事件的 **domain 链无关** | §4.2, §8.1 | 签名不含 chainId | ✅ |
| **REQ-RCPT-8** | 签名覆盖 `agentId ‖ epoch ‖ task ‖ work ‖ result ‖ anchors` | §4.2 | 篡改任一字段 → 验签失败 | ✅ |
| **REQ-RCPT-9** | canonical JSON 跨语言**字节确定** | §8.4 | KAT 门禁 | ✅ |
| **REQ-RCPT-10** | 未知字段必须被拒绝（防 schema 漂移） | 工程推论 | `DisallowUnknownFields` | ✅ |

---

## 2. 任务类型与生成 — `REQ-TASK-*`

**来源：** `MVP.md` §5.0 / §5.1

| ID | 需求 | 来源 | 验证方式 | 状态 |
|---|---|---|---|---|
| **REQ-TASK-1** | **只接受** `probe` / `extract` / `compute`（不变式 A2） | §5.0 | 结构校验拒绝其他类型 | ✅ S1 |
| **REQ-TASK-2** | **拒绝生成类任务**（摘要 / 分类 / 翻译）—— 无 ground truth | §5.0 | 拒收 | ✅ S1 |
| **REQ-TASK-3** | **拒绝「取任意 URL + hash」** —— 只证明"访问过" | §5.0 | 拒收 | ✅ S1 |
| **REQ-TASK-4** | `probe` = 探测 URL 可达性 / 状态码 / 响应头 | §5.0 | 独立探测可复现 | ✅ S2 — `internal/mining/probe.go` |
| **REQ-TASK-5** | `extract` = 抽取**指定字段** | §5.0 | 重取可复现 | ✅ S2 — `internal/mining/extract.go` |
| **REQ-TASK-6** | `compute` = 执行确定性计算 | §5.0 | 可复算 | ✅ S2 — `internal/mining/compute.go` |
| **REQ-TASK-7** | 任务生成器**无法产出**生成类任务 | §5.0 | 生成器 API 只接受 3 种类型 | ✅ S2 — `internal/mining/generate.go`，500 轮测试 |
| **REQ-TASK-8** | 每份有效工作量 = 签名有效 + 类型合法 + anchor 可重取 + result 可重算 + key 未抢先 | §5.1 | 见 S2/S3 | 🟡 — 前 4 项已具备（S1/S2）；**key 去重属 S3** |

> **为什么 §5.0 是全 MVP 最重要的一条：** 它让验证变成**二值**，从而**取消**了 dispute / arbitration / slashing（见 `REQ-NG-3`）。

---

## 3. 抗女巫 — `REQ-SYBIL-*`

**来源：** `MVP.md` §5.2 / §5.4 / §5.5 / §5.6

| ID | 需求 | 来源 | 验证方式 | 状态 |
|---|---|---|---|---|
| **REQ-SYBIL-1** | `artifactKey = sha256(task.type + task.spec.url + contentHash)` | §5.2 | 计算确定可复现 | ✅ S3 — `artifactkey.go` |
| **REQ-SYBIL-2** | **去重账本必须是全局的**（非 per-agent）—— 不变式 A6 | §5.2 | 跨 agent 去重生效 | ✅ S3 — `TestArtifactKey_IsAgentIndependent` |
| **REQ-SYBIL-3** | `novelty`：首次出现 = 1.0；已被他人提交 = **0.0** | §5.2 | 第二名提交得 0 分 | ✅ S3 — `TestMemLedger_FirstSightingThenRepeat` |
| **REQ-SYBIL-4** | 同一 agent 重复提交同一 key = 0.0 | §5.2 | 重复得 0 分 | ✅ S3 — 同上 |
| **REQ-SYBIL-5** | `contentHash` 更新 → 新 key → **正常得分**（不误伤） | §5.2 | 内容变化后得分恢复 | ✅ S3 — `TestArtifactKey_DistinguishesInputs` |
| **REQ-SYBIL-6** | 验证者**随机指派**，不可自选 | §5.5 | 指派不可预测 | ✅ S4 — `verification.Policy` 接口；`Verify` **重新检查**指派（策略是选择器不是权威），并在 `Assign` 与 `Verify` **双重**排除生产者。**BLK-3 仍未关闭**：生产策略未选定，参考实现为确定性种子 |
| **REQ-SYBIL-7** | 验证者**质押积分**下注（非代币，`stake: 50`） | §5.5 | 质押记账正确 | ✅ S4 — **实现为独立承诺账本**（`verification.StakeLedger`），**不是扣分**。A5 强制：`PointsLedger` 方法集被锁定为 6 个且无 `Debit`，所以扣分表达方式**不可用**。`StakeLedger` 方法集同样被守卫（无 transfer/debit/balance 类动词） |
| **REQ-SYBIL-8** | 判定为**二值**（一致 / 不一致），非程度判定 | §5.5 | 无部分分 | ✅ S4 — `Outcome.Verified` 为 bool；比较 `result.Hash` **与** `result.Value` 两者，任一不符即拒。重执行失败**不算通过**（否则把源下线即可刷过） |
| **REQ-SYBIL-9** | 验证者与被验证者 `agentId` **必须不同** | §5.6 | 违反即拒绝 | ✅ S4 — `ErrSelfVerification`；`RecordedVerdicts` 也会拒绝"验证者 == 生产者"的回执（**这是仅凭回执就能查的那条规则**） |
| **REQ-SYBIL-10** | 同一 agent 不能同 epoch 内既大量产出又大量验证（比例上限） | §5.6 | 超比例即拒绝 | ✅ S4 — `RatioPolicy`（默认 0.5）；**产出为 0 的纯验证者直接拒绝**。边界语义已写明：准入判据是 `verified <= produced × ratio`，坐在边界上的 agent 可再验一次 |
| **REQ-SYBIL-11** | **验证奖励 < 产出奖励** → 纯验证不划算 | §5.6 | 参数断言 | ✅ S4 — A5 下积分只能由产出获得，**不存在"验证即得积分"的路径**；比例上限使验证至多占产出的一半 |
| **REQ-SYBIL-12** | 验证窗口 = 该 epoch 结束前（默认 24h） | §5.4 | 窗口外不再重取 | ✅ S4 — `verification.EpochWindow`（默认 24h）；窗口保护的是**生产者**（内容会变，过期重取会误伤诚实工作），不是安全门。CLI 默认启用，`--no-window` 显式关闭并自报 |
| **REQ-SYBIL-13** | 判错方扣质押 | §5.5 | 判错扣款生效 | ⚠️ **语义已澄清，不是扣款** — 实现为承诺记录的**状态变化**（`released` / `slashed`），**没有任何积分被扣除**。A5 禁止积分在 agent 间移动，且 `PointsLedger` 无 `Debit`。已实测：注入 `Debit` 会让两条守卫同时失败；schema 守卫另查**无 balance 列**且**无触碰 `point_entries` 的 trigger** |
| **REQ-SYBIL-14** | 比例上限必须**真的被计量**（否则规则形同虚设） | S4-6 | 计数可达策略 | ✅ S4 — `store.Activity` 用 SQL COUNT 提供真实每 epoch 计数（**验证数含 rejected**，否则无限拒绝免费）；**无 `Activity` 时 fail closed（拒绝）**，不静默放行 |
| **REQ-SYBIL-15** | 验证者必须**重取 anchor**，不只重算 result | §5.5 | 篡改 anchor 被拒 | ✅ S4 — `mining.AnchorConsistency`；逐条 anchor 比对 contentHash；**源不可达不算通过**（否则下线源即可绕过）；无外部 anchor 时**明确报"无可检"**而非静默成功 |
| **REQ-SYBIL-16** | 四项伪造攻击对**真实栈**全部 0 分 | §11④ | 余额不增 | ✅ S8-4 — `internal/redteam`；含**控制测试**证明诚实工作仍得分 |

> **诚实声明（必须保留）：** 大户养足够多 sybil **仍可自验**。成本线性上升，收益被全局去重压住。**这是可接受的权衡，不是无懈可击。**

---

## 4. 计分与积分 — `REQ-SCORE-*`

**来源：** `MVP.md` §5.3 / §6.1 / §6.2 / §6.3

| ID | 需求 | 来源 | 验证方式 | 状态 |
|---|---|---|---|---|
| **REQ-SCORE-1** | `points = BASE × verified × novelty × diversity × budgetFactor` | §5.3 | 与公式逐项一致 | ✅ S3 — `score.go` |
| **REQ-SCORE-2** | `BASE = 10` | §5.3 | 常量断言 | ✅ S3 — `BasePoints` |
| **REQ-SCORE-3** | `verified` 为 **0/1 二值**，不一致直接作废（不给部分分） | §5.3 | 边界测试 | ✅ S3 — `TestScore_VerifiedIsBinary` |
| **REQ-SCORE-4** | `diversity = 1 / (1 + sameDomainRepeats × 0.5)` —— 同 domain 递减 | §5.3 | 同 domain 重复 → 衰减 | ✅ S3 — `TestScore_DiversityAttenuates` |
| **REQ-SCORE-5** | `budgetFactor` 使 per-agent 上限 = `B(n) × 5%` | §5.3, §6.3 | 单 agent 无法独占 | ✅ S3 — **分层**：`Allocate` + `CapAllocation` |
| **REQ-SCORE-6** | `B(n) = B0 × decay^n`，`B0 = 1,000,000`，`decay = 0.99` | §6.2 | 常量 + 衰减断言 | ✅ S3 — `TestEpochBudget_Decays`。**⚠ S3b 修复**：`n` 必须相对 **epoch 起点**计算（`internal/epoch`）；无起点时 `n ≈ 20729` → `B ≈ 3.3e-85`，发行量恒为零。见 [`docs/notes/epoch-anchoring.md`](notes/epoch-anchoring.md) |
| **REQ-SCORE-7** | `epoch = 1 天` | §6.2 | 常量断言 | ✅ S3 — `EpochLength`。**⚠ S3b**：`EpochOf` / `receipt.NewEpoch` 均已锚定到共享起点，且 `delta < 0` 时钳到 epoch 0（防 `uint64` 下溢）；起点值待定稿（BLK-4） |
| **REQ-SCORE-8** | 积分**不可转让、不定价、可交易性为零、不承诺回报**（不变式 A5） | §6.1 | **无转账接口** | ✅ S3 — 反射断言方法集（`TestNoTransferCapability`） |
| **REQ-SCORE-9** | 积分性质必须写进 UI 与文档 | §6.1 | 文案审查 | ✅ S8-9 — **已自动化**：`internal/compliance` A5 文本守卫（风险词表 + 批准免责语白名单 + 逐行 points 作用域）+ `internal/devtools/compliance_audit.go`（人类可读审计，违规 exit 1）+ **CI 第 10 道门禁**。实测扫描用户可见文件命中 5 条短语、**全部落在批准免责语内**；喂入违规文本 → exit 1。**边界**：绊线非证明（词表外措辞会漏；伪装成免责语的违规会漏）；**非法律意见**，R7 仍需人工复核 |
| **REQ-SCORE-10** | 每 epoch 预算按份额分配：`points_i = B(n) × (work_i / Σwork)` | §6.2 | 分配正确 | ✅ S3 — `TestAllocate_ProportionalAndCapped` |
| **REQ-SCORE-11** | **挖矿必须真的产生积分**（生产路径上"实现"≠"接线"） | §11① | 端到端可观测 | ✅ S3b — `mining.ScoringSink`；CLI 两个入口都经它；`stats` 报 `totalPoints` / `yourBalance`。实测 5 条回执 → 5 条记账 |
| **REQ-SCORE-12** | **校验不通过的回执既不计分，也不占用 artifact** | §5.2 | 无效提交不得抢走 novelty | ✅ S3b — `TestScoringSink_InvalidReceiptEarnsNothing`（含真实 SQLite 路径） |
| **REQ-SCORE-13** | `diversity` 的"同 domain"判定必须基于**真实 domain** | §5.3 | 跨 domain 不被误罚 | ✅ S3b — `sameDomainRepeats` 从已落盘回执推导；正反两条测试锁定（初版方向相反） |
| **REQ-SCORE-14** | `verified` 口径**由验证者结论驱动**，不再信任提交者 | §5.5 | 有明确替换点 | ✅ S4 — `mining.VerdictSource` 可注入；生产用 `verification.RecordedVerdicts`。默认仍是 `SelfCheckVerdicts`（兼容 S4 前行为，且**显式命名**其局限）|
| **REQ-SCORE-15** | **`Verified` 的来源必须在调用点可见**（不能靠 nil 默认值隐身） | S4-0 | 类型名自述其局限 | ✅ S4 — `SelfCheckVerdicts.Describe()` 明说 "NOT adversarial verification"；`RecordedVerdicts.Describe()` 说明读自回执 |

---

## 5. 推理额度接入 — `REQ-INFER-*`

**来源：** `MVP.md` §9.3 / `TASKS.md` S2-6

| ID | 需求 | 来源 | 验证方式 | 状态 |
|---|---|---|---|---|
| **REQ-INFER-1** | 支持 **BYO API key**：`openai` / `anthropic` / `local` | §9.3, S2-6 | 三种 provider 均可跑通 | ✅ S3b — `internal/llm/` 三种 provider + **CLI 已接线**（`mine --provider/--model`）。密钥**只走环境变量**，刻意不做 flag（避免进 shell history / 进程表）|
| **REQ-INFER-2** | **先做 BYO API key**（保证能跑），**订阅路径并行核实** | §9.3 | 策略符合 | 🟡 S2 — BYO 路径已实现；订阅仍待核实（BLK-1） |
| **REQ-INFER-3** | 驱动订阅是**未知项**（BLK-1）—— 不得假设其可行 | §9.3 | 不依赖它 | ✅ — 实现完全不依赖订阅 |
| **REQ-INFER-4** | `work` 字段如实记录 provider / model / tokens | §4 | 回执字段真实 | ✅ S3b — 语义 extract 现在记录**真实 token 与 model**（`mining.Usage` + `UsageSource`，Loop 按迭代取用并重置）；probe/compute/位置型 extract 仍为 0（它们**确实**不消耗推理）；语义任务不再自报 `provider: "none"` |
| **REQ-INFER-5** | API key 不得落入日志 / 错误信息 / 回执 | `CODING_RULES.md` §8 | 泄漏测试 | ✅ S2 — `TestProviderErrorDoesNotLeakKey` + `TestRedactKey_NeverRevealsValue` |

> **BLK-1 是最高优先级风险：** 若订阅无法程序化驱动，叙事退化为"再买一份 API 额度来挖矿"，吸引力大幅下降。

---

## 6. 节点 — `REQ-NODE-*`

**来源：** `MVP.md` §7.1 / §7.2 / §7.3 / `TASKS.md` S5

| ID | 需求 | 来源 | 验证方式 | 状态 |
|---|---|---|---|---|
| **REQ-NODE-1** | 收件端点用 **HTTP**，**不是 WebSocket** | §7.1 | 无 WS 依赖 | ✅ S5 — `POST /messages`，标准库 `net/http`，无框架、无 WS |
| **REQ-NODE-2** | 按 `agentId` 存储（SQLite 单文件） | §7.1 | 拉取正确 | ✅ S5 — `messages` 表按 `agent_id` 建索引；`TestMessageStore_ByAgentIsolates` |
| **REQ-NODE-3** | 客户端**拉取**（GET）+ 可选 SSE 推送 | §7.1 | GET 可用 | ✅ S5 — `GET /messages/{agentId}`（含 `?limit=`）；**SSE 未做**，§7.1 标为可选 |
| **REQ-NODE-4** | `eventId` / `receiptId` 去重 | §7.1 | 重复投递只存一次 | ✅ S5 — 主键 + `ON CONFLICT DO NOTHING`；重复仍返回 **200**（重试不得被当成失败） |
| **REQ-NODE-5** | 返回收件 acknowledgement | §7.1 | 有 ACK | ✅ S5 — `{ok,id,stored}` |
| **REQ-NODE-6** | 提供 `/.well-known/relayfirst` 信息文档 | §7.1 | JSON 可获取 | ✅ S5 — 含 `protocol` / `endpoints` / 计数 / `verifies:false` |
| **REQ-NODE-7** | 节点是**哑的**：**不验证、不裁决** | §7.1 | 节点代码无验签逻辑 | ✅ S5 — `internal/node` **不 import `eip712`**；文档中显式声明 `verifies:false` |
| **REQ-NODE-8** | 节点内**不得出现任何链读** | §7.1 | 依赖图无 ethclient | ✅ S5 — 无任何链客户端依赖；`go.mod` 无新增 |
| **REQ-NODE-9** | 一条 `docker run` 起节点 | §7.2 | 陌生用户 10 分钟跑起 | ✅ S5 — 实测构建并运行；镜像 36.5MB，非 root，含 `HEALTHCHECK` |
| **REQ-NODE-10** | 多 relay publish（向 ≥2 个节点投递） | S5-9 | 单点故障不影响送达 | ✅ S5/S7 — `internal/publish`；**一处故障仍送达**（实测）；部分成功即视为成功；**扇出并发**：最坏等待 = 1×timeout 而非 N×timeout（实测 5×300ms → 0.31s），报告顺序仍按配置 |
| **REQ-NODE-11** | 节点不做 NATS / Redis / Postgres / 多 edge | §7.1 | 依赖审查 | ✅ S5 — 仅标准库 + 已有纯 Go SQLite；镜像只构建 `cmd/relayfirst-node` |
| **REQ-NODE-12** | payload **字节原样保存**（节点不得改写回执） | §4.2 | 往返后签名仍有效 | ✅ S5 — 节点视 payload 为不透明字节；`TestNode_ReceiptRoundTripSurvivesSignature`（**往返后仍能验签**） |
| **REQ-NODE-13** | **anchor proof 必须能对已公布 root 离线校验**（不依赖任何 store/服务器） | §4.3, §11② | 无 `--db` 也能验 | ✅ S7 — `anchor check --root <hash>`；`--root` **刻意不从 proof 读取**（否则循环）；已实测无 store 通过、错 root 被拒 |

---

## 7. 命令行体验 — `REQ-CLI-*`

**来源：** `MVP.md` §9.1 / §9.2 / §11①

| ID | 需求 | 来源 | 验证方式 | 状态 |
|---|---|---|---|---|
| **REQ-CLI-1** | 入口为 `npx relayfirst@latest`（零安装） | §9.1 | 无需预装 | 🟡 S6 — `scripts/npx-relayfirst.mjs` + `package.json` `bin` **已就位**；**未发布 npm**（外部动作）。启动器委托 Go 二进制，不重写协议 |
| **REQ-CLI-2** | `init`：生成 / 导入 EVM 钱包 → 输出 `agentId` | §9.1 | 30 秒内完成 | ⛔ S6 **被阻断** — 私钥落盘属凭据处理（`POL-SECRETS-1`）。用户自备 key 经环境变量注入 |
| **REQ-CLI-3** | `config set --provider/--api-key` | §9.1 | 三种 provider 可配 | 🟡 S6 — `config set/get/show` 已实现（含 provider/model）；**`--api-key` 刻意不做**，密钥只走环境变量，且 `Set` **拒绝密钥形状的值** |
| **REQ-CLI-4** | `mine`：守护进程启动，**立刻出分** | §9.1 | 启动即产出 | ✅ S6 — 无 flag 可运行；实测即出分 |
| **REQ-CLI-5** | `status`：积分可查 | §9.1 | 输出正确 | ✅ S6 — 含本 epoch / 终身积分、回执、去重 artifact、有效 anchor、epoch 结束时间 |
| **REQ-CLI-6** | `receipts --export`：**能带走全部回执** | §9.1 | 导出可离线验证 | ✅ S6 — 写规范签名字节；**实测导出后逐个离线验签通过，篡改被拒** |
| **REQ-CLI-7** | **实时反馈**：本 epoch 积分 / 任务数 / 有效 anchor | §9.2 | 农民立刻看到分数涨 | ✅ S6 — `liveProgress`；数字从账本读回，不做本地并行计数 |
| **REQ-CLI-8** | **陌生用户 ≤10 分钟出分**（无人工协助） | §9.1, §11① | 计时测试 | ⬜ S6-8 — **需真人计时**；机器实测整条路径 < 3 秒，但判据要求"陌生人无协助" |
| **REQ-CLI-9** | `verify`：**离线**验签（不连任何服务器） | §11② | 断网可用 | ✅ S1 |
| **REQ-CLI-10** | **配置文件不得含任何密钥** | `CODING_RULES.md` §8 | 行为 + 结构双重断言 | ✅ S6 — `Config` 无密钥字段；`Set` 拒绝 `sk-`/`-----BEGIN`/`PRIVATE KEY`；测试锁定字段**结构**防止未来新增 |

---

## 8. 链上锚定 — `REQ-ANCHOR-*`

**来源：** `MVP.md` §4.3 / `TASKS.md` S7

> ⚠️ **本区域是 MVP 中唯一涉及 Solidity 的部分，且排在最末（S7）。**
> 代码层已完成；**链上部署与提交需真人**（无节点、无资金账户、无部署权限）。

| ID | 需求 | 来源 | 验证方式 | 状态 |
|---|---|---|---|---|
| **REQ-ANCHOR-1** | 回执集 → Merkle tree（按 epoch） | S7-1 | root 确定可复算 | ✅ S7 — `internal/merkle`；**RFC 6962 域分离**（`0x00` 叶 / `0x01` 节点）改用 **keccak256**（EVM 原生，复用已有 `golang.org/x/crypto/sha3`）+ **补齐 2 的幂**（形状两语言唯一）；叶子按 receipt id 排序，不依赖插入顺序 |
| **REQ-ANCHOR-2** | 链上仅需一个 `mapping(uint256 epoch => bytes32 root)`（~50 行） | §4.3, S7-2 | 部署成功 | 🟡 S7 — **`forge build`/`forge test` 通过，未部署**。**两处刻意偏差**：① §4.3 的 `uint256 => bytes32` **无法编译**（mapping 需键+值两个类型），改为 `mapping(uint256 epoch => bytes32 receiptsRoot)`；② 额外按 **operator 命名空间**隔离，否则任何人可覆盖任何人的 root。实际 **197 行**（含注释与自定义 error） |
| **REQ-ANCHOR-3** | 每日提交一个 root，成本可忽略 | S7-3 | gas 可接受 | ⬜ **未做** — 提交是签交易，属人工动作；步骤见 S7 报告 |
| **REQ-ANCHOR-4** | 任何人可用 Merkle proof 证明"我的回执在 epoch N" | §4.3, S7-4 | proof 验证通过 | ✅ S7 — Go `Prove`/`Verify` + CLI `anchor proof`/`anchor verify`（**自己重算 root**，不信任 proof 携带值）+ 链上 `RelayAnchor.verifyProof`；Go/Solidity/viem **三方得出同一 root** |
| **REQ-ANCHOR-5** | **不做完整结算**（无 settlement / escrow / claim） | S7 注 | 无相关合约 | ✅ S7 — 合约**无** `settlement`/`escrow`/`claim`/`withdraw`/`dispute`，且**不给提交者任何奖励**（不变量 A5） |
| **REQ-ANCHOR-6** | **跨语言 Merkle 语料 + CI 门禁**（不变量 A4） | S7-1 | Go 与 Solidity 对同一语料一致 | ✅ S7 — 一次计算同时产出 `testdata/merkle-vectors.json` 与 `contracts/test/MerkleVectors.sol`；CI **重新生成并 diff**（实测：篡改语料 → FAIL，还原 → PASS） |
| **REQ-ANCHOR-7** | epoch root **不可覆盖**（写一次） | S7-2 | 二次提交 revert | ✅ S7 — `EpochAlreadyAnchored`；锚定要防的就是事后修改 |
| **REQ-ANCHOR-8** | 合约**无 owner / admin / 特权方** | §7.3 | 无 onlyOwner | ✅ S7 — 无 `Ownable`、无 admin、无注册表；权限仅来自"写自己命名空间" |

---

## 9. 工程约束 — `REQ-ENG-*`

**来源：** `MVP.md` §8 / §12 · `AGENTS.md` §3 · `CODING_RULES.md`

| ID | 需求 | 来源 | 验证方式 | 状态 |
|---|---|---|---|---|
| **REQ-ENG-1** | **密码学原语绝不手写**（keccak256 / secp256k1 / ecrecover / X25519 全用库）—— A1 | §8.0 | 依赖审查 | ✅ |
| **REQ-ENG-2** | **`CGO_ENABLED=0` 必须能构建** —— A3 | §8.3 | CI 门禁 | ✅ |
| **REQ-ENG-3** | **跨语言 KAT 向量必须存在且在 CI 中门禁** —— A4 | §8.4 | CI 门禁 | ✅ |
| **REQ-ENG-4** | SQLite 用 `modernc.org/sqlite`（纯 Go），**不用** `mattn/go-sqlite3` | §8.3 | 依赖审查 | ⬜ S2 |
| **REQ-ENG-5** | ecrecover / keccak 用成熟库；EIP-712 可自研薄层（二选一） | §8.1, §8.2 | 见 ADR-0001 | ✅ |
| **REQ-ENG-6** | **并发正确性必须有 CI 门禁**（`-race`） | `CODING_RULES.md` §9 | `go test -race` 通过 | ✅ S4-11 — **第 9 道门禁**。**不改变 A3**：race 需要 `CGO_ENABLED=1`，但那只用于**测试**；出货二进制仍是 `CGO_ENABLED=0`，构建门禁照旧。工具链不支持时 **skip 而非 fail**（环境事实，不是仓库缺陷） |
| **REQ-ENG-7** | 用户可见的计数**不得随历史增长**而变慢 | — | 单次查询 | ✅ S4 — `CountAnchorsByAgent` 用一条 SQL 取代"每迭代读全部回执"；测试断言它与被替换的循环**结果完全一致**，且**排除 inline 合成 anchor**（虚高比没有更糟） |
| **REQ-ENG-9** | 门禁必须**能被真实故障触发**（非空转） | `AGENTS.md` §5.2 | 破坏性实测 | ✅ 已实测五条：注入 `Debit` → A5 守卫 FAIL；篡改 Merkle root → 新鲜度门禁 FAIL；篡改 proof sibling → viem 门禁 FAIL；移除 `Credit` 的锁 → race 门禁 FAIL；**喂入价值声明 → A5 合规门禁 FAIL（exit 1）** |
| **REQ-ENG-10** | **只做 TS SDK**；不写 Go / Python SDK | §12 | 无多余 SDK | ✅ |
| **REQ-ENG-11** | `a2a-go` 只允许出现在**身份与序列化**边界，**不得**进入挖矿核心 | §8.5 | 依赖审查 | ⬜ |
| **REQ-ENG-12** | **不做 token 挖矿 / 质押奖励 / 节点排放** —— A7 | §12 | 无相关逻辑 | ✅ |
| **REQ-ENG-13** | **不修改 AgentLand 或其他 sibling 项目** —— A8 | `AGENTS.md` | 改动审查 | ✅ |
| **REQ-ENG-14** | 每个 `internal` 包必须有测试 | `CODING_RULES.md` §6.3 | `go test ./...` | ✅ |
| **REQ-ENG-15** | 格式化 / vet / build / test 四项全过才可提交 | `CODING_RULES.md` §9 | `scripts/ci.sh` | ✅ 10 道门禁 |

---

## 10. 验收判据 — `REQ-ACC-*`

**来源：** `MVP.md` §11（**七条全过，任何一条不过不上线**）

| ID | 判据 | 一句话 | 验证方式 | 状态 |
|---|---|---|---|---|
| **REQ-ACC-1** | ① | 陌生人 **10 分钟内**产出第一份积分，全程无人工协助 | 计时测试（S6-8） | ⬜ |
| **REQ-ACC-2** | ② | **关掉服务器**后回执仍可被第三方**离线**验证 | `relayfirst verify` 断网 | 🟡 S1 部分 |
| **REQ-ACC-3** | ③ | 任意陌生人 **一条 docker 命令**跑起自己的节点 | 陌生用户实测 | ⬜ |
| **REQ-ACC-4** | ④ | 四项伪造攻击**全部 0 分**：a 重复 key / b 编造 hash / c 5 个 agent 同 URL / d 生成类任务 | 红队测试 | 🟡 部分（b/d 已拦） |
| **REQ-ACC-5** | ⑤ | **对抗验证闭环**跑通，且判错时质押被正确扣除 | 端到端测试 | ⬜ |
| **REQ-ACC-6** | ⑥ | **TS/Go KAT 字节相同**，不一致即构建失败 | CI 门禁 | ✅ |
| **REQ-ACC-7** | ⑦ | **`CGO_ENABLED=0` 构建通过**，单二进制静态可分发 | CI 门禁 | ✅ |

**② 和 ④ 是"这个 play 是不是真的"的判据。** ①③⑤ 是体验与机制，⑥⑦ 是工程底线。

**另有一条不是判据但同等硬：** **至少 1 个真实消费方**（`REQ-LAUNCH-1`，见下）。

| ID | 前置条件 | 来源 | 状态 |
|---|---|---|---|
| **REQ-LAUNCH-1** | 上线前必须具备**至少 1 个真实付费 / 真实消费方**（存在性证明，非规模） | §10.3 | ⬜ |

---

## 11. 非目标 — `REQ-NG-*`

**来源：** `MVP.md` §12 · `AGENTS.md` §4

**以下全部推迟到 Roadmap（`ARCHITECTURE.md`）。任何人提出时，指向该文档。**

| ID | 不做 | 原因 |
|---|---|---|
| **REQ-NG-1** | Delegation 完整 policy（作用域 / 限额 / 撤销） | 对"农民来玩"零贡献 |
| **REQ-NG-2** | 结算 / Escrow / Merkle claim | 让你变成又一个支付服务，稀释叙事 |
| **REQ-NG-3** | **Dispute / 仲裁 / Slashing** | **§5.0 的 ground truth 约束使其不必要** |
| **REQ-NG-4** | x402 / USDC 集成 | 同上 |
| **REQ-NG-5** | E2EE（X25519 / XChaCha20） | 农民不在乎隐私 |
| **REQ-NG-6** | 多语言 SDK（Go / Python） | 3 倍成本，MVP 只需 TS |
| **REQ-NG-7** | 完整状态机（6 条超时边） | 过度工程 |
| **REQ-NG-8** | NATS / Redis / 多 edge | SQLite 够用 |
| **REQ-NG-9** | RFN-01…RFN-12 标准族 | 全部 Roadmap |
| **REQ-NG-10** | 多 relay 共识 / 联邦 | 同上 |
| **REQ-NG-11** | WebSocket transport | HTTP + SSE 够用 |
| **REQ-NG-12** | **Token 发行 / 质押奖励 / 节点排放** | **明确 non-goal** |
| **REQ-NG-13** | 按 `ARCHITECTURE.md` 全量实现 | 它是 L4 路线图，不约束 MVP |
| **REQ-NG-14** | 承诺空投 / 回报 / 收益 | 摧毁"不发币"退路，且法律风险 |
| **REQ-NG-15** | 把 sybil 防御描述为无懈可击 | 被拆穿一次，叙事就没了 |

---

## 12. 需求 → 阶段 → 验收映射

**一眼看出"哪个阶段交付哪条需求"。**

| 阶段 | 交付需求 | 对应判据 |
|---|---|---|
| **S1** ✅ | `RCPT-1..10`、`TASK-1..3`、`CLI-9`、`ENG-1..3,5,6,8..11` | ⑥⑦ |
| **S2** | `TASK-4..8`、`INFER-1..5`、`ENG-4` | ①（部分） |
| **S3** | `SYBIL-1..5`、`SCORE-1..10` | ④ |
| **S4** | `SYBIL-6..13` | ⑤ |
| **S5** | `NODE-1..11` | ③ |
| **S6** | `CLI-1..8`、`SCORE-9` | ① |
| **S7** | `ANCHOR-1..5` | （可选） |
| **S8** | 全部判据复核 + `LAUNCH-1` | ①–⑦ |

---

## 13. 覆盖度审计（本文是否漏了什么）

**方法：** 逐份文档过一遍，确认每条可执行需求都已登记。

| 来源文档 | 主要章节 | 已覆盖 | 备注 |
|---|---|---|---|
| `MVP.md` | §1–§3 定位与取舍 | 间接（§0 引用） | 取舍原则不是需求，是裁决规则 |
| | §4 回执 | ✅ `RCPT-*` | |
| | §5 抗女巫 | ✅ `TASK-*`、`SYBIL-*`、`SCORE-*` | |
| | §6 积分 | ✅ `SCORE-*` | |
| | §7 节点 | ✅ `NODE-*` | |
| | §8 选型 | ✅ `ENG-*` | |
| | §9 CLI | ✅ `CLI-*`、`INFER-*` | |
| | §10 叙事与分发 | 🟡 `LAUNCH-1` | 叙事本身不是需求 |
| | §11 验收 | ✅ `ACC-*` | |
| | §12 不做 | ✅ `NG-*` | |
| | §13 风险 | 见 `TASKS.md` §12 | 风险不是需求 |
| | §14 工期 | 见 `TASKS.md` | 工期不是需求 |
| | §15 待决 | 见 `TASKS.md` §1 阻塞项 | |
| | §16 A2A | ✅ `ENG-7` | |
| `TASKS.md` | S1–S8 | ✅ §12 映射 | |
| `AGENTS.md` | §3 不变式 A1–A8 | ✅ `ENG-*`、`NG-*` | |
| | §4 禁止操作 | ✅ `NG-*` | |
| `CODING_RULES.md` | §1–§9 | ✅ `ENG-*` | |

**已知未登记项（有意）：**

- 叙事 / 分发 / 社区运营 —— **不是工程需求**，属运营（`MVP.md` §10）
- 工期估算 —— 属计划（`TASKS.md` §11 里程碑）
- 风险与缓解 —— 属风险管理（`MVP.md` §13）

---

## 14. 需求变更流程

**本文是派生文档，禁止直接改。**

```text
要改需求
  ↓
① 改 MVP.md（L0 权威）        ← 必须先
  ↓
② 同步 TASKS.md（L1）
  ↓
③ 回填本文（更新「来源」与「状态」）
  ↓
④ 本文与 MVP.md 冲突时 → 以 MVP.md 为准，并修正本文
```

**⚠️ 反模式：只改本文不改 `MVP.md`。** 那会让派生索引变成竞争性真相源，正是 `DOCS.md` §6 要避免的。

---

## 附：与 `DOCS.md` 的对应

| 项 | 位置 |
|---|---|
| 本文件层级 | **派生索引**（低于 L0，见 `DOCS.md` §1） |
| 权威在何处 | `MVP.md`（L0） |
| 何时更新 | 当 `MVP.md` 需求变化，或阶段状态推进时 |
| 登记位置 | `DOCS.md` §2 文档清单 |
