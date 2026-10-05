# SQLite 写路径优化：度量与批量（Phase 1 交付）

> **状态：Phase 1 已交付。Phase 2 未开工**（见 §6）。
>
> 日期：2026-10-05
> 依据：外部任务书（2026-10-05）+ `b6ee030` 的核实记录
> 红线（本页全程遵守）：**不动存储引擎**、**不放宽写连接数**、**不做存储接口抽象**

---

## 1. 为什么先度量，而不是先批量

`b6ee030` 核实了 WuKongIM 笔记，发现它写的 **`2000/s` 数字是从 WuKongIM 借来的** ——
RelayFirst **从未测过自己的吞吐**。

所以"SQLite 单连接逐条写入是慢的"这件事，**当时只是一个已知未量化的成本，不是结论**。

**先度量的三个理由：**

1. **没有基线就没有回退门禁** —— 优化后无法证明"变快了"，也无法防止**将来变慢**。
2. **Phase 2 的收益需要对照** —— 任务书要求"优化前后对比数字"，没有前就没有后。
3. **它可能推翻 Phase 2 的前提** —— 见 §6，度量结果**已经**改变了对 A6 的理解。

---

## 2. 实测数字（Phase 1 的核心交付）

**基准配置（写入 `testdata/ingest-baseline.json`）：**

| 项 | 值 |
|---|---|
| **机器** | Apple M4，10 CPU |
| **Go** | go1.27.1 darwin/arm64 |
| **SQLite 驱动** | `modernc.org/sqlite v1.60.1`（**纯 Go，无 CGO** —— A3） |
| **journal_mode** | **wal**（实测，非假设） |
| **synchronous** | **2 = FULL**（实测） |
| **负载** | 20000 次写入，**10% 重复投递**，2048 字节载荷 |
| **写连接数** | 1（`SetMaxOpenConns(1)`） |

**结果：**

```text
吞吐      10782 writes/s
p50       0.052 ms
p99       0.179 ms
max       15.7 ms
stored 18001 / duplicates 1999   ← 与 10% 设定一致
```

**可重复性（同机四次运行）：** 10782 / 11578 / 11124 / 10885 writes/s。

```text
抖动约 7%
```

---

## 3. 为什么负载里必须有重复投递

**这是本次度量最容易被做错的地方。**

若 benchmark 只测**唯一插入**，测的是**生产中不存在的路径** ——
因为发送方**ack 丢失会重试**（S5-3），所以真实 ingest **必然**含重复。

**重复走的是去重路径**（`ON CONFLICT DO NOTHING`），而**去重才是 A6 关心的争用点**。
只测唯一插入会**高估**吞吐，且**完全看不到** Phase 2 最需要理解的东西。

所以负载含 **10% 重复**，且 `stored`/`duplicates` 计数**写入基线** ——
若某次运行的比例对不上，说明负载生成坏了，会立刻可见。

---

## 4. 数字必须带环境，否则是传闻

基线 JSON 里存的不只是数字，还有**机器、Go 版本、驱动版本、journal 模式**。

**理由：** 没有机器的吞吐数字不是度量，是**传闻** —— 这正是本任务要修的那个缺陷。
所以：

- **工作负载不同** → benchmark **拒绝比较**（打印"workload differs"而非误导性 delta）
- **机器不同** → **比较但明确警告** delta 可能是机器差异

**门禁阈值 20% 的依据：** 实测抖动 **~7%**。

> **低于噪声的阈值会因自身抖动而红，然后被人关掉 —— 那比没有门禁更糟。**
> 20% 在噪声之上，且能抓住**真实算法回退**（Phase 2 预期是**倍数级**变化，不是百分比级）。

**已实测**：把基线抬高 3 倍（模拟回退）→ 门禁 **FAIL**（`-64.5%`）。

**⚠️ 门禁本身的两个缺陷（已修，值得记录）：**

1. **基线缺字段 → 比较永远拒绝。** 加入 `batchSize` 后，**旧基线没有该字段**（值 0），
   于是"workload differs"路径一直触发。**修法：重新生成基线。**
2. **命令替换里的空匹配会【静默杀掉脚本】。** 在 `set -euo pipefail` 下，
   `x=$(... | grep ... | head -1)` 若**无匹配**则返回非零，而**命令替换的失败发生在赋值里而非门禁处** ——
   **没有 FAIL 行，脚本直接退出 1**。实测：删除基线的 `batchSize` → **整个步骤无任何输出且 exit 1**，
   这是最难调试的一种结果。

   **修法**：加 `|| true`。**这是本项目第 N 次遇到"失败被吞"**，与"节点不能验签"、
   "变异测试空过"属同一类：**一个不说话的失败比一个响亮的失败危险得多。**

---

## 5. Phase 1 交付物

| 交付物 | 位置 |
|---|---|
| **benchmark（可重复运行）** | `internal/devtools/ingest_bench.go`（`go run … -update` 重写基线） |
| **基线数字 + 环境** | `testdata/ingest-baseline.json` |
| **CI 门禁（第 14 道）** | `scripts/ci.sh` |
| **本页文档** | `docs/notes/sqlite-write-path.md` |

**未改任何生产代码** —— 遵守 Phase 1 边界（`git diff` 只含上述四项）。

---

## 6. ⚠️ 度量结果**已经改变了 Phase 2 的前提**（重要）

任务书基于 `b6ee030` 的说法：

> "`SetMaxOpenConns(1)` **同时是 A6 的原子性保证**（避免'检查-后-插入'竞态）"

**Phase 1 读代码时发现：这句话可能是错的。**

```
internal/sqlite/mailbox.go   Put()      →  INSERT ... ON CONFLICT(receipt_id) DO NOTHING
internal/store/ledger.go     Observe()  →  INSERT ... ON CONFLICT DO UPDATE ... RETURNING seen_count
```

**两条写路径都是【单语句原子 upsert】，不是 check-then-insert。**

`ledger.go` 的注释**自己就写明了**这一点：

> "The insert and the update are expressed as one statement so that two concurrent submissions of
> the same artifact cannot both be told they were first: SQLite applies the primary-key constraint,
> and the RETURNING clause reports which path was taken."

**含义：** 若两条写路径都是单语句原子，则 A6 的原子性**来自 SQL 语义**，
**不是**来自 `SetMaxOpenConns(1)` —— 那么 `SetMaxOpenConns(1)` 的可能**只是性能选择**。

**⚠️ 但这不构成"可以放宽写连接"的结论。** 它只是说明**原来的论证方向错了**。
Phase 2 **必须**先做的事：**用并发测试实测**在 `>1` 写连接下 A6 是否仍成立，
而不是靠"注释说它是单语句"来推断。

**这正是"先度量"的价值** —— 它推翻的**不只是**吞吐假设，还有**我自己上一轮写下的因果**。

---

## 6b. 【Phase 2 前置】A6 论证 —— 用实测替代推断（2026-10-05）

**任务书要求 Phase 2 先写 A6 论证。已按"先度量"原则用测试完成。**

### 实测结论

| 问题 | 答案 |
|---|---|
| A6 的原子性来自哪里？ | **SQL 语义** —— 两条写路径都是**单语句原子 upsert**（§6 已述） |
| 那么 `SetMaxOpenConns(1)` 保护的是什么？ | **不是** A6 的原子性；是**忙等行为** |
| 现在能放宽写连接吗？ | ✅ **障碍已解除，且 A6 实测仍成立**（见 §6d）。但**生产仍不该放宽**（理由见 §6d） |

### 决定性实测：`PRAGMA busy_timeout` 是**每连接**的

8 连接下并发写，**实测大量 `database is locked (SQLITE_BUSY)`**：

```
ledger:  16 个观察者中 10 个 SQLITE_BUSY
message: 16 个写入者中 多数 SQLITE_BUSY
```

**根因（直接探测证实）：**

```text
db.Handle().Exec("PRAGMA busy_timeout = 5000")
  → conn 0 busy_timeout=5000   ← 只有这一条
  → conn 1 busy_timeout=0      ← 池里其余全是 0
  → conn 2 busy_timeout=0
  → conn 3 busy_timeout=0
```

`PRAGMA` **按连接生效**，而 `Exec` 只触及池中**一条**连接。
所以池里**除第一条之外**的连接**没有忙等待** → 立即返回 `SQLITE_BUSY`。

### 为什么原来那句"`SetMaxOpenConns(1)` 是 A6 的原子性保证"**错了两层**

| | 原说法 | 实测 |
|---|---|---|
| 机制 | "避免检查-后-插入竞态" | ❌ 两条路径**都是单语句**，**没有** check-then-insert |
| 结论 | "所以它是 A6 的原子性保证" | ❌ A6 原子性来自 **SQL**；`=1` 保证的是**忙等行为** |

**但"能放宽写连接"这个反向结论同样不成立** —— 实测显示 8 连接**立即崩**。

### 因此 Phase 2 的正确形态

```text
若要让写池 >1，【必须先】把 busy_timeout 按连接应用
    （driver 的 connection hook，或 DSN 参数若驱动支持）
然后【重新跑】TestA6_..._ManyConnections 确认仍只有一个赢家
```

**两个测试曾处于 skip 状态**（`TestA6_ArtifactDedupHasExactlyOneWinner_ManyConnections` /
`TestA6_MessageDedupHasExactlyOneWinner_ManyConnections`）——
**它们就是放宽写池前必须先通过的测量**。skip 而**不是删除**，因为"存在但失败"是比注释更好的"尚未成立"记录。

**另有两个测试断言"当前阻塞"**（`TestA6_ManyConnectionsAreBlockedByAMissingBusyTimeout`）——
一旦有人按连接应用了 pragma，**这些测试会失败**，并把人指向需要重跑的测量。
**它们后来确实失败了**，这正是下面 §6d 的记录。

### 对 Phase 2 的影响（诚实说明）

**Phase 2 的"批量事务"仍可做且可能仍有收益**（把 N 次往返合成 1 次），
但它**不是**"为了放宽连接"的手段 —— 单连接下的批量事务**与 A6 无冲突**
（去重语句本就在事务内单语句原子）。

**而"放宽写连接"是一项独立的前置工作**，且**实测表明它当时不可用**。

---

## 6d. Phase 3 交付：按连接应用 busy_timeout，解锁并重跑 A6 测量（2026-10-05）

### 修法

`PRAGMA busy_timeout` **按连接生效**，`Exec` 只触及池中一条连接。改为**写进 DSN**，
由驱动在**每条新连接**上应用：

```go
// internal/sqlite/sqlite.go
const DefaultBusyTimeoutMillis = 5000
// plain path → file:<path>?_pragma=busy_timeout(5000)
// file: URI  → 追加 & / ?
// :memory:   → 原样（无需锁等待，且加 ? 会改变驱动打开的库）
// 路径里的 ? # % 先百分号转义，否则会被读成参数语法
```

**生产 `SetMaxOpenConns(1)` 未改**。busy_timeout 现在是个**兜底**：万一将来有人放宽池，
不会静默丢掉每连接的等待行为。

### 两个断言"当前阻塞"的测试 —— 它们**如期失败**了

```
TestA6_ManyConnectionsAreBlockedByAMissingBusyTimeout
  → FAIL: "no writer reported a busy error, so a wider write pool may now work"
```

这正是它们被写成"断言失败"的目的（§5 的 ⚠️）。已**翻转为断言成功**
（`TestA6_ManyConnectionsCanWriteWithoutBusyErrors`）：多连接下的不同写入**必须全部成功**。

### 重跑测量（就是 §5 要求"必须先通过"的那两个）

| 测试 | 之前 | 现在 |
|---|---|---|
| `MessageDedupHasExactlyOneWinner_ManyConnections`（8 连接 / 32 投递） | ⏭ skipped | ✅ **PASS — 恰好 1 个赢家** |
| `ArtifactDedupHasExactlyOneWinner_ManyConnections`（8 连接 / 32 观察者） | ⏭ skipped | ✅ **PASS — 恰好 1 个赢家** |
| `ManyDistinctArtifactsUnderContention`（64 个不同 key） | ⏭ skipped | ✅ **PASS — 64 个各自有赢家**（反证：不是"无论 key 都只留一个"） |
| `SingleConnectionIsStillTheProductionDefault` | ✅ | ✅ **仍 PASS**（生产未变） |

**结论（比原来更窄也更准）**：A6 的原子性来自**单语句 upsert**，在 8 连接下**实测仍成立**。
所以"放宽写池"对 A6 是**安全**的 —— 但这是**前置条件，不是理由**。

### ⚠️ 仍然不要放宽生产写池

生产维持 `SetMaxOpenConns(1)`。理由：

1. **单写者序列化是正确的且够用** —— ingest 基准饱和远低于争用中的 SQLite（§6c：~1.2–1.8 万 writes/s）。
2. **放宽只对"并发写"有意义**，而写是 append-heavy、非高并发（§6 原判断未变）。
3. **本轮只证明"放宽是安全的"，没有证明"放宽有收益"**。后者需要另一次基准（放宽后 vs 单连接），**未做**。

**新增单测**（`internal/sqlite/pragma_test.go`）：DSN 各形态与转义；并**回读 `PRAGMA busy_timeout`**
确认它真的生效（不只看 DSN 字符串，因为"语法对但驱动忽略"会留下同样的缺口）。

---

## 6c. Phase 2 交付：批量事务 + 前后对比（2026-10-05）

### 实现

`internal/sqlite/batch.go`：`MessageStore.NewBatcher(limit)` → `Add` / `Flush` / `Results`。

**关键设计：批量与单条走【同一个 SQL 语句】**

`putOne(e execer, m)` 接受 executor（`*sql.DB` 或 `*sql.Tx`），两条路径**共用它**。
两份 SQL 副本**迟早会分歧**，而分歧就是**去重行为差异** —— 那是 A6 唯一依赖的东西。

**默认 batch = 16，不是任务书建议的 64。** 依据是实测饱和点（见下）。

### 前后对比（真实 benchmark，20000 ops，10% 重复）

```text
batch=1    10945 writes/s   p50=0.050ms  p99=0.191ms   ← 现状（逐条）
batch=16   17545 writes/s   p50=0.007ms  p99=0.348ms   ← +60% 吞吐 / p50 快 7x
batch=64   16199 writes/s   p50=0.007ms  p99=0.943ms   ← p99 恶化 5x
```

**复测三次（batch=1 vs batch=16）：**

```text
8348 → 19717      9469 → 18411      9103 → 16153
```

**结论：吞吐提升【1.8x–2.4x】**（首次测得 1.6x 是较低的一次，故区间下限按复测取）。

### ⚠️ 一个必须写明的权衡：**p99 随批量增大而恶化**

```text
p99:  0.191ms (batch=1)  →  0.348ms (batch=16)  →  0.943ms (batch=64)
```

**这是真实代价，不是噪声。** 原因：批量让**一次提交服务多条消息**，
所以**单条消息的观测延迟包含"等本批填满"的时间**。
批量越大，等待越久，**尾延迟越差**。

**所以"批量越大越好"是错的。** 16 是**吞吐与尾延迟的平衡点**：
- 吞吐在 16 后不再增长（64 反而略降）
- p99 在 64 时恶化到近 1ms

**benchmark 报告的是【调用方观测到的每条消息延迟】**（含其分摊的提交时间），
**不是**"单条 INSERT 的耗时" —— 后者会显示一个**没有调用方体验过**的数字，且会**美化批量路径**。

### ⚠️ 批量引入的**新危险**：忘记 Flush 会**阻塞整个节点**

**已实测并写成测试**（`TestBatch_AnUnflushedBatchHoldsTheWriteLock`）：

```text
未 Flush 的批次【持有写锁】→ 其他 Put 【阻塞】（实测阻塞 >2s，直到 Flush 释放）
```

**这比逐条路径的失败模式更糟**：调用方忘了 Flush **不只是丢数据，是让节点停止收发**。

**默认 batch=16 只是限制而非消除**：一批 15 条后来静默，锁会**一直握到下一个消息或 flush**。
**真正的缓解是调用方 flush** —— 所以 `Batcher` 的文档注释把"最后的 Flush"标为
**调用方最可能忘掉的一步**。

### 与 A6 的关系（Phase 2 论证的落地）

**批量【不】与 A6 冲突**，理由已由实测建立：

- A6 原子性来自 **SQL**（单语句 upsert），**不是**连接数
- `putOne` 是 `Put` 一直用的**同一个语句**，放进事务只改变**何时提交**
- 并发重复仍由 `messages` 主键约束解决 —— 第二条看到 `RowsAffected 0` 并报 `stored=false`，
  **无论它在同一批、另一批、还是不在批里**

**测试锁定**：`TestBatch_MatchesSinglePathExactly`（同一序列经两条路径**逐条结果一致**）、
`TestBatch_ConcurrentBatchesStillDedup`（8 个并发批争同一 id → **恰好 1 个 stored**）。

### 未做（红线遵守）

- ❌ **未放宽写连接数**：仍 `SetMaxOpenConns(1)` —— Phase 3 只解除了**障碍**（§6d），
  **没有**放宽池，也**没有**证明放宽有收益（那需要另一次基准）
- ❌ **未动存储引擎**
- ❌ **未做存储接口抽象**
- ⚠️ **批量尚未接入节点 HTTP 路径** —— `Put` 每请求调用一次，**批量不会自动生效**。
  接入需要改变请求处理形态（攒批再提交），属**独立决策**，本 Phase 未做

---

## 7. 与 WuKongIM 笔记的关系

`docs/notes/wukongim-tech-advantages.md` 的借鉴清单 #1（benchmark 门禁）**已由本 Phase 完成**。
清单 #2（批量事务）仍待 Phase 2。

**本页不引用任何 WuKongIM 数字** —— 全部为 RelayFirst 自测。