# S4 验收报告 — 对抗验证机制

> 按 `DOCS.md` §4.5 的阶段报告模板。**证据优先于结论。偏差如实记录（`AGENTS.md` §5.4）。**
>
> 日期：2026-10-04
> 阶段：S4（重执行验证 + 指派策略接口 + 防串谋 + 承诺记录 + S4-0 接线）
> **状态：机制完成。BLK-3（生产指派策略）仍开放，属刻意。**

---

## 一句话结论

「自己出题自己答」已被修掉：新增的验证者会**真的重跑任务**，判定二值，
且计分不再信任提交者。

**但两件事必须同时说清楚：**

1. **BLK-3 未关闭。** 生产指派策略仍未选定，参考实现（确定性种子）**可被磨 agentId**。
2. **「扣质押」不是扣分。** A5 禁止积分在 agent 间移动，`PointsLedger` 方法集被锁定为 6 个且无
   `Debit`，所以"slash"只能表达为**承诺记录的状态变化**。

---

## 交付

| 文件 | 行数 | 作用 |
|---|---:|---|
| `internal/verification/verification.go` | ~250 | `Verifier`：指派复核 → 资格检查 → 重执行 → 二值判定 → 记录 → 结算 |
| `internal/verification/policy.go` | ~230 | `Policy` 接口；`RatioPolicy`；参考实现 `EligiblePolicy`；`AllowAll`（测试用） |
| `internal/verification/stake.go` | ~230 | `StakeLedger`（**承诺账本，非积分**）；`MemStakeLedger` |
| `internal/verification/verdicts.go` | ~200 | `RecordedVerdicts`（**生产口径**，读回执）；`MemVerdicts`；`SelfCheckAdapter` |
| `internal/verification/verification_test.go` | ~600 | S4 验收 21 条（先写、先红） |
| `internal/verification/verdicts_test.go` | ~210 | 口径源 9 条 |
| `internal/verification/e2e_test.go` | ~330 | **真实 executor** 端到端 5 条 |
| `internal/mining/verdict.go` | ~85 | `VerdictSource` 接口 + `SelfCheckVerdicts`（**显式命名其局限**） |
| `internal/mining/scoring_sink.go` | ±40 | `Verified` 改为注入（**S4-0**） |
| `internal/mining/verdict_test.go` | ~150 | S4-0 接线 4 条 |

**外部依赖：无新增。**

---

## 对应验收判据

| 判据 | 状态 | 证据 |
|---|---|---|
| **⑤ 对抗验证闭环** | ✅ **达成** | 端到端（真实 executor）：诚实通过、篡改被拒、自验被拒、超比例被拒、承诺 released/slashed |

**S4 完成定义核对：**「验证闭环端到端可跑，且判错扣质押生效」
→ ✅ 达成，**但"扣质押"语义已按 A5 澄清为承诺记录状态变化**。

---

## 一、先写测试，先看它红

按用户选择的**安全路径**：先写验收测试，确认失败原因正确，再实现。

```
$ go test ./internal/verification/
github.com/relayfirst/relayfirst/internal/verification: no non-test Go files
FAIL
```

这是预期的红：包还不存在。但更重要的是**测试本身就是规格** ——
21 条里每一条对应 `TASKS.md` §5 的一行或红队清单的一项。

实现后**两条测试失败**，两条都暴露了真实问题：

### 失败 1：比例上限的边界算错了

```
produced=10 verified=5 should be allowed, got ... exceeds the 0.5 cap
```

我写的是 `verified+1 > produced × ratio`，把"**这次**验证"也算进了余量，
于是坐在边界上的 agent 被无故拒绝。改为 `verified > produced × ratio`，
并把边界语义**写进注释**：坐在边界上可再验一次，之后才锁。

选择记录而非隐藏：`MVP.md` §5.6 称其为"比例上限"，是**软性威慑界**，
不是安全边界。要做硬边界需要共享计数器 + 原子 check-then-increment，
那是另一套设计。

### 失败 2：可重放性没想清楚

```
Verify 1: verification: settle: commitment ... already released
```

同一回执验证三次，第二次起结算失败。这暴露了我**没有明确回答**"重复验证意味着什么"。

答案：**承诺只结算一次**（第二次判定不能改变第一次记录的后果，
否则追责就无从谈起）；但**判定照常重算并写回**，所以重复验证仍可作一致性检查。

于是 `Verify` 改为：承诺已存在（`wrote == false`）→ 返回判定，**跳过结算**。

---

## 二、最关键的约束：A5 让"质押"不能是扣分

读代码时发现的事实，它决定了整个设计：

```go
// TestPointsLedger_MethodSetIsClosedToMovement
want := map[string]bool{
    "Credit": true, "Balance": true, "EpochBalance": true,
    "Entry": true, "Entries": true, "AgentCount": true,
}
```

**`PointsLedger` 的方法集被精确锁定为 6 个，没有 `Debit`。**

所以任务描述里的"扣质押"**无法**实现为扣分 —— 加一个 `Debit` 会让这条守卫失败。

这是好事，不是障碍。设计因此变为：

| 层 | 承载什么 | 能否移动积分 |
|---|---|---|
| `scoring.PointsLedger` | 积分（**只能由产出获得**） | ❌ 结构性禁止 |
| `verification.StakeLedger` | **承诺记录**（magnitude + 状态） | ❌ 无 transfer/debit/balance 类动词 |

`StakeLedger` 的方法集**同样被守卫**：`Commit` / `Release` / `Slash` / `Get` / `Standing` / `Entries`
（外加一个只读的 `StakeLedgerMethods()` 供守卫使用）。
`slashed` 是一个**被放弃的额度合计**，不是一个被减掉的余额。

`TestCommitmentNeverMovesPoints` 跑完整生命周期（commit → slash），
然后断言**每个 agent 的余额逐位未变、条目数未增**。

### 这个守卫是**非空转**的（实测）

```
向 PointsLedger 注入 Debit →
  FAIL TestNoTransferCapability: PointsLedger.Debit looks like a value-moving method
  FAIL TestPointsLedger_MethodSetIsClosedToMovement: unexpected method Debit
还原 → ok
```

---

## 三、端到端：用**真实 executor**，不是 stub

stub 测试能覆盖比较逻辑与防串谋规则，但覆盖不了**"重跑真实任务是否真能复现真实结果"**。
如果它不能，生产环境里每次验证都会拒绝诚实工作，而所有 stub 测试依然全绿。

所以 `e2e_test.go` 把 `Recomputer` 接到 **`mining.Run`**：

```
TestEndToEnd_HonestWorkVerifiesAgainstTheRealExecutor          PASS
  生产者真的跑一次 extract → 验证者用同一 executor 重跑 → 一致

TestEndToEnd_TamperedResultIsRejectedAgainstTheRealExecutor    PASS
  篡改 result 的回执。关键：r.Validate(nil) 仍通过
  —— 它签名有效、结构合法，正是自检抓不到的那种。

TestEndToEnd_VerifiedReceiptEarnsRejectedOneDoesNot            PASS
  三条结构完全相同的回执，只有验证状态不同 → 仅 verified 计入

TestEndToEnd_CommitmentRecordedThroughTheRealVerifier          PASS
TestEndToEnd_CommitmentIsBoundToATimestamp                     PASS
```

第二条的 `r.Validate(nil) == nil` 断言是**故意的**：它证明了这个缺口
**只能靠重执行来补**，而不是靠更严的结构校验。

---

## 四、S4-0：把 `Verified` 从"自签自校验"改成注入

原状（`S3b` 报告里记为诚实边界）：

```go
verified := r.ValidateStructure() == nil && r.Validate(nil) == nil
```

即**问提交者：你自己觉得你的活干得好吗？**

现在：

```go
type VerdictSource interface { Verified(r *receipt.Receipt) bool }
```

- 生产：`verification.RecordedVerdicts` —— 读回执的 verification 块，
  并要求 `status == verified` **且** `verifierId` 存在 **且** `recomputedHash` 存在
  **且** 验证者 ≠ 生产者。只认显式 `verified`，pending/rejected/乱码一律 false。
- 默认（未注入）：`SelfCheckVerdicts`，**同一个类名就写着它的局限**，
  `Describe()` 返回 `"self-check (structure and signature only; NOT adversarial verification)"`。

> **⚠️ 行为变化，必须知道：** 接上真实源后，**刚挖出的回执是 `pending`，
> 因此在被验证前不得分**。这是正确终态（验证才是让分数有意义的东西），
> 但意味着**独自运行的矿工会做工作却拿不到分** —— 这是个容易让人困惑的调试状态，
> 所以 `Describe()` 存在，CLI 应报告当前用的是哪个源。

**默认保留 `SelfCheckVerdicts`（而非默认"未验证"）是刻意的兼容选择**，
不是主张自检等于验证。

---

## 五、防串谋：策略是**选择器**，不是权威

`Verify` **重新检查** `Policy` 的输出：

```go
if verifier == r.AgentID { return ..., ErrSelfVerification }
```

理由：策略是**最可能被替换的组件**，若机制信任它，一个 buggy 或恶意的 `Assigner`
就能打开自验口子。所以生产者排除做在**两处**（`Assign` 与 `Verify`）。

比例上限同理由策略实现，但**产出为 0 的纯验证者直接拒绝** ——
它没有可损失的东西，不能让它当裁判。

---

## 六、诚实边界（必须随文档传播）

1. **大户养足够多 sybil 仍可自验。** 成本随 sybil 数量**线性上升**，收益被 A6 全局去重压住。
   **这是可接受的权衡，不是无懈可击**（`MVP.md` §5.6）。
2. **参考策略 `EligiblePolicy` 可被磨 agentId。** 它用确定性种子（好处：任何人可复核谁该验证），
   攻击者可以反复生成 agentId 直到自己被选中。**BLK-3 仍未关闭。**
3. **"slash" 不扣任何积分。** 它是承诺记录的状态变化。不要在任何文案里暗示"扣分/罚没"。
4. **无验证奖励。** A5 下积分只能由产出获得，所以"验证者得奖励"（§5.5 原文）**没有实现**，
   以实现为准：验证的价值在于**缺席会阻止生产者得分**，而不是验证者自己得分。

---

## 七、偏差与遗留

> **本节已更新：原列出的多项未完成项在本轮补齐，见文末「本轮补齐」。**

1. **BLK-3 未关闭。** 机制就位，生产策略待定。
2. **任务描述写的"验证者质押积分下注"未实现为下注。** A5 使"下注"不可行；
   实现为**先记录承诺、事后再结算**，与任务描述的"先质押"顺序相反。
   这更符合"无需信任"：验证者不需要预付任何东西，也能被追责。
3. **§5.5 的"验证者得奖励"未实现**（原因见上）。

---

## 七之二、本轮补齐（S4 收尾）

上表的 4–7 项在本轮完成。逐条记录：

| 原遗留 | 现状 |
|---|---|
| 4. 比例上限计数未接真实值 | ✅ `store.Activity` 用 SQL COUNT 提供每 epoch 产出/验证数。**验证数含 rejected**，否则无限拒绝免费。**无 Activity 时 fail closed** |
| 5. 验证窗口未实现 | ✅ `verification.EpochWindow`。**窗口保护生产者**：内容会变，过期重取会误伤诚实工作。CLI 默认启用 |
| 6. 验证未接入 CLI | ✅ `relayfirst verify-receipt`；输出含 verdict / 重算 hash / 窗口 / **anchor 是否已检** / 比例上限是否放宽 |
| 7. 无持久化 | ✅ `verification_commitments` + `verification_verdicts` 两表；schema 守卫查**无 balance 列**、**无触碰 `point_entries` 的 trigger** |

### 本轮发现的两个真实缺陷

**其一：验证者没有重取 anchor。** 这是读代码时发现的，不是测试发现的。

`Verification` 只比对 `result.Hash`。但 `Anchor.ContentHash` **也在签名载荷里**
（`MVP.md` §4.2），而 `MVP.md` §5.5 明确要求"重取 anchor"。`probe` 回执记录的是
**状态码 + 内容 hash**；若源之后返回了不同字节，只重跑任务会**报一致**，
于是接受了它从未检查过的签名声明。

已补 `mining.AnchorConsistency`：逐条 anchor 比对 contentHash；
**源不可达不算通过**（否则下线源即可绕过）；**无外部 anchor 时明确报"无可检"**
而非静默成功 —— "没有错误"不能被读成"证据成立"。

实测（CLI，篡改库中 anchor hash）：

```
"verified": false,
"reason": "the anchor evidence does not match the source: anchor consistency:
           anchors[0] records content sha256:ffffffffffff... but
           https://example.com now returns sha256:25ddf2c883e0...;
           the receipt's evidence no longer describes its source"
```

**其二：我自己的一个测试想法是错的，及时发现了。**

曾打算按 `result.Value` 判断期望 verdict，写成 `coverageValues()` 帮助函数。
读到 `extract` 执行器才发现 `Value` 是 **JSON body**，不是状态码 ——
`"200"` 只是 `probe` 的值。若照原想法写，测试会断言一个**永不发生的拒绝**，
看起来在测防护，实际什么都没测。

改为：**诚实路径真实执行任务并记录真实 anchor**，这样它与验证者的重取一致，
`verified` 才是真的。这个教训记在这里：**测试里的"期望值"如果来自猜测，
测试就在验证猜测。**

### 本轮新增测试

```
internal/redteam        7 条（四项攻击 + 控制 + 签名层守卫）
internal/verification   8 条窗口 + 9 条 durable + 6 条 Activity 计数
internal/mining         7 条 anchor 一致性
internal/sqlite        12 条承诺/结论持久化 + schema 守卫
internal/store          7 条 epoch 查询 + Activity
cmd/relayfirst          5 条改为真实 store 驱动
```

### 仍遗留

- **BLK-3 生产指派策略**（用户的决定）。
- **`verify-receipt` 放宽了比例上限**，且**在输出里自报**。理由：比例是协议级
  sybil 防御，单人手动核一次不是它防的威胁；强制会使该命令对未挖矿的验证者不可用。
  规模化验证应改用比例策略。
- **验证窗口用 epoch 长度常量而非导入 `scoring`**，避免无谓耦合；两者若漂移，
  表现为窗口在错误时间关闭，有测试钉住默认值。
- **`setup` 命令未实现** —— 它涉及生成/落盘密钥，属 `POL-SECRETS-1` 边界。
  见下。

---

## 七之三、第二轮补齐

### 1. 并发门禁（`REQ-ENG-6`，第 9 道门禁）

项目里有真正并发的代码：带互斥锁的账本、挖矿 runner 循环、relay HTTP server、
以及会结算承诺的验证者。原先 `go test` **完全没有在 race 检测器下跑过**。

新增 `go test -race ./...`。**它不改变 A3**：race 需要 `CGO_ENABLED=1`，
但那只是**测试期**插桩，出货二进制仍是 `CGO_ENABLED=0`，构建门禁照旧。
工具链不支持时 skip 而非 fail（环境事实）。

### 2. 一个关于"如何验证门禁"的教训

**第一次破坏性验证失败了，但失败的是我的方法。**

我把 `MemPointsLedger.Balance` 的锁去掉，期望 race 门禁报错。**它 PASS 了。**

原因不是门禁失灵，而是**那个改动不构成竞争**：`Balance` 只在所有写者 join 之后被调用，
没有重叠访问。**没有重叠访问就没有竞争，race 检测器当然不会报。**

换成移除 `Credit` 里的锁（那才是并发写者真正竞争的地方），立刻：

```
FAIL go test -race ./... found a problem:
    github.com/relayfirst/relayfirst/internal/scoring.(*MemPointsLedger).Credit()
        points.go:146
    github.com/.../TestPointsLedger_ConcurrentCreditIsSafe.func1()
        points_test.go:239
```

**教训：验证一道门禁，必须制造它本该抓到的那类故障。**
否则你只是在确认它会打印 PASS —— 那是最没价值的测试。

**并且要知道 race 检测器的固有局限：它只报真实发生的竞争。**
所以这道门禁的强度**取决于测试里的并发**。本轮已把这个覆盖补上，见下节。

---

## 七之四、第三轮：补上并发覆盖，并修掉一个真实的竞争

### 起因：上一轮我自己写下的局限

上一轮我写明：race 门禁的强度**受测试并发度限制**，而当时只有 `scoring` 与 `store` 有并发测试。
本轮就是去关掉这个缺口 —— 因为**一道只在顺序代码上跑过的 race 门禁，等于在确认顺序代码是顺序的**。

覆盖前后对比（有并发测试的包）：

```
之前：scoring, store                                     (2 个)
之后：mining, node, scoring, store, verification          (5 个)
```

### 找到了一个**真实的 data race**（并已修复）

`Verifier.Verify` 会**原地改写调用方传入的回执**：

```go
r.Verification = receipt.Verification{ ... }
```

两个 goroutine 同时验证**同一枚回执对象**时，会并发写同一个结构体字段。
**这是真正的竞争**，不是理论问题 —— 补上并发测试后 race 检测器立刻报出：

```
WARNING: DATA RACE
  verification.go:349  ← r.Verification = ...
  concurrency_test.go:167 / :175
```

**修法：只把"记录"这一步串行化，不动重执行。**

```go
// 重执行（网络 + 跑任务）在锁外，保持并行
v.recordMu.Lock()
defer v.recordMu.Unlock()
r.Verification = receipt.Verification{ ... }
```

**为什么锁的范围要这么窄：** 重执行是昂贵的那部分（一次网络取数 + 一次任务执行），
把它放进锁里会毁掉让验证可行的并行度；而记录只是一次结构体赋值加一次账本写入，
串行化它没有任何可测量的代价。

**为什么用单把锁而不是每回执一把：** 临界区是微秒级，
而"每回执一个互斥锁"要引入一个 map 及其生命周期管理，去保护一个在那个粒度上根本不争用的东西。

**修复后复测非空转：** 把锁去掉 → race 立刻复现（`FAIL: TestVerifier_ConcurrentSameReceiptIsIdempotent`）；
装回 → PASS。

### 新增的并发测试

| 包 | 测什么 |
|---|---|
| `node` (4) | 32 个并发**重复投递** → 只存 1 条且全部返回 200；8 agent × 12 消息并发读写 → **邮箱不串**；读与写重叠；只读端点与写并发 |
| `verification` (3) | 16 路并发验证不同回执；**16 路并发验证同一回执** → 承诺恰好 1 条、只结算一次；混合 agree/reject → 两条结算分支都走到 |
| `mining` (3) | 挖矿进行中并发读 `Stats`；失败路径同样并发读；**断言 runner 不会并发调用 sink** |

### 我自己测试里的一个 bug（同一个"看起来对"的陷阱）

`TestVerifier_ConcurrentMixedOutcomes` 第一次失败，报"没有承诺被 slash"。
原因：我用 `strings.HasSuffix(url, "agree")` 判断分支，而 **`"/disagree"` 也以 `"agree"` 结尾** ——
于是两条分支都走了"同意"路径，**分歧路径从未被测试**，而测试却报出一个看起来像实现问题的失败。

改为按路径段 `Contains(url, "/agree")` 匹配。**教训与上一轮同源：**
一个"看起来在测某件事"的断言，如果匹配条件写错，它测的是别的东西。

### 仍未覆盖的并发面

- **`publish` 的扇出**：目前是顺序投递（`for` 循环，非 goroutine），所以没有竞争面；
  若将来改为并行投递，需要补并发测试。
- **`store` / `sqlite` 的跨连接并发**：`SetMaxOpenConns(1)` 把 SQLite 序列化在一连接上，
  所以并发测试走的是同一连接排队，**没有覆盖多连接场景**（那是设计选择，不是缺口）。
- **`verifier` 的同一回执并发**测的是**最坏情况**（多个 goroutine 共享一个指针），
  不是推荐用法。真实调用方各自持有自己的回执对象，那样不会碰到这个竞争 ——
  但**库不该因为调用方"用法不对"就崩**，所以锁保留。

### 3. 用户可见计数不再随历史增长

挖矿进度行每迭代显示 anchor 数。原先的做法是**读全部回执**再在 Go 里计数 ——
成本随 agent 的整个历史增长，而这个循环每几秒跑一次，于是长跑矿工**会莫名越来越慢**。

改为一条 SQL（`CountAnchorsByAgent`，用 `json_each` 展开 anchors 数组后 `COUNT(DISTINCT)`）。
测试断言它与被替换的循环**结果完全一致**，并**排除 inline 合成 anchor**
（虚高比没有更糟）。

### 4. `verify-receipt --all`：epoch 批量验证

单人核对一枚适合抽查，但审计一个 epoch 想要全量。手工逐枚的失败模式是可预期的：
**查了几枚没问题，就假定其余也没问题。**

汇总**区分 rejected 与 skipped**：窗口外、或验证者即生产者，都属"未被判定"。
把"没验"和"验失败"合并成一个数字，会让**什么都验不了的 epoch 看起来像全过**。

### 本轮新增测试

```
internal/store    6 条 anchor 计数（含与旧循环一致性、排除合成 anchor、按 agent 隔离）
```

### 未做，且是刻意的

**`setup` 命令未实现。** 它需要**生成并落盘密钥**，属 `POL-SECRETS-1` 硬边界。
这不是"没做完"，是**不该由代理做**：让代理替用户创设资产控制权，
属于安全决策而非工程缺口。当前可用路径仍是用户自备 key 经 `RELAYFIRST_PRIVATE_KEY` 注入。

---

## 八、后续

| 下一步 | 依赖 | 说明 |
|---|---|---|
| **BLK-3 定稿** | 用户 | 生产指派策略；影响 S4 能否用于真实挖矿 |
| 验证接入 CLI + 真实计数 | — | 上面偏差 4、6 |
| 承诺/结论持久化 | — | 上面偏差 7 |
| **S6 收尾**：合规 `init` + 真人计时 | 用户定政策 | 判据①前置 |
| **BLK-4** 定稿 `GenesisValue` | 用户 | 会影响 epoch 划分，从而影响锚定成员 |
| **仓库仍未 `git init`** | 用户 | 守卫不让代理签提交 |
