# Planning（规划伞文档）

> **这是所有【未定案】规划与决策的唯一入口。** 代码侧自闭环的任务已清空；剩下的都在这儿，
> 按"谁做"分类，并**向下链接到对应的 feature 文档**。
>
> **⚠️ 本文件与它下面的 feature 文档，都是【未到 TASKS 阶段】的规划。**
> 定案前的流程见 [`AGENTS.md` §5.0](../../AGENTS.md)：**Planning → TASKS → Action → 更新**。
> **未定案的东西不写进 `TASKS.md` 主表。**
>
> 更新日期：2026-10-06。**权威状态**在 `TASKS.md`；本文是**规划索引**，不复制状态。

---

## 0. feature 文档（本文是入口，它们是详情）

> **每个 feature = 一份文档**，挂在这里。定案后按 `AGENTS.md §5.0` 进 `TASKS.md`。

| Feature 文档 | 一句话 | 对应问题 |
|---|---|---|
| [`mvp2-launch-readiness.md`](mvp2-launch-readiness.md) | **上线前一览**：十条判据 + 六问 | 总览 |
| [`discovery-plan.md`](discovery-plan.md) | **发现层 G2→G1→G3** 实施计划（solid） | 去中心化 / Explorer |
| [`explorer-indexer-plan.md`](explorer-indexer-plan.md) | Explorer 架构与红线 | Explorer |
| [`decentralization-gap.md`](decentralization-gap.md) | 去中心化现状 vs 目标（G1–G5 / O1–O4） | 去中心化 |
| [`terminal-experience-plan.md`](terminal-experience-plan.md) | 节点 logo/banner/report 设计 | 节点体验 |
| [`cli-role-and-ux-plan.md`](cli-role-and-ux-plan.md) | `relayfirst` 定位与品牌层 | 矿工 UX |
| [`node-incentives-discussion.md`](node-incentives-discussion.md) | 节点动机（BLK-5）选项 | 为何安节点 |
| [`../gtm/blk-2-first-consumer-plan.md`](../gtm/blk-2-first-consumer-plan.md) | BLK-2 首个消费方行动方案 | 上线硬前置 |
| [`blk-3-assignment-policy.md`](blk-3-assignment-policy.md) | BLK-3 指派策略（已裁决 A） | 验证者指派 |
| [`blk-4-genesis-decision.md`](blk-4-genesis-decision.md) | BLK-4 genesis 日期（已裁决 A） | 发布日 |
| [`mcp-setup.md`](mcp-setup.md) | MCP 接入（已实现，属参考） | IDE 接入 |

---

## A. 需要你【裁决】的（我做不了，因为它是业务决策）

| 项 | 决定 | 决策包 | 还缺什么 |
|---|---|---|---|
| **BLK-4 genesis** | **✅ A：genesis = 公开发布日 00:00 UTC** | [`blk-4-genesis-decision.md`](blk-4-genesis-decision.md) | **仅缺"具体哪天"** → 我改一行常量 + 跑回归 + 用 `-tags mainnet` 验防呆解除 |
| **BLK-3 指派策略** | **✅ A：确定性种子** | [`blk-3-assignment-policy.md`](blk-3-assignment-policy.md) | 部署配置（候选集 + seed 来源）；**机制零改动** |

> **两项方向都已裁决。** BLK-3 **机制上已关闭**（现参考实现即 A）；BLK-4 只等你给发布日。

### 已做的防呆（BLK-4）

`-tags mainnet` 构建在 genesis 仍为占位符时**启动即 panic**（`internal/epoch/release*.go`），
所以占位符**不可能被误带上线**。实测见 `blk-4-genesis-decision.md` §6。

---

## B. 需要【外部环境 / 人工】的（我无法代做）

| 项 | 为什么我做不了 | 入口 |
|---|---|---|
| **BLK-1** 订阅可程序化驱动？ | 需真实账号/订阅核实 | `TASKS.md` §1 |
| **BLK-2** 第一个真实消费方 | 需**人去接洽** | [`../gtm/blk-2-first-consumer-plan.md`](../gtm/blk-2-first-consumer-plan.md)（画像/首封信/8 周时间盒） |
| **判据 ①** 陌生人 10 分钟出分 | 定义就是**真人计时** | `TASKS.md` S6-8 / S8-1 |
| **判据 ③** 陌生人一条命令起节点 | 代码层已过（Dockerfile 实测）；**镜像未发 + 计时未做** | 见 C |
| **判据 ⑨** SBT 钱包可见 | 需**链上部署** + 真实钱包 | `contracts/` + `docs/stages/S7-report.md` |
| **S12-3** 真实 USDC | 需**选链 + 测试币 + 部署** | `TASKS.md` S12-3 |
| **BLK-3** 候选集"谁在池里" | 需**组织决定**谁当验证者 | 见 A |

---

## C. 需要【发布动作】的（一次性命令，但要有凭据）

### C1. npm —— **只发两个用户工具，不发节点**（**打包已修好，只剩 `npm publish`**）

| 物 | 目的 | 现状 |
|---|---|---|
| `relayfirst` CLI | `npx relayfirst …` 零安装挖矿/验证 | ✅ **打包就绪**；⏳ 未 `npm publish` |
| `relayfirst-mcp` | IDE 一行配置（MCP stdio） | ✅ **打包就绪**；⏳ 未 `npm publish` |
| ~~`relayfirst-node`~~ | **刻意不发 npm** | 它是**长驻服务**：npm/npx 适合按需启动的 CLI/MCP（stdio），不适合守护进程 + 持久卷。**节点用 Docker** |

**已修（2026-10-06）**：原 4 个阻塞项 —— `private:true`、`bin/` 被 gitignore、tarball 只有单平台单二进制、版本过期 —— 全部处理：

- `package.json`：去掉 `private`，版本 → `0.5.0`，加 `prepublishOnly` → `scripts/build-npm-binaries.sh`，`files` 含 `bin/npm`。
- `scripts/build-npm-binaries.sh`：**交叉编译 5 平台 × 2 工具**（linux/darwin × amd64/arm64 + windows/amd64）。
- 两个 launcher：优先选 **`bin/npm/<name>-<os>-<arch>`**（按 `process.platform`/`arch`），
  找不到才回退 `go build`，并把**平台名**写进错误信息。
- **CI 新增门禁**：`private`/`prepublishOnly`/`files` 三查 + 真跑一次交叉编译 + 真跑 launcher。
- **实测**：`env -i PATH=<only node> node scripts/npx-relayfirst.mjs version` → 输出 `0.5.0-s6`
  （**PATH 里没有 `go`**，证明用的是预编译二进制，而非回退构建）。

**⚠️ 诚实的两个未决点（不阻塞发布，但要知情）**：

| 点 | 说明 |
|---|---|
| **tarball 体积** | 含 5 平台二进制 → **~42MB**（`npm pack --dry-run`）。若嫌大，可改**按平台分包**（`optionalDependencies` + `os`/`cpu`），或用 `postinstall` 下载单个平台。**未做** |
| **macOS 未签名** | 交叉编译出的 macOS 二进制**未签名/未公证** → Gatekeeper 可能拦截。首次发布建议先发 linux/windows，或补签名流程 |

> 相关代码：`scripts/npx-relayfirst.mjs` / `scripts/npx-relayfirst-mcp.mjs`（**启动器，不是重实现** ——
> 委托给 Go 二进制，避免 EIP-712 双实现漂移，不变量 A4）。
> 发布后，`docs/notes/mcp-setup.md` 里"本地二进制回退"那段可删。

### C2. Docker

| 物 | 到哪 | 现状 |
|---|---|---|
| `relayfirst/node` → registry | `docker push` | `Dockerfile` 就位、实测 build/run 成功，**未发布**（P1 #6） |

> **节点只有 Docker 这一条分发路径**，这是设计选择（见 C1 的 node 一行）。

---

## D. 规划/优化项（用户 2026-10-06 指示）

> 完整上线一览：[`mvp2-launch-readiness.md`](mvp2-launch-readiness.md)。

| 项 | 状态 | 文档 | 下一步 |
|---|---|---|---|
| **① 去中心化优化** | 已到 Nostr 底线；缺口 G1–G5 | [`decentralization-gap.md`](decentralization-gap.md) | 见 ②|
| **② 发现层 + Explorer** | 📋 **已出 solid plan** | [`discovery-plan.md`](discovery-plan.md) + [`explorer-indexer-plan.md`](explorer-indexer-plan.md) | **要排工期：先改 `MVP.md`**（`AGENTS.md §5.1`） |
| **③ npm + 节点体验** | npm 打包就绪；节点体验有设计 | [`terminal-experience-plan.md`](terminal-experience-plan.md) | npm：`npm publish`（PKG-3）；体验：UX-1 |
| **④ 节点激励** | 🗣️ **讨论**（BLK-5，待裁决） | [`node-incentives-discussion.md`](node-incentives-discussion.md) | 回答三问（Q1 无激励 / Q2 声誉榜 / Q3 运维费） |
| **⑤ 矿工 UX 优化** | 📋 有设计 | [`cli-role-and-ux-plan.md`](cli-role-and-ux-plan.md) | UX-1 开工时 |

**⑥ 上线前还缺** → 见 `mvp2-launch-readiness.md §6`（BLK-1/2/3/4 + 发布动作 + 真人动作）。

---

## E. 已闭环（本轮及近期完成，登记备查）

- **S13-3d** 委托签发者 + 消费者（`session grant` / `session verify`）
- **S12-7** MCP IDE 一行配置文档（[`mcp-setup.md`](mcp-setup.md)）
- **S10-6** 客户端独立复验（`assertion.Check`；此前 `TASKS.md` 有重复行，已修）
- **事件生产者** `relayfirst session open/close/show`
- **npm 打包就绪**（跨平台二进制 + CI 门禁；仅剩 `npm publish`）
- **发布构建路径**（`make release` + genesis 守卫 CI 门禁）
- **SQLite Phase 3**（按连接 `busy_timeout` + A6 并发测量）

---

## 一句话

**代码写完了；卡住的是"业务决策 + 外部接洽 + 发布动作"。**
A 类给我输入即可当轮闭环；B/C 类需要你或运营去做，我会在你推进时同步文档。
**D 类的规划文档已就绪**：②要开工先改 `MVP.md`；④要你回答三问。
