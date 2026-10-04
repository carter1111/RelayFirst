# S3b 验收报告 — 挖矿→计分闭环 + epoch 锚点修复

> 按 `DOCS.md` §4.5 的阶段报告模板。**证据优先于结论。偏差如实记录（`AGENTS.md` §5.4）。**
>
> 日期：2026-10-03
> 阶段：S3b（闭环接线 + 一处上线阻塞缺陷的发现与修复）
> 起点：`next` —— 用户要求继续推进

---

## 为什么有 S3b 这个编号

S3 报告（[`S3-report.md`](S3-report.md)）已经把去重、计分、发行、积分账本都写完了，
183 条测试当时（102 条）也全过。但**"能挖矿"在产品意义上是不成立的**：

> `mine` 产出回执、落盘、签名，但**从头到尾没有任何代码调用计分**。
> 积分经济一步都没动过。

这不是"少个功能"，是**里程碑 M2 挂着绿灯但没有闭环**。把这条接上之后，
才暴露出下面那处更严重的缺陷。所以把这次收尾单独编号，而不是塞进 S3。

---

## 交付

| 文件 | 行数 | 作用 |
|---|---:|---|
| `internal/mining/scoring_sink.go` | ~185 | **`ScoringSink`**：落盘 + 计分 + 记账；`ReceiptLister` 接口 |
| `internal/mining/usage.go` | ~110 | `Usage` / `UsageSource` / `UsageReset` / `UsageRecorder`（推理用量，字段名对齐回执 `work`） |
| `internal/epoch/epoch.go` | ~95 | **epoch 起点**（`GenesisValue`）+ `Of` / `Bounds`（叶子包，破 `receipt`↔`scoring` 环） |
| `internal/epoch/epoch_test.go` | ~150 | 起点锚定 / **下溢钳位** / 纯函数性 / `Bounds` 半开区间往返 |
| `internal/mining/generate.go` | +35 | `Generator.SemanticFields`：让 extract 任务真实消耗推理额度 |
| `internal/mining/scoring_sink_test.go` | ~420 | 计分/重放/无效/红队/持久化顺序/diversity 正反 |
| `internal/llm/usage_wiring_test.go` | ~360 | **推理用量写入回执**（含 RED→GREEN 记录）：真实 token / 累计 / 重置 / 免费任务仍为 0 |
| `internal/mining/generate_semantic_test.go` | ~150 | 语义字段只作用于 extract、默认零成本、blank 回退、provider 记录 |
| `internal/store/mining_integration_test.go` | ~250 | **打在真实 SQLite 路径上**的闭环与红队 |
| `cmd/relayfirst/main.go` | ~130 改 | `mine` 接 `ScoringSink`；`--provider/--model/--semantic`；`stats` 报积分；**修复 `--semantic` 被静默丢弃** |
| `internal/scoring/emission.go` | — | `EpochOf` / `EpochBounds` 改为锚定 |
| `internal/receipt/receipt.go` | — | `NewEpoch` 改为锚定 |
| `internal/devtools/epochprobe.go` | ~50 | 诊断脚本（`//go:build ignore`） |

**外部依赖：无新增。网络测试：零。**

---

## 一、接上闭环（S3-10 … S3-13）

`ScoringSink` 的顺序是**先落盘，再计分**。这个顺序是刻意的：

- 丢分可以事后补算（回执还在）；
- 白付钱不可逆（回执没存住却已经记账）。

计分失败**不向上抛错**，因为回执已经存好了；把它变成一次迭代失败会让
`Runner` 进入退避，把一次临时的账本抖动放大成整轮停摆。

**校验不通过的回执既不计分，也不占用 artifact。** 后者容易被忽略但很重要：
无效提交若能占据 artifact 键，就会**抢走诚实矿工之后做同一份工作时的 novelty** ——
攻击者不需要伪造工作，只需要抢先提交垃圾，就能让别人白干。

`stats` 也补了 `totalPoints` / `creditedReceipts` / `yourBalance`，
因为一个只报"回执数"的 `stats` 无法区分"挖了没记账"和"一切正常"。

### 一处返工：diversity 的重复计数方向是反的

初版 `domainRepeats` 把"该 agent 本 epoch 的所有积分"都算作同 domain 重复。
后果不是不精确，而是**方向反了**：跨 domain 广泛工作的 agent 被罚得比
死磕单个 domain 的 agent 更重 —— 正好和 diversity 的目的相反。

改为从**已落盘回执**推导 domain（`Receipts.ByAgent` 读回，`store.ReceiptStore` 已满足）。
并补了正反两条测试锁定：同 host 第三条必须低于第一条；不同 host 必须几乎不衰减。

> 不同 host 那组断言不是 `== BasePoints`，而是 `>= BasePoints × 0.99`。
> 因为 `BudgetFactor` 本身会让第 2、3 条略低（实测 9.9969 / 9.9939）。
> 用 99% 下界可以把"额度衰减"和"diversity 惩罚"分开 —— 后者会砍到约 50%。

---

## 二、发现的阻塞缺陷：epoch 没有起点

闭环接上后跑真实 CLI，产出 3 条回执，`stats` 只报 **1 条记账**：

```json
{ "receipts": 3, "creditedReceipts": 1, "totalPoints": 10 }
```

根因：`EpochOf` 是

```go
uint64(t.Unix() / int64(EpochLength.Seconds()))
```

**没有起点。** 当前 epoch ≈ **20,729**，于是

```
B(20729)   = 3.326203e-85
cap(20729) = 1.663102e-86
```

`BudgetFactor = 1 - alreadyEarned/cap`：第一条 `alreadyEarned = 0` 走早退分支返回 **1**
（所以满分 10 分），此后 `alreadyEarned = 10 ≫ cap` → 恒为 **0**。

**每个 agent 每 epoch 恰好只能记一次分。** 不是"衰减很快"，是**发行量实际为零**。

完整分析见 [`docs/notes/epoch-anchoring.md`](../notes/epoch-anchoring.md)。

### 修复

新增叶子包 `internal/epoch` 承载共享起点常量（`scoring` 依赖 `receipt`，
公式放任一侧都成环）。`receipt.NewEpoch` 与 `scoring.EpochOf` 都改为调用它。

两处细节：

1. **`delta < 0` 必须返回 0。** 否则负数转 `uint64` 会回绕成天文数字，
   再把预算压到零 —— **和原 bug 症状一模一样**，只是换了条路。
2. **起点必须是常量，不能是配置。** epoch 号在**签名载荷**里，任何验证者都要
   在**不信任服务器**的前提下推出同一个值。可配置会让两个诚实节点算出不同 epoch
   而互相拒签（安全失败，但起点一旦公布就不可改）。

### 修复后

```
epoch      = 2
B(epoch)   = 9.801000e+05
cap(epoch) = 4.900500e+04
```

同样 5 条回执：

```json
{ "receipts": 5, "creditedReceipts": 5, "totalPoints": 46.648984 }
```

（不足 `5 × 10` 来自 `BudgetFactor` 额度衰减与 `diversity`，都是设计意图。）

### 上线前必须做完的事

`GenesisValue` 目前是**占位符**（2026-10-01），选在近期过去，好让开发期跑正常量级预算。
**上线前必须改成真实发布日**（已登记为 `TASKS.md` **BLK-4**）。
风险不对称：**偏晚安全**（从 epoch 0 拿到完整 `B0`），偏早则会削弱头矿叙事。
又因 epoch 在签名载荷内，**改动会让旧起点下的所有回执失效**，必须在开放真实挖矿**之前**定稿。

回归保护：`TestEpochOf_IsAnchoredAtGenesis` 断言当前 epoch 与 cap 处于可用范围，锚点丢失即失败。

---

## 三、诚实边界（必须随文档一起传播）

**1. `Verified` 目前是"自签自校验"，不是验证结论。**

`ScoringSink.paramsFor` 把 `Verified` 设为 `r.Validate(nil) == nil`，即
**本进程刚刚自己签、自己验**。真实的对抗验证（S4）尚不存在。

这样做的理由是：若按"未验证"处理，矿工一分都拿不到 —— **是更糟的错误**。
S4 落地后必须改由验证者结论驱动，已登记为 **S4-0（前置）**。

**2. 积分不可转让、未定价、不承诺回报。**

`PointsLedger` 没有任何转账/扣减方法（`TestNoTransferCapability` 反射断言）。
持久化已经具备（CLI 用的是 `store.NewScoringLedgers`），
但**至今没有任何东西读取它来发放奖励**。积分目前只是本地记账。

**3. 语义任务只是"经济上提高成本"，不是"协议上强制"。**

手写 per-site 抓取规则仍可绕过模型。抗刷量的承重墙仍是全局去重账本（不变量 A6）。

---

## 四、实测数字

### 测试

```
go test ./... -count=1 -v
  --- PASS 计数：200（全仓；S3 时为 102）
  --- FAIL 计数：0
  --- SKIP 计数：0
go test ./internal/scoring/ -count=1 -v
  --- PASS 计数：43
```

### 构建门禁（`./scripts/ci.sh`）

```
CGO_ENABLED=0 go build ./...   → PASS
go vet ./...                    → PASS
gofmt -l .                      → PASS（空）
go test ./...                   → PASS
KAT corpus                      → PASS (53 vectors)
```

### 端到端

```
mine --once ×5（5 个不同 URL，真实 SQLite）
  → receipts: 5, creditedReceipts: 5, totalPoints: 46.648984   （修复前：1 条）

go run internal/devtools/epochprobe.go
  epoch = 2, B = 9.801000e+05, cap = 4.900500e+04                （修复前：epoch 20729, B 3.3e-85）
```

### 红队

| 测试 | 位置 | 断言 |
|---|---|---|
| `TestScoringSink_FiveAgentsOneArtifactYieldsOneCredit` | mining（内存） | 5 个不同密钥、不同 receiptId → **恰好 1 个记账**，总计不翻倍 |
| `TestMiningLoopFiveAgentsOneArtifactDurably` | store（**真实 SQLite**） | 同上，主打持久化路径 |
| `TestScoringSink_InvalidReceiptEarnsNothing` | mining | 篡改回执 0 分且**不占 artifact** |
| `TestScoringSink_ReplayDoesNotDoubleCredit` | mining | 重放 5 次仍 1 条 |
| `TestMiningLoopReplayIsNotDoubleCredited` | store | 同上，`ON CONFLICT DO NOTHING` 路径 |

刻意用**不同密钥 + 不同 receiptId**，让**积分账本的幂等性无法成为**拒绝它们的理由 ——
这样测的才是去重账本本身。若复用同一个 receiptId，测试会"通过"却什么都没证明。

---

## 五、第二处 bug：`--semantic` 被静默丢弃

`--semantic` 接上后，真实回执里却是：

```json
"fields": ["status", "id"]        // 位置型默认值，不是语义字段
```

`flags` 解析器把每个 `--x y` 收进 `values` map，**只对 `source` 走 repeated 分支**。
`--semantic` 因此被后一个同名 flag 覆盖，**静默丢失**：CLI 接受参数、不报错、
产出的却是零推理成本的位置型 extract。

修法：显式维护 `repeatableFlags` 集合。不能用启发式判断，因为
`--semantic a --semantic b` 与 `--model x` 在语法上无法区分。

修复后：

```json
"fields": [{"semantic": "the page title"}]
"work":   {"provider": "local", ...}
```

**顺带发现的不一致：** 语义 extract 明明调用了 provider，`work.provider` 却是
`"none"`（`providerOf` 只看 `op`/`provider`，而语义 spec 两者都没有）——
即"唯一为消耗推理而存在的任务类型，却记录自己没消耗推理"。
已让生成器在语义模式下把 provider 写进 spec。

> **仍是缺口：** 现在记录的是**用了哪个 provider、花了多少 token、哪个 model**，
> 但 token 数依赖 provider 的自报。`--provider local` 用的是查表 stub，
> **真的不消耗推理**，所以它记 0 是诚实的，不是没接线。
> 真实 provider（openai/anthropic）返回的用量会被 `FieldResolver` 累计并写入回执。

**教训：** 这两个 bug 都是"跑一遍真实 CLI 看落库内容"才发现的，
单测和构建门禁全绿。CLI 参数解析与回执字段填充是纯函数的测试盲区。

---

## 六、把推理用量写进 `work` 块（REQ-INFER-4）

原状：语义 extract **调用了** provider，但 `work` 记 `tokensIn: 0`、`tokensOut: 0`、
`model: ""` —— 即**唯一为消耗推理而存在的任务类型，声称自己没消耗任何推理**。
MVP 的经济学押注在这个成本上，所以这不是显示问题。

按用户选择的**更安全路径**做：先写会失败的测试，确认失败原因正确，再实现。

### RED（先跑，确认红得对）

```
--- FAIL: TestSemanticExtractRecordsRealTokenUsage
    work.tokensIn = 0, want 1234 (the provider's reported input tokens)
    work.tokensOut = 0, want 567 (the provider's reported output tokens)
    work.model is empty, but a real model served this task
```

### 实现

- `mining.Usage` —— 字段名**刻意与回执 `work` 块一致**，避免在必须无歧义的接缝上
  多一层命名映射。`Add` 累计，因为一个提取任务可能有多个语义字段 = 多次调用。
- `mining.UsageSource` / `UsageReset` —— 单方法接口。**没有**加宽 `Resolver`：
  只有真正调用模型的实现才有用量，测试里的离线 stub 没有，
  加宽接口会强迫每个实现者满足一个它并不具备的关注点。
- `Loop.workFor` **按类型断言发现** `UsageSource`，所以**任何调用点都不用改**。
- `Loop.MineOnce` 每次尝试前 `ResetUsage`：否则失败尝试的开销会被记到最终成功那条回执上。

### 关键判断：CLI 的 local provider 记 0 是**正确**的

端到端实测留下的是：

```
compute|local|0|0      extract|local|0|0      probe|none|0|0
```

`--provider local` 是查表 stub，**真的不消耗推理**。
`usage()` 诚实返回它配置的 token 数（默认 0）。
**去编造一组"看起来像真的"数字会让回执对自己的成本撒谎**，
而那正是 `work` 块存在的意义所在。非零路径由测试用配置了用量的 provider 覆盖。

### GREEN

```
TestSemanticExtractRecordsRealTokenUsage        PASS   真实 token 写入回执
TestSemanticExtractAccumulatesMultipleFieldCalls PASS  3 字段 = 3 次调用，累计而非覆盖
TestUsageModelFallsBackToProvider               PASS  真实 provider 不返回 model 时回退
TestUsageRecorderResetPerIteration              PASS  第二条回执不继承第一条的开销
TestProbeAndComputeRecordNoTokens               PASS  免费任务保持为 0（反向门禁）
TestSemanticExtractWithoutResolverRecordsNoTokens PASS 无 provider 时既不发 token 也不卡死
```

另：**移除了一个 `t.Skip`**。`TestSemanticExtractWithoutResolverRecordsNoTokens` 初版在
"没有 extract 回执"时直接 skip —— 而该场景下 extract **必然**产生不了回执，
于是它永远 skip、**永远证明不了任何事**。改为断言真实行为：
无 provider 时 extract 应当失败，但矿工必须继续跑（不能因为一个任务类型缺配置就停摆），
且任何回执都不得凭空报 token。

### 顺带补上 `internal/epoch` 的测试

该包此前**零测试**，而它承载共识关键常量。补了 9 条：起点锚定、
**pre-genesis 钳位防 `uint64` 下溢**、非正长度、纯函数性、
`Bounds` 半开区间与跨大量 epoch 的往返一致性。

---

## 偏差与教训

1. **S3 的"完成"是账本级完成，不是产品级完成。** 计分函数全对、测试全过，
   但没有任何生产代码调用它。教训：**"实现"和"接线"要分开验收**，
   接线做完之前不该给里程碑开绿灯。

2. **两处 bug 都是走路走出来的，不是审出来的。** epoch 无起点、diversity 方向反，
   都是跑真实 CLI / 写测试时才暴露。单测围着纯函数转时，
   这类"接线处的量级错误"是盲区。

3. **一个"看起来在跑"的系统可以完全没在经济上动。** `mine` 打印 `ok probe`、
   `stats` 报 3 条回执 —— 一切正常，只是发行量为零。
   所以 `stats` 现在会显式报积分，`mine` 会打 `→ +N points` / `→ no credit: 原因`。
   静默的零收益和正常的零收益必须能分辨。

---

## 后续

| 下一步 | 依赖 | 说明 |
|---|---|---|
| **BLK-4：定稿 `GenesisValue`** | — | 开放真实挖矿前必须完成 |
| **S4-0**：`Verified` 改由验证者驱动 | S4-3 | 当前口径是临时的 |
| S4-1（BLK-3）：验证者指派策略 | — | 阻塞 S4 |
| 真实 provider 联网实测 | — | CLI 已接线；`FieldResolver` 已累计用量；**无密钥，未做端到端联网验证**（需真人填环境变量） |
