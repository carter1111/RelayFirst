# RelayFirst — DevOps Guide

> **本文件定义 RelayFirst 的 CI / CD / Release 流水线。**
> **范围：** 代码门禁 → 交叉编译 → npm publish → Solidity 部署。
> **不在范围：** Staging / Prod 环境（协议类项目无中心化服务器）。
>
> 配套：测试覆盖见 [`CODING_RULES.md`](CODING_RULES.md) §9；文档层级见 [`DOCS.md`](DOCS.md)。

---

## 0. 协议类项目的 DevOps 现实

```
传统 Web App                  RelayFirst（A2A 网络）
───────────────────           ─────────────────────────────
有中心化服务器                 无中心服务器——节点分布式运行
Dev → Staging → Prod          Dev → Release Candidate → "Live"
部署到可控集群                  版本更新靠社区自愿跟随
一人管理所有环境                无法控制"生产环境"
配置在服务器上                   零配置——二进制里只有 genesis epoch
健康检查 / 监控                 你的 node 是哑的 store-and-forward
```

**结论：** RelayFirst 需要的不是三阶段部署，而是**发版可靠性**。

```text
① CI（每次 push/PR）    Gate everything
② Release（手动 tag）   Cross-compile + checksums + npm + deploy
```

---

## 1. 本地门禁

```bash
# 全部 14 道 gate
./scripts/ci.sh
```

| # | Gate | 作用 |
|---|------|------|
| 1 | `CGO_ENABLED=0 go build` | A3 静态构建 |
| 2 | `go vet` | 静态分析 |
| 3 | `gofmt -l` | 格式化 |
| 4 | `go test ./...` | 单元测试 + KAT |
| 5 | `go test -race` | 并发安全 |
| 6 | KAT corpus freshness | 跨语言向量存在 |
| 7 | A5 compliance audit | 积分声明合规 |
| 8 | Frozen receipt corpus | A9 历史回执永可验证 |
| 9 | Ingest benchmark | SQLite 写入路径不倒退 >20% |
| 10 | Version compatibility matrix | ≥30 项跨版本规则/unknown fields/negotiation |
| 11 | Import-graph separation | 节点/verifier/MCP/dashboard 导入图隔离 |
| 12 | Merkle vectors freshness | Go/Solidity ↔ viem 三方对齐 |
| 13 | Solidity forge test | .sol 合约测试 |
| 14 | npm packaging | npx 入口零安装链路 |
| 15 | Release path arms genesis guard | BLK-4: 发布版不携带占位 epoch |

---

## 2. 开发工作流

### 2.1 日常开发

```bash
# 每次 commit 前
./scripts/ci.sh        # 或 git add && git commit --no-verify

# PR 提交后
# GitHub Actions 自动跑 ci.sh（详见 .github/workflows/ci.yml）
```

### 2.2 分支策略

```bash
main          ← 稳定可用（release tags 从这里打）
feature/sX-Y  ← 单个任务的开发分支
```

**规则：**
- 只在 `main` 上打 release tag
- feature branch 合并前必须通过 CI
- 合并使用 squash merge（保持 `main` 清洁）

---

## 3. Release 流程

### 3.1 触发条件

Release **不由 CI 自动触发**。由人判断：

```
什么时候可以发 release？
├── 全部 14 道 CI gate 绿 ✅
├── 目标 stage 的所有任务标 done ✅
├── docs/stages/S<N>-report.md 写了 ✅
├── TASKS.md 已同步 ✅
└── 我审查过 changelog ✅
```

### 3.2 步骤

```bash
# 1. 在 main 分支上
git checkout main
git pull origin main

# 2. 写 CHANGELOG.md 条目
# （用下面格式，简明）

# 3. 打 tag
git tag v0.1.0          # 正式 release
# git tag v0.1.0-rc1    # release candidate（可选，MVP 不需 RC）

# 4. 推送 tag（触 GitHub Actions）
git push origin main
git push origin v0.1.0

# 5. Actions 会自动做三件事：
#    a) 交叉编译 5 平台 × 5 二进制
#    b) npm publish
#    c) forge deploy → Sepolia（仅 tag）
```

### 3.3 RC vs Final

| 类型 | 用途 | 建议 |
|---|---|---|
| **RC** (`v0.1.0-rc1`) | 让社区验证新版本 | MVP 期间**不需要**（没有活跃用户基线） |
| **Final** (`v0.1.0`) | 正式发布 | **直接用这个** |

> **何时需要 RC？** 当你有一个已有活跃用户的分布式网络时。
> RelayFirst MVP 阶段：没人跑旧版 → RC 是多此一举。

---

## 4. 环境与安全

### 4.1 本地环境变量

```bash
# .env.local（不要提交！.gitignore 已排除）
export SEPOLIA_RPC_URL="https://sepolia.infura.io/v3/YOUR_KEY"
export PRIVATE_KEY="0x..."       # 仅用于 forge deploy
export NPM_TOKEN="npm_..."       # 仅用于 npm publish
```

### 4.2 CI 环境变量（GitHub Secrets）

| Secret | 用途 | 谁持有 |
|---|---|---|
| `SEPOLIA_RPC_URL` | Forge 部署到 Sepolia | Release operator |
| `NPM_TOKEN` | npm publish | Release operator |
| `GITHUB_TOKEN` | 创建 GitHub Release | Actions（自动） |

### 4.3 密钥最小化原则

```text
MCP server     → 零密钥（结构性质，门禁 11 验证）
relayfirst-node → 可选验证者密钥（ADR-0004，独立组件）
relayfirst-verifier → 签名断言密钥（独立二进制）
CLI             → EVM 密钥（用户自有，不入日志）
```

**绝对禁止：**
- 私钥出现在任何代码中
- 私钥出现在任何提交记录中
- 私钥以明文形式出现在命令参数中

---

## 5. 发布产物清单

每个 release tag 自动生成：

```
GitHub Releases (auto-generated):
├── relayfirst-darwin-amd64        ← macOS Intel
├── relayfirst-darwin-arm64        ← macOS Apple Silicon
├── relayfirst-linux-amd64         ← Linux Intel
├── relayfirst-linux-arm64         ← Linux ARM
├── relayfirst-windows-amd64.exe   ← Windows Intel
├── checksums.txt                  ← SHA-256
└── README-release-notes.md        ← changelog 摘要

npm packages:
├── relayfirst@latest              ← npx relayfirst (CLI)
├── relayfirst-mcp                 ← MCP server
└── relayfirst-dashboard           ← TUI dashboard
```

---

## 6. 故障恢复

### 6.1 发布失败

```bash
# Actions 失败了？查看日志然后：
# 1. 修复问题
# 2.  bump patch version
# 3. 重新打 tag

git tag -d v0.1.1      # 删除坏 tag
git push origin :refs/tags/v0.1.1   # 删除远程 tag
git tag v0.1.1          # 重新打 tag
git push origin v0.1.1
```

### 6.2 安全事件

发现严重漏洞后的流程：

```
1. hotfix 分支从 main 切出
2. 修 bug + 加回归测试
3. CI 全绿
4. 紧急 release: v0.1.1 (patch)
5. SECURITY.md 披露渠道通知社区
```

参见 [`SECURITY.md`](SECURITY.md)（待建）。

---

## 7. 升级指南

对于节点运营者（社区成员）：

```bash
# 升级 CLI
npx relayfirst@latest version    # 当前版本
npx relayfirst@latest upgrade    # 升级到最新

# 升级 Docker node
docker pull relayfirst/node:latest
docker stop relayfirst-node
docker rm relayfirst-node
docker run ... relayfirst/node:latest  # 新容器
```

**注意：** 升级节点**不影响**已存储的数据（SQLite 单文件持久化）。

---

## 8. 监控与维护（非阻塞性）

由于我们没有中心服务器，以下指标**由社区自行收集**，不属于项目本身的运维：

| 指标 | 谁收集 | 说明 |
|---|---|---|
| 活跃节点数 | 任何想统计的人 | 通过 `/ws/messages/{agentId}` 或 `/agents` 探测 |
| 吞吐量 (w/s) | 节点运营者 | 内置 benchmark 可在节点端跑 |
| 链上锚定 | 任何人 | 读取 `RelayAnchor.sol` 的 `epochRoot` mapping |
| SBT holders | 任何人 | 读取 `RelayPoints.sol` 的总供应量 |

**这些指标的聚合仪表盘属于 Indexer（GAP-G1），未排入 MVP。**
