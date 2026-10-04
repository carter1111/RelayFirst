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

**已实测**：把基线抬高 3 倍（模拟回退）→ 门禁 **FAIL**（`-68.9%`）。

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

## 7. 与 WuKongIM 笔记的关系

`docs/notes/wukongim-tech-advantages.md` 的借鉴清单 #1（benchmark 门禁）**已由本 Phase 完成**。
清单 #2（批量事务）仍待 Phase 2。

**本页不引用任何 WuKongIM 数字** —— 全部为 RelayFirst 自测。