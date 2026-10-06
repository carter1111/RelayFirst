# Epoch 结算触发器 —— 谁、何时调 `Settle`（D1 条件 2）

> **状态：已定（2026-10-07）。** 这是 D1（emission 统一为模型 B）落地的三个条件之一。
>
> 相关：`MVP.md §6.2`（发行模型）、`internal/mining/scoring_sink.go`（`Finalize`）、
> `planning.md §A2`（D1 裁决）。

---

## 1. 为什么不能"每回执结算"

模型 B（`MVP.md §6.2`）是**固定预算按份额**：`points_i = B(n) × (Σwork_i / Σwork)`。

**分母是本 epoch 的全部 work。** 若在挖矿路径上每来一条回执就结算一次：

- **分母在动** —— 每笔新工作都会改变所有已结算 agent 的份额；
- **已发的分是错的** —— 越早结算的人拿到的比例，在更多工作到来后会变小；
- **不可复核** —— 同一 epoch 的"结算"在不同时刻给出不同数字，客户端无法离线复算。

所以结算**必须是一个 epoch 级、一次性的步骤**，作用于**已关闭的 epoch**。

---

## 2. 触发方式：**显式命令**（本决定）

```bash
relayfirst settle [--epoch N] [--db ./relayfirst.db]
```

- **谁**：**运营者**（或一个调度器）在 epoch **结束后**运行一次。
- **何时**：某个 epoch 的 `[start, end)` 已过（`scoring.EpochBounds`），即**该 epoch 不再接受新工作**之后。
- **默认 epoch**：不传 `--epoch` 时结算**当前** epoch（便于测试与人工操作；生产调度应显式传刚结束的那个）。
- **幂等**：points entry 的 id 形如 `settle:<epoch>:<agent>` —— **同一 epoch 重复结算不重复发分**（`ON CONFLICT DO NOTHING`）。所以**崩溃后重跑是安全的**。

### 为什么不用"自动"（定时器/事件）

| 候选 | 为什么否 |
|---|---|
| **进程内定时器** | 节点/矿工可能根本没在跑；定时器在哪个进程里？多实例会重复结算（虽然幂等，但语义混乱） |
| **收到某条事件就结算** | 触发权交给了**投递**，而 `NET-3` 说节点不权威、无全局序 —— 结算必须由**人/调度**在明确的时间点发起 |
| **每条回执顺带结算** | 见 §1：分母在动，数字不可复核 |

**显式命令**把"何时结算"变成**运营者可见、可重跑、可审计**的动作，与"relay 不权威"一致。

---

## 3. 结算做什么（`ScoringSink.Finalize`）

```text
1. 读该 epoch 的 work totals          （WorkLedger.Totals）
2. scoring.Settle = 份额 + 5% cap     （纯函数，可被任何第三方复算）
3. 每个 agent 写一条 points entry      （id = settle:<epoch>:<agent>，幂等）
4. 返回 settled map                    ← 同一个 map 用于建 Merkle root（S7）
```

**关键**：**返回的 `settled` map 就是建 root 的数字**，所以**"客户端看到的 points"与"锚定的 root"不可能分歧**。

---

## 4. 状态

| 项 | 状态 |
|---|---|
| `scoring.Settle`（纯函数 + 6 测试） | ✅ 已实现 |
| `ScoringSink.Finalize`（题 id 幂等） | ✅ 已实现 |
| `relayfirst settle` 命令 | ✅ 已实现 |
| **全链路测试**（work → Settle → points → Merkle root） | ✅ `TestSettlementChain_WorkToPointsToMerkleRoot` |
| **生产调度**（cron / 定时由谁跑） | ⬜ **未接线** —— 需运营侧定（属部署决策） |
