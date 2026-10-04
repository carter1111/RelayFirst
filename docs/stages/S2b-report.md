# S2 补充验收报告（S2-6 / S2-7 / S2-8 / S2-9）

> 按 `DOCS.md` §4.5 的阶段报告模板。**证据优先于结论。偏差如实记录（`AGENTS.md` §5.4）。**
>
> 日期：2026-10-03
> 阶段：S2 补完（推理接入 + 长跑 + 持久化 + 语义化）
> 前置：`docs/stages/S2-report.md`（首版，记录了当时的三项未完成）

---

## 交付

| 文件 | 行数 | 作用 |
|---|---:|---|
| `internal/llm/provider.go` | ~95 | `Provider` 接口 + `Usage` + `ErrNotConfigured` / `ErrEmptyResponse` |
| `internal/llm/openai.go` | ~205 | OpenAI 兼容客户端（`temperature=0`） |
| `internal/llm/anthropic.go` | ~200 | Anthropic messages 客户端 |
| `internal/llm/local.go` | ~130 | 无网络 `LocalProvider` + `NewProvider` 分发 |
| `internal/llm/resolver.go` | ~150 | `FieldResolver`：把模型输出接入 `mining.Resolver` |
| `internal/mining/semantic.go` | ~350 | `Resolver` 接口 + `ParseFieldSpecs` + `NormalizeValue` + `ResolveFields` |
| `internal/mining/runner.go` | ~250 | `Runner`：间隔循环 + 指数退避 + 持久化 + 取消 |
| `internal/store/receipt.go` | ~180 | `ReceiptStore`（按 id / agent+epoch / artifact 查询） |
| `cmd/relayfirst/main.go` | ~350 | 新增 `mine` / `stats` 子命令 |

**外部依赖（1 个）：** `modernc.org/sqlite`（纯 Go，守 A3）。
**密钥：** 全部从运行时配置读入，**不硬编码、不提交、不打印**。

---

## 对应验收判据

| 判据 | 状态 | 证据 |
|---|---|---|
| **⑦ `CGO_ENABLED=0` 构建** | ✅ **仍达成** | 引入 SQLite 后 `CGO_ENABLED=0 go build ./...` exit 0 |
| **② 第三方离线验证** | ✅ **闭环成立** | 实测：mine → 落盘 → 离线验证 `valid:true`；篡改 → `signer mismatch` + exit 1 |
| ① 10 分钟出积分 | 🟡 推进 | 挖矿 + 持久化可用；**计分接入 CLI 属 S6** |
| ④ 伪造工作量被拦住 | ✅ S3 已达成 | S3 的 5 条红队未受影响 |

**S2 完成定义核对：**「单机跑守护进程，持续产出可被离线验证的回执」→ ✅ **达成**。

---

## 实测数字

### 测试

```
go test ./... -v
  --- PASS 计数：162（全仓）
  FAIL 计数：0
```

**较首版 S2 报告（102）净增 60 个。**

### 端到端闭环实测（本次最关键）

```
$ relayfirst mine --once --db test.db --key 0x4c08... --source https://example.com
{
  "artifact": "sha256:9b49f028...",
  "attempts": 1,
  "produced": true,
  "receiptId": "0x2c38fc7b...",
  "taskType": "compute"
}

$ relayfirst verify receipt.json          # 离线，无任何服务器
{ "valid": true, "taskType": "compute", "anchors": 1, ... }

$ relayfirst verify tampered.json          # result.value 被改
error: receipt: signer mismatch: agentId declares 0x2c75... but signature recovers 0x7eb8...
exit=1
```

**「挖出 → 落盘 → 离线验证通过 → 篡改被拒」四步全部成立。**

### 构建门禁

```
CGO_ENABLED=0 go build ./...   → exit 0
go vet ./...                    → exit 0
gofmt -l .                      → 空
go test ./...                   → PASS
KAT corpus                      → PASS (53 vectors)
```

---

## 实现中发现的三个真实问题

> 三个都是**测试抓出来的**,不是笔误。

### 问题 1：`NormalizeValue` 只做"去符号",没做"数值规范化"

`$1,234.50` → `"1234.50"`,而 `1234.5` → `"1234.5"`。

**同一个答案,两个哈希。** 那会让两次诚实运行对同一事实产生分歧 —— **验证就不再是二值的**,而 §5.0 的地基正是二值。

**修正:** 按数值含义重新拼写(去前导零、去尾零、归一化符号)。现在两者都归一为 `"1234.5"`。

### 问题 2：`backoffFor` 在 cap < base 时退化为常量

`backoffBase = 1s`,若 `MaxBackoff = 100ms`,则**第一次退避就已顶到 cap**,增长完全不可见。

**修正:** 首延迟取 `min(base, cap)`,让增长在任何 cap 下都可见。

### 问题 3：网络中断**不会**让矿机停摆(这是好消息)

测试原本假设"服务不可达 → 每次迭代都失败 → 触发退避"。实际失败 —— 因为
**`compute` 任务不需要网络**,生成器随机选到它时迭代**成功**,正确地重置了退避。

**处理:** 这不是 bug,是韧性。已**固化为显式测试**
(`TestRunner_NetworkOutageDoesNotStopMining`),并改由**写入失败**来驱动退避测试。

**值得记:** 这验证了 S2 的"三种任务类型覆盖不同依赖面"设计确实带来了容错 —— 断网时矿机仍能产出。

---

## 偏差与遗留

### 偏差

| 项 | 说明 |
|---|---|
| `NormalizeValue` 语义扩展 | `MVP.md` 只说"归一化为确定性字符串",未指定规则；本实现固定为"去前导零/尾零/符号归一" |
| 归一化位置 | 放在 **mining 层**而非 llm 层 —— 因为 wire format 归 mining 所有,llm 只负责推理 |
| `Runner` 的退避策略 | `MVP.md` 未指定；采用指数退避 + cap |
| `internal/devtools/` 两个新脚本 | `dump_receipt.go` / `tamper.go`（带 `//go:build ignore`）,用于手工验收 |

**无隐藏偏差。**

### 遗留（明确的缺口）

```
⬜ S2-6 CLI 接线        → flags 已存在（--provider/--model/--api-key），但 mine 尚未构造 Resolver
                          （因为需要真实 key；接口与实现已就绪,只差命令行拼接）
⬜ 计分接入 CLI         → ScoringLedgers 已就绪,但 mine 未调用 scoring.Emit（属 S6）
⬜ 语义化任务在生成器中的产出 → Generator 目前只产生 dot-path 的 extract
⬜ S4 对抗验证 + 质押    → 被 BLK-3 阻塞
```

**关键缺口说明:** `mine` 命令**目前只跑 dot-path / probe / compute 任务**,不产语义化任务 ——
因为后者需要注入真实 provider,而那需要 API key。**接口、实现、测试都已就位,只差一个 flag 拼接。**

---

## 下一步阻塞

| 阻塞 | 状态 |
|---|---|
| **BLK-1** 订阅可程序化驱动？ | ⬜ 仍未核实（S2-6 已实现 BYO 路径,不依赖它） |
| BLK-2 真实消费方？ | ⬜ |
| **BLK-3** 验证者指派策略 | ⬜ **S4 开工前必须定** |

---

## 成本断层的处理

断层已记档,见：

- [`docs/notes/inference-cost-gap.md`](../notes/inference-cost-gap.md) —— 调研笔记
- [`ADR-0002`](../decisions/ADR-0002-semantic-extract-as-inference-cost.md) —— 决定记录

**本阶段让成本从"不存在"变为"存在但有前提":**

| | 首版 S2 | 现在 |
|---|---|---|
| 能否消耗推理 | ❌ 不能 | ✅ **能**（语义化字段需 provider） |
| 前提 | —— | 需配置 API key 且任务用语义化字段 |
| 成本性质 | 零 | **经济诱导,非协议强制** |

**诚实边界不变:** 决心机械解析的攻击者仍可绕开模型。抗女巫的主要承重仍是**全局去重**（S3 已实现）。
