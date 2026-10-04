# S3 验收报告

> 按 `DOCS.md` §4.5 的阶段报告模板。**证据优先于结论。偏差如实记录（`AGENTS.md` §5.4）。**
>
> 日期：2026-10-03
> 阶段：S3（全局去重账本 + 计分 + Epoch）

---

## 交付

| 文件 | 行数 | 作用 |
|---|---:|---|
| `internal/scoring/artifactkey.go` | ~140 | `ArtifactKey` / `SpecURL` / `DomainOfURL`（**A6 的键侧**） |
| `internal/scoring/ledger.go` | ~110 | `Ledger` 接口 + `MemLedger`（**全局**去重账本） |
| `internal/scoring/score.go` | ~150 | `Score`（纯函数）/ `Emit`（唯一推进账本处） |
| `internal/scoring/emission.go` | ~150 | `EpochBudget` / `PerAgentCap` / `Allocate` / `CapAllocation` |
| `internal/scoring/points.go` | ~215 | `PointsLedger` 接口 + `MemPointsLedger`（**A5：无转账**） |
| `internal/scoring/points_test.go` | ~270 | 含 **A5 结构门禁**（反射断言方法集） |
| `internal/scoring/ledger_test.go` | ~310 | 键确定性/agent 无关性/并发 |
| `internal/scoring/score_test.go` | ~390 | 公式边界 + **5 条红队** |
| `internal/scoring/emission_test.go` | ~215 | 衰减/分配/上限 |
| `internal/scoring/helpers_test.go` | ~35 | 测试辅助 |

**外部依赖：无新增**（纯标准库）。**网络测试：零**（全部用构造回执）。

---

## 对应验收判据

| 判据 | 状态 | 证据 |
|---|---|---|
| **④ 伪造工作量被拦住** | ✅ **S3 部分达成** | 5 条红队测试全过（见下）；**④ 完整判定需 S4 验证闭环** |
| ① 10 分钟出积分 | 🟡 推进 | 计分可用；**CLI 接入属 S6** |
| ⑤ 对抗验证闭环 | ⬜ 未开始 | S4 |

**S3 完成定义核对：**「S3-8 / S3-9 两条红队全部通过，即"刷量不赚钱"成立」→ ✅ **达成**（且多出 3 条）。

---

## 实测数字

### 测试

```
go test ./internal/scoring/ -v
  --- PASS 计数：42（顶层）
go test ./... -v
  --- PASS 计数：102（全仓）
  FAIL 计数：0
```

### 构建门禁

```
CGO_ENABLED=0 go build ./...   → exit 0
go vet ./...                    → exit 0
gofmt -l .                      → 空
go test ./...                   → PASS
KAT corpus                      → PASS (53 vectors)
```

### 5 条红队测试

| 测试 | 对应 | 断言 |
|---|---|---|
| `TestRedTeam_FiveAgentsSameURLAllZeroButFirst` | **S3-8** | 5 agent 同 URL → **恰好 1 个得分**，总计 = `BasePoints`（非 5×） |
| `TestRedTeam_ManyAgentsFarmingIsLinearNotMultiplicative` | S3-8 强化 | 50 次刷量 → 总计仍 = `BasePoints` |
| `TestRedTeam_FabricatedContentHashCannotReuseNovelty` | **S3-9** | 编造 hash → 得 0；且键与诚实键**不碰撞** |
| `TestRedTeam_RejectedSubmissionCannotBurnArtifact` | **新增** | 被拒提交**不留痕**（见下"设计缺陷"） |
| `TestRedTeam_UnacceptedTaskTypeCannotScore` | A2 | 生成类任务在计分层也**不可计分** |

### A5 结构门禁

```
TestNoTransferCapability          → PointsLedger 上无任何值转移方法
TestPointsLedger_MethodSetIsClosedToMovement → 方法集恰为 {Credit, Balance, EpochBalance, Entry, Entries, AgentCount}
```

**用反射断言,而非注释约定** —— 将来有人加 `Transfer`,测试失败而不是静默上线。

### 全局性（A6）证据

```
TestArtifactKey_IsAgentIndependent
  → agent A 与 agent B 对同一 url+contentHash 得到【相同】键
TestMemLedger_ConcurrentObserveIsSafe
  → 8 线程争抢同一 key,恰好 1 个判定为 novel
```

### 定点累加（防漂移）

```
TestPointsLedger_FixedPointAccumulation
  → 10,000 次 micro-point 累加后余额精确,无浮点漂移
```

---

## 实现中发现并修掉的两个真实问题

> 两者都是**测试抓出来的语义矛盾**,不是笔误。

### 问题 1：`SeenCount` 语义 → 一个 griefing 攻击面

**原设计:** `SeenCount` 记录**每次**提交(含未通过验证的)。
**测试暴露:** 5 agent 测试断言 `SeenCount == 5`,失败 —— 因为 `Emit` 只记录得分提交。

**判断:代码对,注释错。** 而且原设计有个**我起初没意识到的攻击面:**

> 若未验证的提交也记账,攻击者可**"烧掉"artifact** —— 提交一份垃圾回执命名该 key → 该 key 标记已见 → **后来真正干活的 agent 得 0 分**。

**修正:** 明确只记录**已验证且得分**的观察。补 `TestRedTeam_RejectedSubmissionCannotBurnArtifact` 把这条性质钉死。注释同步改为解释**为什么**这是安全属性。

**代价:** 记账面变窄(拿不到"尝试次数"统计)。**值得。**

### 问题 2：`Allocate` 里两个约束互相打架

**原设计:** 同一函数内同时做**比例分配**与 **per-agent 5% 上限**。
**测试暴露:** 2 个 agent 时比例应给 25%/75%,但 75% 超 5% 上限被压到 5%。

**更糟的是** —— 压顶后余量**被转移给另一个 agent**:`a` 本该拿 25%,却因 `b` 被压顶而拿到 5%(那是别人的份额)。**这是 bug,不只是精度问题。**

**根因:** `MVP.md` 把两者放在**不同层** ——
- `budgetFactor` 在**计分公式内**(§5.3,逐条回执)
- 5% 上限在**结算层**(§6.3,epoch 分配)

**修正:** 拆成两步 —— `Allocate` 只做比例(纯份额语义),新增 `CapAllocation` 显式做上限。**上限从"除法的副作用"变成"显式决定"。**

**代价:** 调用方须记得调 `CapAllocation`。**缓解:** 函数名与 doc 明确表述两者语义差异。

---

## 偏差与遗留

### 偏差

| 项 | 说明 |
|---|---|
| 新增 `CapAllocation` | `MVP.md` 未显式要求此函数;它是"分层"的必然结果(见问题 2) |
| 新增 `MemPointsLedger.TotalPoints` / `MemLedger.Snapshot` | 审计辅助,未被 `TASKS.md` 要求 |
| 定点存储(`MicroPerPoint = 1e6`) | `MVP.md` 未指定精度;选择整数存储以消浮点漂移 |
| `Sighting.SeenCount` 语义收窄 | 从"所有提交"改为"已验证提交"(见问题 1) |

**无隐藏偏差。** 上述均为显式选择,且各附理由。

### 遗留（属后续 stage）

```
⬜ 持久化（SQLite）             → S2-8 未落地；当前 MemLedger / MemPointsLedger 无持久性
⬜ 对抗验证 + 质押              → S4
⬜ 判错扣质押                   → S4
⬜ 计分接入 CLI（mine 实时反馈）→ S6
⬜ ④ 完整判定（需 S4 闭环）     → S8
```

**重要:** 当前两个账本**都是内存实现,重启即丢**。这不是疏忽 —— `Ledger` / `PointsLedger` 接口就是为此而设,SQLite 实现可直接替换而不改调用方。但在持久化落地前,**不得声称 S3 可用于生产**。

---

## 下一步阻塞

| 阻塞 | 阻塞 S3 吗 | 状态 |
|---|---|---|
| **BLK-1** 订阅可程序化驱动？ | ❌ **不阻塞**（S3 纯离线逻辑） | ⬜ 仍未核实 |
| BLK-2 真实消费方？ | ❌ 不阻塞 | ⬜ |
| **BLK-3** 验证者指派策略 | ❌ 不阻塞 S3（S4 前必需） | ⬜ 待定 |

**S3 已完成,且不依赖任何阻塞项。** S4 开工前必须先解 BLK-3。

---

## 建议的 ADR

- **ADR-0001**（EIP-712 自研薄层）仍待补 —— S1 遗留,建议在 S4 前补。
- **ADR-0003（新增建议）：积分用定点整数存储**
  - 背景:浮点累加会漂移,而积分账本必须可精确对账
  - 决定:内部以 micro-point(`1e6`)整数存储,展示层转 float
  - 代价:极小额奖励(<1e-6 点)被舍入为 0
- **ADR-0004（新增建议）：`SeenCount` 只记已验证提交**
  - 背景:记录未验证提交会引入 griefing(烧掉 artifact)
  - 决定:拒绝的提交不留痕
  - 代价:丢失"尝试次数"统计
