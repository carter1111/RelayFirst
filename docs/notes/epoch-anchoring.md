# 笔记：Epoch 需要一个起点（否则经济恒为零）

- 发现日期：2026-10-03
- 严重度：**阻塞上线的经济层缺陷**
- 状态：已修复（`internal/epoch`），起点值为**待定占位符**
- 相关：`MVP.md` §6.2 / §6.3、`internal/scoring/emission.go`、`internal/receipt/receipt.go`

---

## 1. 现象

把挖矿与计分接通后跑真实 CLI（`mine --once`），产出 3 条回执，`stats` 只有 **1 条被记账**：

```json
{
  "receipts": 3,
  "distinctArtifacts": 1,
  "creditedReceipts": 1,
  "totalPoints": 10
}
```

`distinctArtifacts` 为 1 是因为重复跑了同一个 URL，属于预期。真正的问题是：
**同一个 agent 在此之后再也拿不到任何积分**，无论它做多少不同的工作。

## 2. 根因

`EpochOf` 原本是

```go
uint64(t.Unix() / int64(EpochLength.Seconds()))
```

**没有起点。** epoch 索引直接来自 Unix 时间戳，于是"当前 epoch"约为 **20,729**
（2026-10-03，日长 epoch）。

而 epoch 预算按 `B(n) = B0 × decay^n` 定义，`B0 = 1,000,000`、`decay = 0.99`：

```
epoch      = 20729
B(epoch)   = 3.326203e-85        // 1e6 × 0.99^20729
cap(epoch) = 1.663102e-86        // B × 5%
```

`BudgetFactor(epoch, alreadyEarned)` 的实现是

```go
return 1 - (alreadyEarned / cap)
```

`alreadyEarned = 0` 时它返回 **1**（`alreadyEarned <= 0` 的早退分支），
所以**第一条回执以满分 10 记账**；此后 `alreadyEarned = 10 ≫ cap`，
`BudgetFactor` 恒为 **0**，后续全部零分。

这就是上面 `creditedReceipts: 1` 的来历 —— 经济不是"衰减得很快"，而是**实际上只能发一次**。

## 3. 影响

| 层面 | 后果 |
|---|---|
| 经济 | 全 epoch 预算 ≈ `1e-85` 分，等于零发行；积分经济无法运转 |
| 头矿叙事 | `MVP.md` §6.3 的"越早参与分越多"完全失效 |
| 计分 | 每个 agent 每 epoch 恰好只能拿一次积分，与本 epoch 做了多少工作无关 |
| 回执 | `receipt.NewEpoch` 用同一未锚定公式，链上 `epochRoot` 的 epoch 号会是 20729 这种量级 |

`docs/stages/S3-report.md` 的问题 2 已经指出了 `Allocate` 与上限的分层问题，
但那只是**分配形状**的问题。这里是**发行总量恒为零**，严重一档。

## 4. 修复

新增叶子包 `internal/epoch`，把起点做成一个共享常量：

```go
const GenesisValue int64 = 1790841600 // 2026-10-01T00:00:00Z（占位符）

func Of(t time.Time, length time.Duration) uint64 {
    delta := t.Unix() - GenesisValue
    if delta < 0 {
        return 0   // 起点之前一律 epoch 0，绝不让 uint64 下溢
    }
    return uint64(delta / int64(length.Seconds()))
}
```

`receipt.NewEpoch` 与 `scoring.EpochOf` 都改为调用它。

**为什么必须是常量而不是配置项。** epoch 号在回执的**签名载荷**里
（`MVP.md` §4.2）。任何验证者必须能从同一份回执推出同一个 epoch 号，且**不信任服务器**。
若起点可配置，两个诚实节点用不同起点就会算出不同 epoch 号 → 签名校验失败。
这是"安全失败"（不会静默计错分），但代价是**起点一旦公布就不能改**，
所以它只能是一个固定常量。

**为什么放在独立包。** `scoring` 依赖 `receipt`，公式放任何一个里面都会成环。
`internal/epoch` 不依赖任何内部包，两边都能引。

**下溢的处理。** `delta < 0` 时返回 0，而不是让它变成负数再转 `uint64`。
后者会回绕成一个天文数字，进而把预算压到 0 —— 和这个 bug 的**症状一模一样**，只是走了另一条路。

## 5. 修复后

```
epoch      = 2
B(epoch)   = 9.801000e+05
cap(epoch) = 4.900500e+04

+    1 days  epoch 3     B = 9.702990e+05
+   30 days  epoch 32    B = 7.249803e+05
+  365 days  epoch 367   B = 2.501016e+04
+  730 days  epoch 732   B = 6.382083e+02
+ 1825 days  epoch 1827  B = 1.060475e-02
```

同样的 5 条回执，现在 **5 条全部记账**，合计 ~46.65 分：

```json
{ "receipts": 5, "creditedReceipts": 5, "totalPoints": 46.648984 }
```

（不足 `5 × 10` 的部分来自 `BudgetFactor` 的额度衰减与 `diversity`，两者都是设计意图。）

## 6. 待办：上线前必须定下起点

`GenesisValue` 目前是**占位符**，选在近期过去，好让开发期跑的是正常量级的预算
（epoch ≈ 2，B ≈ 980k）而不是被衰减到零的版本。

**上线前必须改成真实发布日。** 取值的风险是不对称的：

- **设得偏早**：上线时 epoch 已经不小，`B(n)` 已被衰减，早期参与者拿到的比曲线本意少。
  头矿叙事被削弱，但**不会坏**。
- **设得偏晚**：上线从 epoch 0 开始，拿到完整 `B0`，正是设计意图。**偏晚是安全方向。**

又因为 epoch 在签名载荷里，**改动起点会让旧起点下签发的所有回执失效**。
所以这件事必须在开放真实挖矿**之前**做完，不能之后补。

回归保护：`internal/scoring` 的 `TestEpochOf_IsAnchoredAtGenesis`
断言当前 epoch 与预算处于可用范围，锚点被移除就会失败。

## 7. 复现方式

```bash
go run internal/devtools/epochprobe.go
```

打印当前 epoch、`B(n)`、`cap(n)` 及未来若干时点的预算，用于确认锚点是否生效。
