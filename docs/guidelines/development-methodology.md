# 去中心化协议项目 — Planning → Tasks → Action → DevOps 方法论

> **适用场景：** Nostr / Matrix / A2A Network / 任何分布式节点网络项目。
> **不适用：** 传统 Web App（有中心服务器、跑 Staging/Prod 那种）。
>
> **一句话核心：**
> 协议类项目的"部署"不是把代码推上服务器，而是**把版本推给社区**。
> 所以 DevOps = **发版可靠性**，不是三阶段部署流水线。

---

## 🏗️ 总览：四个阶段一条线

```
Planning         Tasks             Action              DevOps
───────────      ──────────        ──────────          ─────────────
想法/讨论         任务排期           写代码/部署          CI gate (每次 push)
 ↓ 定案            ↓ solid plan     ↓ 附证据            ↓ tag trigger
 docs/notes/      TASKS.md          代码实现             cross-compile
 feature docs    验收 + 依赖       运营/接洽             npm publish
                标 done+证据                                deploy solidity
                                                          create release
```

每个阶段都有**明确的输入输出、门禁规则、和产出文件**。下面逐一拆解。

---

## Phase 1 — Planning（未定案）

### 📌 什么时候进来？

当有一个新想法、优化方向、缺口发现、或者用户提出需求时：

```
来源:
├── "要不要加 Indexer？"
├── "节点要不要有 TUI dashboard？"
├── "签名该用库还是自研？"
└── "BLK-1 订阅能不能程序化驱动？"
```

### 📁 放在哪？

**唯一入口：** `docs/notes/planning.md`（伞文档）

每个 feature 一份独立文档，挂在伞文档 §0 下：

```
docs/notes/planning.md                  ← 伞文档（索引页）
├── docs/notes/decentralization-gap.md   ← G1→G5 缺口分析
├── docs/notes/explorer-indexer-plan.md  ← Indexer 方案
├── docs/notes/node-tui-dashboard-plan.md ← TUI Dashboard 方案
└── ...
```

### ✅ 进门条件（P1）

| 规则 | 说明 |
|------|------|
| **P1** | **未定案 → 只在 Planning** |
| **P2** | **进 TASKS 必须有 solid plan**（任务级分解 + 验收 + 依赖） |

### 🔚 怎么出去？

三种去向：

```text
① 用户明确批准     → 进入 TASKS（排期）
② 决定不做         → 从 planning.md 移除
③ 范围变化        → 先改 MVP.md（L0），再同步 TASKS
```

### 📋 Checklist

```bash
□ 想法够具体吗？（不能只是"要做个 xxx"）
□ 写了 feature 文档吗？（docs/notes/<name>.md）
□ 登记在 planning.md §0 了吗？
□ 没有跳过 Planning 直接进 TASKS 吗？(违反 P1/P2)
```

---

## Phase 2 — Tasks（排期）

### 📌 什么时候进来？

当 Planning 里某件事**定了**——用户批准了，范围明确了，设计 solid 到可以直接拆任务。

### 📁 放在哪？

**唯一入口：** `TASKS.md`（主表）

```markdown
## S1 — 回执 + 签名 + KAT
| id | 任务 | 依赖 | 验收 | 状态 |
|----|------|------|------|------|
| S1-1 | init Go module | — | build pass | ✅ done |
| S1-2 | Receipt struct | S1-1 | 字段对齐 | ✅ done |
```

### ✅ 进门条件（P2）

| 条件 | 不满足会怎样 |
|------|------------|
| 有 solid plan | 否则不该出现在这里 |
| 依赖已满足 | 否则标 blocked |
| 验收标准写清楚了 | 否则做完了无法判断 |
| 涉及范围变化 → MVP.md 已先更新 | **禁止写入** |

### 🔚 怎么出去？

```text
标 done + 附证据
  ↓
更新 HANDOFF.md（状态/锁定决策变化）
  ↓
从 planning.md §0 移除（如果之前是未定案的）
```

### 📋 Checklist

```bash
□ 有 solid plan 才写进去（P2）
□ 有依赖列吗？
□ 有验收列吗？（能判断对/错）
□ 状态机正确吗？(todo → in-progress → done → verified)
□ 标 done 时有证据吗？（测试数/命令输出/提交哈希）
□ 验收判据未过的项，没标 verified 吧？
```

---

## Phase 3 — Action（实现）

### 📌 开始读序（不可跳）

```bash
1. DOCS.md §1          ← 谁是老大
2. MVP.md              ← 范围（不可偏离）
3. TASKS.md §0–§1      ← 阻塞项
4. HANDOFF.md §5, §9   ← 关键概念 + 反模式
5. CODING_RULES.md     ← 动代码前
```

### 🎯 执行纪律

| 规则 | 说明 |
|------|------|
| **一次只做一个 task id** | 不要横跨多个 stage |
| **只改本仓库文件** | 不碰 sibling 项目 |
| **遇到不确定 → 停下来问** | 偏离选型 / 新依赖 / 文档矛盾 |
| **偏差如实记录** | 写 `docs/stages/S<N>-report.md` |

### ✅ 完成定义（Definition of Done）

```text
① 实现与 TASKS.md 的验收列一致
② 有可复核的证据（测试数/命令输出/提交哈希）
③ 相关不变式（A1–A8）未被违反
④ 若涉及范围 → MVP.md 已先更新
⑤ 若跨 stage → docs/stages/ 有报告
⑥ 无遗留未记录的偏差
缺任何一条，不算完成。
```

### 📋 Checklist（每次提交前自检）

```bash
□ 读的是 MVP.md 而不是 ARCHITECTURE.md？
□ 我手写密码学了吗？（应该没有 —— A1）
□ 任务类型还是只有 probe/extract/compute 吗？（A2）
□ CGO_ENABLED=0 还能构建吗？（A3）
□ KAT 向量还在 CI 门禁里吗？（A4）
□ 我改的范围需要先改 MVP.md 吗？
□ 我改到 sibling 项目了吗？（A8 —— 应该没有）
□ 我的 done 有证据吗？
```

---

## Phase 4 — DevOps（CI → Release）

### 核心原则

> 协议类项目的 DevOps **不是三阶段部署流水线**（Dev→Staging→Prod），
> 而是**发版可靠性**。因为你没有生产服务器可以控制。

```
传统 Web App              协议类项目
─────────────            ────────────
Dev → Staging → Prod     Dev → Release → Live
一人管理所有服务器         版本靠社区自愿跟随
配置在服务器上             零配置（二进制里只有 epoch）
```

---

### 4.1 Local Gate — 本地门禁 (`scripts/ci.sh`)

```bash
./scripts/ci.sh    # 全部门关卡在你本地能跑
```

典型 **10–15 道 gate**，每道对应一条不变式或关键约束：

| # | Gate | 对应不变式 |
|---|------|-----------|
| 1 | `CGO_ENABLED=0 go build` | A3 |
| 2 | `go vet` | 静态分析 |
| 3 | `gofmt -l` | 格式化 |
| 4 | `go test ./...` | 单元测试 |
| 5 | `go test -race` | 并发安全 |
| 6 | KAT corpus freshness | A4 |
| 7 | A5 compliance audit | A5 |
| 8 | Frozen receipt corpus | A9 |
| 9 | Ingest benchmark | 性能回归 |
| 10 | Version compatibility matrix | 跨版本 |
| 11 | Import-graph separation | 架构边界 |
| 12 | Merkle vectors freshness | 三方一致性 |
| 13 | Solidity forge test | 合约正确性 |
| 14 | npm packaging | npx 入口 |
| 15 | Release path arms genesis guard | BLK-4 |

---

### 4.2 GitHub Actions — CI Gate (`.github/workflows/ci.yml`)

触发：每次 `push` 到 `main` 或 `PR` 合并。

```yaml
on:
  push: { branches: [main] }
  pull_request: { branches: [main] }
```

**作用：** 1-1 映射 `ci.sh` 的所有 gate，在 GitHub 环境再跑一遍。任何一道失败 → PR 被阻止。

---

### 4.3 GitHub Actions — Release Pipeline (`.github/workflows/release.yml`)

触发：**手动打 tag** `v*`。

```yaml
on:
  push: { tags: ['v*'] }
```

自动执行四步：

```
① Cross-compile 5 平台
  ├── relayfirst-darwin-amd64
  ├── relayfirst-darwin-arm64
  ├── relayfirst-linux-amd64
  ├── relayfirst-linux-arm64
  └── relayfirst-windows-amd64.exe

② npm publish（如适用）
  ├── relayfirst@latest
  ├── relayfirst-mcp
  └── relayfirst-dashboard

③ Forge deploy → 测试网（如适用）

④ Create GitHub Release
  ├── 所有二进制文件
  ├── checksums.txt
  └── changelog 摘要
```

---

### 4.4 完整 Release 流程

```bash
# Step 1: 准备 Checklist
git checkout main
git pull origin main

确认以下全部满足:
✅ 全部 CI gates 绿（本地 + CI 各跑一遍）
✅ 目标 stage 的所有任务标 done
✅ docs/stages/S<N>-report.md 写了（偏差如实记录）
✅ TASKS.md 已同步
✅ CHANGELOG.md 有条目

# Step 2: 打 Tag 推送
git tag v0.1.0              # 正式 release
git push origin main
git push origin v0.1.0      # ← 触发 release.yml

# Step 3: Actions 自动执行（见上节）
# 你只需要打开 GitHub → Actions 页面看结果
```

### RC vs Final — 什么时候需要 RC？

| 类型 | 用途 | 什么时候需要 |
|------|------|------------|
| RC (`v0.1.0-rc1`) | 让社区验证新版本 | 有活跃用户基线，怕破坏旧版本 |
| Final (`v0.1.0`) | 正式发布 | **MVP 阶段通常不需要 RC**（没人跑旧版） |

---

### 4.5 密钥管理

```text
GitHub Secrets (Settings → Secrets and variables → Actions):
├── SEPOLIA_RPC_URL   → 链上部署 RPC
├── DEPLOY_KEY        → 部署地址私钥
├── ETHERSCAN_API_KEY → 合约验证
└── NPM_TOKEN         → npm publish

绝对禁止：
❌ 私钥出现在任何代码中
❌ 私钥出现在任何提交记录中
❌ 私钥以明文形式出现在命令参数中
```

---

### 4.6 故障恢复

**发布失败：**

```bash
git tag -d v0.1.1
git push origin :refs/tags/v0.1.1
git tag v0.1.1
git push origin v0.1.1
```

**安全事件：**

```
hotfix 分支从 main 切出
  → 修 bug + 加回归测试
  → CI 全绿
  → 紧急 release: v0.1.1 (patch bump)
  → SECURITY.md 披露渠道通知社区
  → 事后写 post-mortem (docs/notes/post-mortem-YYYY-MM-DD.md)
```

---

## 🔗 四个阶段的衔接关系

```
                        Planning（伞文档）
                         docs/notes/planning.md
                            │ 定案批准后
                            ▼
                        Tasks（主表）
                         TASKS.md
                            │ 逐个任务完成
                            ▼
                        Action（写代码）
                         附证据 + 写 report
                            │
              ┌─────────────┴─────────────┐
              ▼                           ▼
         CI Gate (每次 push)         Release (打 tag)
         ci.yml                      release.yml
              │                           │
              └──────────→ 社区拿到版本 ←─┘
```

---

## 📝 快速开始 Checklist（复制去其他项目用）

```bash
# ===== Phase 1: 初始化六件套 =====
touch MVP.md           # L0 范围定义
touch TASKS.md         # L1 任务源
touch HANDOFF.md       # 交接说明
touch CODING_RULES.md  # 编码规范
touch AGENTS.md        # AI agent 契约
touch DOCS.md          # 文档地图 + 权威层级

# ===== Phase 2: DevOps 框架 =====
mkdir -p docs/notes     # Planning 伞文档目录
touch DevOps.md         # CI/Release/npm 指南
touch CHANGELOG.md      # Keep a Changelog 格式
touch SECURITY.md       # 漏洞报告 + 密码学惯例
mkdir scripts
touch scripts/ci.sh     # 本地门禁脚本
mkdir .github/workflows
touch ci.yml            # GitHub Actions CI
touch release.yml       # Release 流水线

# ===== Phase 3: Git + GitHub =====
git init && git add .
git commit -m "feat: initial project scaffold"
gh repo create owner/repo --public
git remote add origin https://github.com/owner/repo.git
git push -u origin main

# ===== Phase 4: First Release =====
# 配置 GitHub Secrets 后：
git tag v0.1.0
git push origin --tags
# → GitHub Actions 自动跑完整流水线
```

---

## 🔧 维护约定

| 事件 | 必须更新 | 顺序 |
|------|---------|------|
| **MVP 范围变化** | `MVP.md` → `TASKS.md` → `HANDOFF.md` | **严格按此序** |
| **完成任务** | `TASKS.md`（标 done + 证据） | — |
| **Stage 完成** | `docs/stages/S<N>-report.md` + `TASKS.md` | — |
| **锁定决策变化** | `MVP.md` §2 → `HANDOFF.md` §4 | — |
| **新增文档** | `DOCS.md` §2 | — |
| **发生安全事件** | `SECURITY.md` + `docs/notes/post-mortem-*.md` | — |

**🚫 最危险的反模式：只改 TASKS.md 不改 MVP.md。** 这会造成范围漂移，两份文档失去单一事实来源。
