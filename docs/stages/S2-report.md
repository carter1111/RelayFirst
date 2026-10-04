# S2 验收报告

> 按 `DOCS.md` §4.5 的阶段报告模板。**证据优先于结论。偏差如实记录（`AGENTS.md` §5.4）。**
>
> 日期：2026-10-03
> 阶段：S2（挖矿守护进程 + 任务生成）

---

## 交付

| 文件 | 行数 | 作用 |
|---|---:|---|
| `internal/mining/executor.go` | ~140 | `Executor` 接口 + `Deps` + `Run` 分发（**A2 结构强制点**） |
| `internal/mining/probe.go` | ~60 | `probe`：URL 状态码探测（4xx/5xx = 成功结果） |
| `internal/mining/extract.go` | ~190 | `extract`：按 dot-path 抽取字段（`data.price`、`items.2.name`） |
| `internal/mining/compute.go` | ~200 | `compute`：hash / concat / sortjson（含**合成 inline anchor**） |
| `internal/mining/generate.go` | ~130 | `Generator`：只产出 3 种 ground-truth 类型（**A2 运行时强制点**） |
| `internal/mining/loop.go` | ~220 | `Loop.MineOnce`：生成 → 执行 → 拼回执 → 签名 → 自验 |
| `internal/anchor/fetch.go` | ~130 | `Fetcher.Capture/Verify`：取数 + sha256 + 上限保护 |
| `internal/mining/executor_test.go` | ~80 | A2 覆盖性测试 + `Generator` 500 轮测试 |
| `internal/mining/executors_test.go` | ~190 | 三个 executor 行为测试（httptest 实测） |
| `internal/anchor/fetch_test.go` | ~200 | 取数/hash 稳定/内容变更/上限测试 |

**外部依赖：** 无新增（沿用 `golang.org/x/crypto` + `decred secp256k1`）。
**网络相关测试：** 全部用 `httptest`，**零外部网络依赖**。

---

## 对应验收判据

| 判据 | 状态 | 证据 |
|---|---|---|
| **① 10 分钟出积分** | 🟡 **S2 部分达成** | 单次 `MineOnce` 能产出**可离线验证的签名回执**；**积分计算属 S3** |
| ② 第三方离线验证 | 🟡 推进 | 回执仍走 S1 的 `verify`，anchor 重取逻辑已就绪（`Fetcher.Verify`） |

**S2 完成定义核对：**「单机跑守护进程，持续产出可被 S1-9 离线验证的回执」→ 🟡 **核心达成**，但**守护进程长跑 + 存储未接**（见偏差）。

---

## 实测数字

### 测试

```
go test ./... -v
  internal/anchor   → 全部 PASS（8 个测试）
  internal/mining   → 全部 PASS（executor + executors 两组）
  internal/eip712    → 全部 PASS（含 KAT 10/10）
  internal/receipt   → 全部 PASS
  FAIL 计数：0
```

### 构建

```
CGO_ENABLED=0 go build ./...   → exit 0
go vet ./...                    → exit 0
```

### A2 强制（关键证据）

**结构层（`executor.go`）：**

```go
// Executors() 只含 probe/extract/compute。
// TestExecutorsCoverAcceptedTypes 断言：accepted 类型必须有 executor，
// 非 ground-truth 类型（summarize/classify/translate/""）不得有。
```

**运行时层（`generate.go`）：**

```go
// acceptedTypes() 从 receipt.TaskType.Accepted() 派生，而非硬编码。
// TestGeneratorNeverEmitsGeneratedTasks：500 轮生成，0 次出现生成类任务。
```

**分发层（`Run`）：**

```go
// 未接受类型 → 直接拒绝，即使绕过 Generator（TestRun_RejectsUnacceptedType）。
```

**三层互为冗余** —— 这是给 A2 上的三重锁。

### 三个 executor 的 ground truth（实测）

| executor | 输入 | 结果 | 验证方式 |
|---|---|---|---|
| `probe` | `https://.../health` | `"200"` | 独立探测同 URL 得同码 |
| `extract` | `{"data":{"price":42}}` + `["data.price"]` | `{"data.price":42}` | 重取重抽可得 |
| `compute` | `op=hash, input="hello"` | `2cf24dba...` (sha256) | 可复算 |

### deps 边界（诚实记录）

`compute` 是唯一**零网络** executor —— 因此也是自测挖矿链路的最短路径。

---

## 偏差与遗留（`AGENTS.md` §5.4 要求如实记录）

### 偏差

| 项 | `TASKS.md` 要求 | 实际 | 说明 |
|---|---|---|---|
| **S2-6** | BYO API key：openai / anthropic / local 三种 provider 可跑通 | **⬜ 未实现** | 只有 `Deps` 预留；**三种 provider 客户端未写** |
| **S2-7** | 守护进程主循环（持续产出） | 🟡 `Loop.MineOnce` 单次循环已实现 | **无长跑 daemon 命令**（`mine` CLI 属 S6） |
| **S2-8** | 本地 SQLite 存储 | **⬜ 未实现** | `modernc.org/sqlite` 未引入；回执暂未落盘 |
| S2-5 | anchor 记录 | ✅ 达成 | 每回执 ≥1 anchor（probe/extract 真实，compute 为 inline 合成） |

**S2 完成定义严格说未全过**：`MineOnce` 单次闭环成立，但"单机持续产出 + 落盘"尚未接线。

### 遗留（属后续 stage / 同 stage 补全）

```
⬜ S2-6 BYO API key 三种 provider    → 同 stage 补全（触及 BLK-1）
⬜ S2-7 长跑 daemon（mine CLI）     → S6
⬜ S2-8 SQLite 存储                  → 同 stage 补全
⬜ artifactKey 计算                  → S3-1
⬜ 全局去重账本                      → S3-2
⬜ 计分 / epoch                      → S3-3..7
⬜ 对抗验证 + 质押                   → S4
```

**下一步建议：** 优先补齐 S2-6 + S2-8（让"单机持续产出"闭环成立），再进 S3。S2-6 的 BYO API key 是**最容易触碰到 BLK-1 的代码路径**。

---

## 下一步阻塞

| 阻塞 | 状态 |
|---|---|
| **BLK-1** 订阅可程序化驱动？ | ⬜ 仍未核实（S2-6 将正面触碰） |
| BLK-2 真实消费方？ | ⬜ |
| BLK-3 验证者指派策略 | ⬜（S4 前需要） |

---

## 建议的 ADR（沿用 S1 遗留）

- **ADR-0001**（EIP-712 自研薄层）仍未写，suggested 在 S3 开工前补。
- 新增建议 **ADR-0002：S2-6 的 provider 抽象**（BYO API key 的最小接口，为 BLK-1 的"订阅驱动"留扩展点）—— 写 S2-6 时先定接口再实现。