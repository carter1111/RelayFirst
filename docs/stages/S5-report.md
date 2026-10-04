# S5 验收报告 — 薄节点（自托管）

> 按 `DOCS.md` §4.5 的阶段报告模板。**证据优先于结论。偏差如实记录（`AGENTS.md` §5.4）。**
>
> 日期：2026-10-04
> 阶段：S5（HTTP 收件 / 按 agent 存储 / 去重 / ACK / well-known / SQLite / 静态二进制 / Dockerfile / 多 relay 扇出）

---

## 交付

| 文件 | 行数 | 作用 |
|---|---:|---|
| `internal/node/node.go` | ~300 | 节点 HTTP 面：`POST /messages`、`GET /messages/{agentId}`、`/healthz`、`/.well-known/relayfirst` |
| `internal/protocol/envelope.go` | ~90 | **共享线格式** `Envelope`（**不 import `receipt`**，否则依赖会传递到节点） |
| `internal/sqlite/sqlite.go` | ~150 | DB handle + 全部 schema（**不 import `receipt`**） |
| `internal/sqlite/mailbox.go` | ~180 | `messages` 表存取（`Put`/`ByAgent`/`Get`/`Count`/`AgentCount`） |
| `internal/publish/publish.go` | ~250 | 多 relay 扇出；`Outcome` 区分"部分成功"与"全部失败" |
| `internal/publish/sink.go` | ~120 | `publish.Sink`：先落盘再投递，包在 `ScoringSink` **外层** |
| `internal/publish/receipt_envelope.go` | ~60 | `ReceiptEnvelope`/`DecodeReceipt`（**只在发送侧**，因为必须 import `receipt`） |
| `cmd/relayfirst-node/main.go` | ~200 | 节点可执行文件（与矿工 CLI 分开） |
| `Dockerfile` | ~75 | 两阶段构建；`CGO_ENABLED=0`；非 root；`HEALTHCHECK` |
| `.dockerignore` | ~40 | 排除 `node_modules`(110MB)、`*.db`、密钥 |
| `internal/node/node_test.go` | ~340 | 往返保签名 / ACK / 去重 / 隔离 / well-known / 畸形输入 / 超限 |
| `internal/node/isolation_test.go` | ~90 | **依赖图门禁**：节点二进制不得链接签名/挖矿/LLM 代码 |
| `internal/sqlite/mailbox_test.go` | ~200 | 存取 / 去重 / 按 agent 隔离 / 限制 / 校验 / 重启存活 |
| `internal/publish/publish_test.go` | ~300 | 全达 / **一个挂掉仍送达** / 全挂 / HTTP 错误 / 超时 / 结果顺序 |
| `internal/publish/sink_test.go` | ~210 | 顺序（先落盘）/ 投递失败不算迭代失败 / 落盘失败则不投递 |

**外部依赖：无新增**（仅标准库 + 已有的纯 Go SQLite）。

---

## 对应验收判据

| 判据 | 状态 | 证据 |
|---|---|---|
| **③ 任意陌生人跑起节点** | ✅ **达成** | `docker build` + `docker run` 实测通过（见下） |
| **⑦ no-CGO 构建** | ✅ 保持 | CI 门禁 `CGO_ENABLED=0 go build ./...` → pass |

**S5 完成定义核对：**「按 `MVP.md` §7.2 的命令，一条 `docker run` 起节点」→ ✅ **达成**。

---

## 实测证据

### 镜像与容器

```
docker build -t relayfirst/node:test .
  → 成功；镜像 36.5MB

docker run -d --name rf-node-s5 -p 18080:8080 -v /tmp/rf-data-s5:/data \
  relayfirst/node:test --public-url http://localhost:18080
  → 容器启动

curl /healthz
  → {"ok":true}

curl /.well-known/relayfirst
  → {"name":"relayfirst-node","version":"0.1.0-s5",...,"verifies":false,"note":"store-and-forward only"}
```

### 端到端：真实签名回执 → 容器 → 拉回

```
POST -> {'ok': True, 'stored': True}
PULL count -> 1
bytes identical -> True
```

**`bytes identical -> True` 是本节最重要的一条。** 回执签名覆盖精确字节序列；
节点若解析后重新序列化，回执会**看起来没问题**、然后在别处验证失败 —— 最坏的失败模式。
所以 `payload` 对节点完全不透明，节点只搬运不解释。

### 多 relay 扇出（S5-9）

```
$ relayfirst mine --once --relay <健康容器> --relay http://127.0.0.1:9
  → relayed to 1/2 node(s)
      ! http://127.0.0.1:9: request failed: dial tcp 127.0.0.1:9: connection refused
  （回执仍入库、仍计分；容器端 messages 计数 +1）

$ relayfirst mine --once --relay http://127.0.0.1:9      # 全部不可达
  → relayed to 0/1 node(s) + 明确错误
  （仍入库、仍计分、exit 0）
```

### 测试

```
go test ./... -count=1     → 全部 package ok
新增：internal/node 10 条、internal/store +7 条、internal/publish 14 条
```

### 构建门禁（`./scripts/ci.sh`）

```
CGO_ENABLED=0 go build ./...   → PASS
go vet ./...                    → PASS
gofmt -l .                      → PASS（空）
go test ./...                   → PASS
KAT corpus                      → PASS (53 vectors)
```

---

## 关键设计决定

### 1. 节点是"哑"的，而且这一点**由依赖图强制**

`MVP.md` §7.1 明确：**不验签、不裁决、不读链**。所以节点**不能**链接任何验签代码。

**这不是我一开始就做对的。** 初版把 `node.Envelope` 与 `ReceiptEnvelope` 都放在
`internal/node`。断言"节点不验签"时用 `go list -deps` 一查：

```
github.com/decred/dcrd/dcrec/secp256k1/v4
github.com/relayfirst/relayfirst/internal/eip712
github.com/relayfirst/relayfirst/internal/receipt
```

**节点二进制确实链接了签名栈** —— handler 恰好没验签，但**没有任何结构阻止它开始验签**。

修法需要**两次**拆分，不是一次（因为依赖是传递的）：

| 拆分 | 原因 |
|---|---|
| 线格式 → `internal/protocol` | 让 `internal/node` 不直接 import `receipt` |
| DB handle + schema → `internal/sqlite` | `internal/store` 因存回执而 import `receipt`，节点原先也把它拖进来了 |
| 回执适配器 → `internal/publish` | `ReceiptEnvelope` 必须 import `receipt`；**只有发送方需要它**，转发方只需要格式 |

> 第一次拆完我**又查了一遍**，`internal/node` 仍在链接 —— 因为 `internal/protocol`
> 自己 import 了 `receipt`（我把适配器放在了那里）。**只拆一层不够。**

最终结果（可复核，非承诺）：

```
$ go list -deps ./cmd/relayfirst-node | grep -E 'eip712|secp256k1|internal/receipt|internal/(llm|mining)'
（无输出）

$ relayfirst mine --once --relay http://127.0.0.1:18081 --relay http://127.0.0.1:9
  → relayed to 1/2 node(s)
      ! http://127.0.0.1:9: request failed: ... connection refused
  （容器端 count=1）

$ 从容器拉回 → relayfirst verify /tmp/pulled.json
  → 签名与结构离线验证通过
```

**最后一条是 S5 的完整闭环**：挖矿 → 经**容器化节点**转发（且有一个 relay 已挂）→ 拉回 → **离线验签通过**。
它同时证明了 §7.1"节点不验签"与 §4.2"签名覆盖精确字节"两者可以并存：
节点全程没有验证过任何东西，回执却完好无损。

并且这个性质**被测试锁住**：`internal/node/isolation_test.go` 断言节点二进制
不得链接签名/挖矿/LLM 代码。守卫是**非空转的** —— 同一条检查在
`internal/publish`（确实该有 `receipt`）上会命中。

两个被拆出的依赖（`internal/protocol`、`internal/sqlite`）都刻意**只 import 标准库 +
纯 Go SQLite 驱动**。这是"节点无法验签"从**约定**变成**编译期事实**的地方。

恶意节点仍能**扣留**消息（这是唯一剩下的攻击面），所以 `publish` 必须向多节点扇出。

### 2. `verifies:false` 写进 well-known 文档

客户端最容易犯的错是：把"节点接受了"当成"回执有效"。
最便宜的预防方式是**在客户端第一个拉取的文档里就写明**。所以该字段不只是元数据，
它是一句防止误读的声明。

### 3. 重复投递返回 **200**，不返回冲突

发送方超时后会重试。若重复返回非 2xx，重试会永远重试下去。
`stored:false` 表示"我已经有了"，这对发送方是**成功**，对运维是可区分的信息。

### 4. 部分成功 = 成功

`Outcome.OK()` 只要有**一个**节点确认即为真。
要求全部确认会把每个节点都变成依赖，使网络可靠性等于**最差**那个成员。
"某个节点收了"与"所有节点都收了"是两件事，调用方关心前者，运维关心后者 —— 所以两者都报告。

### 5. 投递失败**不算**挖矿迭代失败

`publish.Sink` 包在 `ScoringSink` **外层**，所以回执在投递之前就已落盘并计分。
把节点不可达变成挖矿失败，会让矿工因为一个"便利设施"下线而停止生产工作 —— 错误的取舍。
失败按节点记录并打印，退出码仍为 0。

### 6. `publish` 用**结构化接口**而非 import `mining`

```go
type ReceiptSink interface {
    Save(r *receipt.Receipt, artifactKey string, at time.Time) error
}
```

Go 接口是结构化满足的，所以 `mining.ReceiptSink` 天然满足它。
这样节点侧代码永远不会把矿工、LLM provider 及其凭据处理拖进来 ——
一个**公开服务器镜像**里不该有能记录 API key 的代码路径。

### 7. 节点镜像只构建 `cmd/relayfirst-node`

同理：镜像里没有矿工代码。少一份攻击面，也少一份"密钥可能被日志打印"的可能。

---

## 偏差与遗留

1. **SSE 未实现。** `MVP.md` §7.1 把 "可选 SSE 推送" 列为可选，本轮只做了 GET 拉取。
   需要实时推送时应补，但拉取已满足 §7.1 的必须项。

2. **`--once` 的投递结果在 JSON 里不可见。** 单次运行走的是 `runOnce`，
   它打印回执 JSON，投递报告由 `OnOutcome` 打到 `stdout` 的独立行。
   两者混在一起时不易解析。长运行模式（`mine` 无 `--once`）显示得很清楚。

3. **`docker run` 的 10 分钟判据未做真人计时。** 实测的机器时间远低于 10 分钟
   （构建 33 秒 + 启动 3 秒），但判据③要求的"陌生人"计时属 S6/S8，未在此阶段做。

4. **`.dockerignore` 排除了 `*.md` 与 `docs/`。** 节点镜像不需要文档，
   但若将来有人想在镜像里放 README，需要调整这一行。

5. **没有做多节点真实网络测试。** 扇出测试用的是 `httptest` 与本地容器，
   不是地理分布的多个节点。跨网络时延与部分失败的表现未验证。

6. **依赖图守卫依赖 `go` 工具链可用。** `internal/node/isolation_test.go` 调用
   `go list -deps`，所以在无工具链的环境（或在 `-short` 下）会 skip。
   这是**刻意的取舍**：真正让性质成立的仍是包结构本身，测试只是防止回归。
   `-short` 下 skip 意味着它不会**主动**报告问题 —— 但 CI 不跑 `-short`。

7. **`internal/store` 现在是 `internal/sqlite` 的薄包装。** `store.DB` 是类型别名，
   `store.Open` 是转发。这保留了矿工侧全部调用点不变，代价是多了一层间接。
   拆分的理由是依赖方向（节点不能到达 `receipt`），不是分层美学。

---

## 后续

| 下一步 | 依赖 | 说明 |
|---|---|---|
| **S6** CLI 上手体验 | S5 | 通向判据①「10 分钟出分」；`init` / `status` / 实时反馈仍未做 |
| S7 链上锚定 | S3-6 | 最独立，不被任何阻塞项卡住 |
| S4 对抗验证 | **BLK-3** | 仍需先定验证者指派策略 |
| **BLK-4** 定稿 `GenesisValue` | — | 开放真实挖矿前必须完成 |
| **仓库仍未 `git init`** | — | 需人工执行（守卫不让代理代做） |
