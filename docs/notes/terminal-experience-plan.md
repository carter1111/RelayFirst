# 终端体验规划 —— 节点 banner/report + CLI 定位

> **状态：节点部分【已实现】（2026-10-06）** —— 见 §7。CLI 部分仍规划中。
>
> 实现：`cmd/relayfirst-node/banner.go`（logo + banner + **子命令 `report`/`status`/`inspect`**）
> + `banner_test.go` / `subcommands_test.go`。
> **实测**：TTY 下打 logo/颜色；`report > file` 与管道**零 ANSI**；非 TTY 启动**仍是单行结构化日志**；
> **无子命令路径不变**（`--listen …` 照旧）。
>
> 相关：`TASKS.md` §11.2（UX-1 / PKG-*）、`MVP.md` §1 原则②（叙事 > 协议完整性）、
> `ADR-0005`（CLI 手写风格）。

---

## 0. 先厘清两件事（否则设计会跑偏）

### 0.1 "谁是用户"决定"哪里需要 UI"

| 二进制 | 用户 | 运行环境 | 需要交互 UI 吗 |
|---|---|---|---|
| `relayfirst-node` | 运维 / 自托管者 | **Docker `-d` / systemd / CI**（**无 TTY**） | ❌ **交互 UI 会毁掉它** |
| `relayfirst`（矿工 CLI） | **人**与**agent** | 本地终端 / agent 进程 | ⚠️ 对人有用，对 agent 无用（见 §3） |
| `relayfirst-mcp` | agent（IDE 进程内） | stdio，非终端 | ❌ 协议流，不可加任何装饰 |

**核心约束：节点必须 headless-first。** 一个"需要 TTY 才能看"的节点在 `docker -d` /
systemd / CI 下是坏的。所以节点的 UI **必须是"有则更好、无则不变"** 的可选层。

### 0.2 用什么判定"有 TTY"

**不用新依赖。** 标准库即可：

```go
info, _ := f.Stat()
isTTY := info.Mode()&os.ModeCharDevice != 0   // 管道/重定向 → false
```

（`go.mod` 里虽有 `mattn/go-isatty`，但那是 `modernc.org/sqlite` 的**间接**依赖，
**不直接引用**它，避免把间接依赖变成直接依赖。）

---

## 1. 节点（`relayfirst-node`）：logo + report + 必要命令

### 1.1 输出分两路（由 TTY 决定，不由 flag 决定）

```text
stderr 是终端   →  品牌 banner（logo + 关键状态）——给人看
stderr 不是终端 →  保持【今天那一行结构化日志】——给 docker logs / 编排 / 脚本
```

**硬要求（验收会测）**：

| # | 要求 |
|---|---|
| R1 | 非 TTY 路径**字节不变**（`docker logs` / 管道解析不受影响） |
| R2 | 任何情况下**不因缺少 TTY 而拒绝启动** |
| R3 | 颜色/ANSI 转义**只在 TTY 下出现**（管道里 grep 不到 `\033`） |
| R4 | `--report` / `report` 是**一次性**动作：打印后退出，**不占用端口** |

### 1.2 Logo

ANSI Shadow 风格 "RELAY"（~40 列，标准终端不折行）：

```text
██████╗ ███████╗██╗      █████╗ ██╗   ██╗
██╔══██╗██╔════╝██║     ██╔══██╗╚██╗ ██╔╝
██████╔╝█████╗  ██║     ███████║ ╚████╔╝
██╔══██╗██╔══╝  ██║     ██╔══██║  ╚██╔╝
██║  ██║███████╗███████╗██║  ██║   ██║
╚═╝  ╚═╝╚══════╝╚══════╝╚═╝  ╚═╝   ╚═╝
```

副标题一行说清它的**信任地位**（这是它最该被记住的事）：

```text
store-and-forward relay · permissionless · multi-relay
no key · cannot forge · validate on the client (MVP.md §5.4)
```

### 1.3 启动 banner（TTY）字段

```text
version   0.1.0-s5
listen    :8080
storage   ./relayfirst-node.db
public    https://relay.example.com        (有 --public-url 才显示)
verify    false  (this node holds no key)
```

### 1.4 `relayfirst-node report` —— 简单 report / 数据

**为什么需要**：现在想知道"这个库里有什么"，得**起节点**再 `curl`。`report` 打开同一个
库、打印、退出，**不占端口**，可在节点运行中另开一个终端跑。

```text
$ relayfirst-node report --storage ./relayfirst-node.db
<logo>
  version ...
  holds
    messages      1234
    agents          17
    agent cards      9
    observations   430
    tasks           12
  serves
    POST /messages · GET /messages/{agentId} · GET /ws/messages/{agentId}
    POST /agents · GET /agents · GET /agents/{agentId}
    GET /observations · GET /observations/{id}/evidence
    POST /tasks · GET /tasks · POST /tasks/{id}/claim
    GET /.well-known/relayfirst · GET /healthz
```

**note**：端点清单是**面向运维的可读视图**，允许与 `/.well-known/relayfirst` **局部重复**；
**机器可读的权威仍是 well-known 文档**（避免第二个真相源）。

### 1.5 必要命令

| 命令 / flag | 作用 | 新增？ |
|---|---|---|
| `relayfirst-node` | 起节点（默认） | 已有 |
| `relayfirst-node report` | 一次性摘要，不占端口 | **新增** |
| `--version` | 版本 | 已有 |
| `--help` | 用法 | 已有 |
| `--listen / --storage / --public-url / --max-payload` | 配置 | 已有 |

**刻意不加**：交互式 REPL、TUI 面板 —— 与 §0.1 的 headless 约束冲突。

### 1.6 验收（若开工）

```text
① `relayfirst-node --help`：TTY 下打 logo；`| cat` 下不打
② 非 TTY 启动输出 == 今天的结构化日志（逐字节，测试锁定）
③ `docker run -d` 正常启动（无 TTY 不报错）
④ `relayfirst-node report` 打印计数并退出，端口未被占用
⑤ 管道输出中 grep 不到 ESC(\033)
```

---

## 2. 与 npm / 发布的关系

- `relayfirst-node` **不发 npm**（长驻服务；Docker 是其路径，见 `TASKS.md` C1）。
- banner / report **不影响** `-tags mainnet` 的 genesis 守卫（守卫在 init，早于 main）。
- `report` 读取即 `sqlite.Open`，**会应用 schema**（幂等）—— 与启动一致，无新行为。

---

## 3. 矿工 CLI（`relayfirst`）—— 见下一份文档

`relayfirst` 的定位问题（"它到底给 agent 用还是给人用"）**直接影响**要不要给它做品牌 UI。
结论与设计见 [`cli-role-and-ux-plan.md`](cli-role-and-ux-plan.md)。

**一句话预告**：**协议层给 agent，UI 层给人** —— 两者**必须都保住**，因为判据 ① 是"**陌生人** 10 分钟出分"。

---

## 7. 实现记录（2026-10-06）

### 命令形状：**子命令**（用户 2026-10-06 定案）

**无子命令时行为不变**（向后兼容）：第一个参数以 `-` 开头 → 走 serve 的 flag 集；
**否则**才当子命令。这一条由 `TestRun_LeadingFlagIsNotASubcommand` 钉住。

```
relayfirst-node                 [flags]  跑服务（默认，未变）
relayfirst-node report          [flags]  离线摘要（读 DB；不占端口）
relayfirst-node status          [flags]  在线检查（HTTP 打一个运行中的节点）
relayfirst-node inspect <what>  [flags]  离线列内容：agents/tasks/observations/messages/db
```

**为什么分三个**（而不是一个 `--report` flag）：

| 命令 | 回答的问题 | 数据来源 |
|---|---|---|
| `report` | 它**存了什么** | 读 SQLite 文件（节点不必在跑） |
| `status` | 它**活着吗 / 现在什么样** | HTTP `/healthz` + `/.well-known/relayfirst` |
| `inspect <what>` | **具体内容**是什么 | 读 SQLite（agents/tasks/observations/messages/db） |

把"存了什么"和"活着吗"折成一个命令，答案就得**依赖节点恰好在不在跑** —— 所以是三个。

**`inspect` 输出 JSON**（不是表格）：与节点 HTTP 端点同构，**可管道、无 ANSI**。
注意：`inspect messages` **故意不打印 payload**（不透明且可能很大），只报存在与大小。

| 项 | 位置 | 证据 |
|---|---|---|
| logo + 启动 banner（TTY） | `cmd/relayfirst-node/banner.go` `printBanner` | pty 下实测渲染 |
| `report` 子命令 | 同上 `runReport` / `printReport` | 实测打印计数并退出，不占端口 |
| `status` 子命令 | 同上 `runStatus` | 实测活节点返回 health+well-known；死节点**报错非静默** |
| `inspect` 子命令 | 同上 `runInspect` | 实测 tasks/db 打印 JSON；缺 --agent/--subject 报错 |
| TTY 分支 | `isTTY`（`os.ModeCharDevice`，**零新依赖**） | `TestIsTTY_FalseForAPipe` |
| 管道/重定向**零 ANSI** | `printReport` 判 **stdout** | `TestReportToAPipeHasNoANSI`（`> file` 与管道均 0 个 `\033`） |
| 非 TTY 启动**字节不变** | banner 只在 `isTTY(os.Stderr)` 时打 | 管道启动实测仍为单行 slog |
| 无子命令不误判 | `run()` 的首参判定 | `TestRun_LeadingFlagIsNotASubcommand` |

### 一处**修正**（过程中发现）

`--report`（当时是 flag）曾写 **stderr**、却按 stderr 判色 → `report > file` 时
**stderr 是终端、stdout 是文件**，文件里**混入 ANSI**。改为：**输出写 stdout、按 stdout 判色**；
banner 仍写 stderr。此修正随子命令化一起落地。

### 未做

- `relayfirst`（矿工 CLI）侧的品牌层 —— 见 [`cli-role-and-ux-plan.md`](cli-role-and-ux-plan.md)（仍规划）
- 交互菜单（形状 3）：**刻意不做** —— 节点是守护进程，TTY-only 菜单价值低且易误触发
