# S8 验收报告 — 红队与上线门禁

> 按 `DOCS.md` §4.5 的阶段报告模板。**证据优先于结论。偏差如实记录（`AGENTS.md` §5.4）。**
>
> 日期：2026-10-04
> 阶段：S8（七条验收判据的红队验证）
> **状态：④⑥⑦ 达成；②③⑤ 机制达成；① 未做（需真人）；BLK-2 未解决。**

---

## 一句话结论

**四项伪造攻击对真实栈全部 0 分**，且有一条控制测试证明诚实工作仍能得分。
**但上线门禁没有全过**：判据①需要真人计时，**BLK-2（真实消费方）仍未解决**。

`TASKS.md` 写明：**七条全过 + BLK-2 已解决。任何一条不过，不上线。**
按此，**现在不应上线**。

---

## 七条判据逐条

| # | 判据 | 状态 | 证据 / 缺口 |
|---|---|---|---|
| ① | 陌生人 10 分钟出分 | ⬜ **未验证** | 需真人计时。机器实测整条路径 < 3 秒，但"陌生人无协助"不能用机器替代 |
| ② | 关掉服务器 → 回执仍可离线验证 | ✅ **达成** | `verify` 无任何网络调用；S6 实测导出后逐枚离线验签通过、篡改副本被拒 |
| ③ | 陌生人一条 `docker run` 起节点 | ✅ **代码层** | S5 实测构建 + 运行成功，`/healthz` 与 `/.well-known` 正常。**"陌生人"计时未做** |
| ④ | 四项伪造攻击全部 0 分 | ✅ **达成** | `internal/redteam`，见下 |
| ⑤ | 对抗验证闭环 + 判错扣质押 | ✅ **机制** | S4 端到端可跑；"扣质押"= 承诺状态变化，非扣分（A5） |
| ⑥ | TS/Go KAT 字节相同（CI 门禁） | ✅ **达成** | 两道 KAT 门禁 + **第三实现**（viem）门禁 |
| ⑦ | `CGO_ENABLED=0` 构建通过（CI 门禁） | ✅ **达成** | CI 门禁；节点镜像为静态二进制 |

---

## 一、四项伪造攻击（判据 ④）

全部跑在**真实栈**上：真实 SQLite、真实 executor、真实验证者、真实评分路径。
**没有 stub。**

| 攻击 | 做法 | 被什么拦住 |
|---|---|---|
| **a. 伪造 result** | 回执声称 `{"id":1,"ok":false}`，源实际返回 `{"id":42,"ok":true}`。**签名有效、结构合法** | 重执行：结果不重现 → 拒绝。**测试同时断言：S4 之前的自检会接受它** —— 这就是 S4 关闭的缺口 |
| **b. 伪造 anchor** | 编造一个源从未返回过的 contentHash | 重取 anchor：记录值与当前值不符 → 拒绝 |
| **c. 重放他人回执** | 两种：原样重交；以及**改签到自己名下** | 原样重交归因于原作者，重交者零收益；改签后 artifactKey 不含 agent，去重账本仍视为同一 artifact |
| **d. 自验** | 生产者把 `VerifierID` 写为自己 | `Verify` 拒绝自验；`RecordedVerdicts` **与** durable 读侧也都拒绝自写结论 |

### 控制测试（同样重要）

```
TestRedTeam_HonestWorkStillEarns
```

**一个"全部拒绝"的实现会通过上面每一条测试。** 所以必须有一条证明诚实路径仍然得分，
否则这些红队测试证明的是"系统坏了"，而不是"攻击被拦住"。

### 额外：签名层守卫

```
TestRedTeam_AttackersKeyDoesNotImpersonate
```

四项攻击都依赖"攻击者无法冒充他人签名"。测试把一枚诚实回执的 `AgentID` 改成攻击者，
断言验签失败，且**恢复出的签名者不等于声明者**。若这一层可绕过，上层验证再严密也无意义。

### 为什么断言是"余额不增"

每条测试的最终断言都是**攻击者积分余额没有上升**，而不是"状态字段是 rejected"。
一个只看状态字段的测试，可能在积分照走的情况下通过。

---

## 二、门禁（判据 ⑥⑦）

```
./scripts/ci.sh                     → 9/9 PASS
  CGO_ENABLED=0 go build            → PASS        (判据 ⑦)
  go vet / gofmt                    → PASS
  go test ./...                     → PASS
  go test -race ./...               → PASS        (并发门禁)
  EIP-712 KAT (53 vectors)          → PASS        (判据 ⑥)
  Merkle 语料新鲜度 (10 trees)       → PASS        (判据 ⑥)
  forge test (15 tests)             → PASS
  viem 交叉校验 (10 roots, 72 proofs) → PASS        (判据 ⑥，第三实现)

go test ./... -count=1 -v           → 383 PASS / 0 SKIP / 0 FAIL
```

### 门禁是**非空转**的（逐条实测）

| 破坏 | 结果 |
|---|---|
| 向 `PointsLedger` 注入 `Debit` | A5 两条守卫**同时 FAIL** |
| 篡改 Merkle 语料的 root | 新鲜度门禁 **FAIL** |
| 篡改一条 proof 的 sibling | viem 门禁 **FAIL**，并打印两个不同的 hash |
| 移除 `MemPointsLedger.Credit` 的锁 | race 门禁 **FAIL**，含完整栈 |
| 移除 `Verifier.recordMu` 的锁 | race 门禁 **FAIL**，复现真实竞争 |

**一个不会失败的门禁等于没有门禁**，所以这五条都是实测的，不是推断的。

> **关于第四条的一次失败尝试：** 我最初移除的是 `Balance` 的锁，门禁**仍然 PASS** ——
> 因为该函数只在所有写者结束后调用，**没有重叠访问就没有竞争**。
> **那是我的破坏无效，不是门禁失灵。** 换成 `Credit` 立刻 FAIL。
> 记下来是因为：**验证一道门禁时，必须制造它本该抓到的那类故障。**

### race 门禁的覆盖（本轮补完）

race 检测器**只报真实发生的竞争**，所以门禁强度取决于测试里的并发。
补覆盖前只有 2 个包有并发测试；本轮补到 **5 个**：

```
之前：scoring, store
之后：mining, node, scoring, store, verification
```

**补覆盖的直接收益：找到一个真实的数据竞争。**
`Verifier.Verify` 原地改写调用方传入的回执，两个 goroutine 验证同一对象时并发写同一字段。
race 检测器立刻报出；已修（只串行化记录步骤，重执行保持并行）。详见 S4 报告。

> **这印证了上一轮写下的局限是真的。** 如果只满足于"门禁 PASS"，
> 这个竞争会一直留着 —— 直到某天两个调用方共享了一枚回执对象。

---

## 三、本轮新增：把第三实现纳入 CI

viem 对 Merkle root 的交叉校验原先是**一次性人工检查**。人工检查的价值有限：
**没人重跑，就抓不到回归。**

现在它是 CI 里的一道门禁，且**只依赖 `node_modules` 里已有的 viem**（无网络、无链、无部署）。

**它为什么重要：** Go 与 Solidity 都是我们自己写的，
两者一致**可能**是共同误读了规范。viem 是独立实现、不共享任何代码，
所以与它一致是"这个构造是规范的忠实实现"的证据，而不是"我们两个文件碰巧约定相同"。

顺带修了一个真实的小毛病：Go 生成器对**单叶树**输出 `"siblings": null`，
迫使每个消费方特判。改为输出 `[]` —— **一种规范表示，胜过四处特判。**

---

## 四、诚实边界

1. **不应上线。** 判据①未验证，**BLK-2 未解决**。`TASKS.md` 的门禁是明确的。
2. **判据③的"陌生人"计时未做。** 构建 + 启动实测远低于 10 分钟，但那是机器时间。
3. **红队测试证明的是"这些攻击在当前实现下不成立"**，不是"系统无法被攻破"。
   `MVP.md` §5.6 已声明：大户养足够多 sybil **仍可自验**，成本线性上升。
   **不要把这些测试讲成无懈可击。**
4. **判据⑤的"扣质押"不是扣款。** 见 S4 报告：A5 下不可行，实现为承诺状态变化。

---

## 六、S8-9：积分声明合规检查（本轮新增）

**判据：** 风险 R7（监管暴露）。**不变式 A5**：积分不可转让、不定价、不承诺回报，
且**必须写进 UI 和文档**。

**问题：** A5 的**结构那一半**早已有守卫 —— `PointsLedger` 的方法集被反射测试锁定为 6 个、
无 `Debit`，加一个转账动词就会构建失败。但**文字那一半没有任何守卫**：
一个打印"your points are worth $5"的 CLI 会让 A5 失效，而**所有 Go 测试仍然通过**。

### 交付

| 文件 | 作用 |
|---|---|
| `internal/compliance/claims.go` | A5 文本守卫：风险词表（29 条，6 个概念组）+ 批准免责语白名单 + **逐行 points 作用域** |
| `internal/compliance/claims_test.go` | 9 组测试：必须抓到违规 / 必须**看见并放行**免责语 / 必须不误报 / 逐行作用域 / 两条限制 |
| `internal/devtools/compliance_audit.go` | 人类可读审计工具；发现违规 **exit 1** |
| `scripts/ci.sh` | **第 10 道门禁** |

### 实测

```
$ go run internal/devtools/compliance_audit.go -v
ok    cmd/relayfirst/main.go:115  "promised return"  (excused: "carry no promised return")
ok    cmd/relayfirst/main.go:115  "transferable"     (excused: "non-transferable")
ok    cmd/relayfirst/main.go:793  "transferable"     (excused: "non-transferable")
ok    cmd/relayfirst/main.go:942  "promised return"  (excused: "carry no promised return")
ok    cmd/relayfirst/main.go:942  "transferable"     (excused: "non-transferable")
scanned 2 file(s), matched 5 phrase(s)
no A5 violations found
```

**5 条命中、全部落在批准免责语内** —— 这正是"守卫在跑且没有误报"的证据。
（若命中数为 0，说明守卫根本没看到真实文本；审计工具对此会打印 WARNING。）

**非空转实测：**

```
$ echo 'Your points are worth $5 each and can be redeemed for USDC.' > /tmp/v.txt
$ go run internal/devtools/compliance_audit.go /tmp/v.txt
FAIL  /tmp/v.txt:1  "worth"     — asserts points have worth
FAIL  /tmp/v.txt:1  "USDC"      — denominates points in a currency
FAIL  /tmp/v.txt:1  "redeemed"  — asserts points are redeemable
3 finding(s) need review
exit status 1
```

### 三个设计决定（值得记住）

1. **逐行 points 作用域。** 风险词只在**该行谈到 points** 时才算 claim。
   没有这个作用域，词表不可用：`price` 出现在合法的挖矿示例里
   （`--semantic "the main product price"`）、`guarantee` 出现在消息排序的注释里。
   **误报会让读者学会忽略守卫，比没有守卫更糟。**
   代价已明写：不含 "points" 的价值声明会被漏掉。

2. **免责语用精确短语，不用"附近有否定词"启发式。**
   启发式两个方向都会误判：`"points are not redeemable, and you can exchange them for cash"`
   同时含否定词与真实违规，邻近检查会**放行违规**。
   代价也已明写并测试：措辞伪装成免责语的违规会通过。

3. **测试断言"看见并放行"而非"没看见"。**
   `TestScan_ExcusesApprovedDisclaimers` 同时断言 `len(all) > 0` 与 `len(unexcused) == 0`。
   少了第一个断言，这个测试会在**守卫彻底失效**时依然变绿 —— 这是本包最危险的失败方式。

### 测试套件抓到两个真实缺陷（不是假设）

- **大小写**：初版风险词表大小写敏感，漏掉 `Redeem` / `Withdraw` / `USDC` 等 4 条明显违规。
  测试立刻报出 → 全部改为 `(?i)`。
- **单复数**：作用域只认 `points`，于是 `"Each point is worth 1 USDC"` 被漏掉。
  → 作用域改为 `point`（含单数）。

### 诚实边界

**这是绊线，不是证明。** 两条限制都**有测试锁定**（`TestScan_LimitsAreReal`），
不是写在注释里就算数：

- **无法评估新措辞** —— 词表之外的表达会被漏掉。
- **无法区分免责语与伪装成免责语的违规**。

**并且它只是文本层检查，不是法律意见。** R7 的关闭需要人工合规复核，
本门禁只是让"文字层不出现承诺"变成**可自动复核**的。

---

## 七、诚实边界

1. **不应上线。** 判据①未验证，**BLK-2 未解决**。`TASKS.md` 的门禁是明确的。
2. **判据③的"陌生人"计时未做。** 构建 + 启动实测远低于 10 分钟，但那是机器时间。
3. **红队测试证明的是"这些攻击在当前实现下不成立"**，不是"系统无法被攻破"。
   `MVP.md` §5.6 已声明：大户养足够多 sybil **仍可自验**，成本线性上升。
   **不要把这些测试讲成无懈可击。**
4. **判据⑤的"扣质押"不是扣款。** 见 S4 报告：A5 下不可行，实现为承诺状态变化。
5. **S8-9 是绊线，不是合规意见。** 见 §六「诚实边界」。

---

## 八、后续

| 下一步 | 依赖 | 说明 |
|---|---|---|
| **判据① 真人计时** | 用户 | 需一个未接触项目的人 |
| **BLK-2 真实消费方** | 用户（外部） | 上线硬前置 |
| **BLK-3 指派策略** | 用户 | 规模化验证的前置 |
| **BLK-4 `GenesisValue`** | 用户 | 影响 epoch 划分 → 影响锚定成员 |
| **S7 真人部署** | 用户资金账户 | 判据⑥的链上那一半 |
| **R7 人工合规复核** | 用户 | 文本层已自动化；法律层仍需人 |
| **仓库仍未 `git init`** | 用户 | 守卫不让代理签提交 |