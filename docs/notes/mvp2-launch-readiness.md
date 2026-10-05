# MVP 2.0 上线前 Readiness 一览

> **用途**：对外/对团队一页说清"现在到哪了、还差什么、还能优化什么"。
> **一句话结论**：软件基本完了（代码侧自闭环已清空）；差的是**网络好用层 + 真实消费方 + 发布动作 + 真人计时 + 一个发布日**。
>
> 更新：2026-10-06。权威状态在 `TASKS.md`；本文是**指标索引**，不复制状态。

---

## 0. 十条验收判据（`MVP.md §11`："任何一条不过，不上线"）

| 判据 | 状态 | 差什么 |
|---|---|---|
| ② 第三方离线验证回执 | ✅ | CI 门禁强制 |
| ④ 伪造工作量被拦（4 攻击 0 分） | ✅ | S8 红队 |
| ⑥ 跨语言 KAT | ✅ | 53 向量，CI 门禁 |
| ⑦ `CGO_ENABLED=0` | ✅ | CI 门禁 |
| ⑩ 节点不可信但能干 | ✅ | S10 + 导入图门禁 |
| ⑤ 对抗验证闭环 | 🟡 | 机制✅；**BLK-3 生产策略待配** |
| ⑧ A2A 闭环 + 官方 SDK 兼容 | 🟡 | 端点✅、官方 `a2a-go/v2` 原语✅；**与外部第三方 agent 互操作未实测** |
| ⑨ SBT 钱包可见 + 不可转让 | 🟡 | 合约/测试✅；**未上链、未真钱包渲染** |
| ① 陌生人 10 分钟出分 | ⬜ | **需真人计时** |
| ③ 陌生人一条命令起节点 | ⬜ | 代码✅（Dockerfile 实测）；**镜像未发布 + 计时未做** |

---

## 1. 去中心化：能到什么地步？（还有优化空间？）

**结论：核心架构已到 Nostr 底线。**

| Nostr 的关键做法 | RelayFirst | |
|---|---|---|
| 身份 = keypair | `agent:eip155:…` | ✅ |
| Relay 哑化 | 节点**结构上**链接不到验签代码 | ✅ 更强 |
| Relay list 用户自签 | relay-set 在 agent **自签的 card** | ✅ |
| 多 relay + 客户端容错 | quorum / 健康 / 有界 failover | ✅ |
| 有效性来自签名 | per-actor hash chain + 状态机 | ✅ |
| **怎么发现** | **带外知道 URL** | ⚠️ |

**关键事实**：**Nostr 自己也没有 P2P 发现 / gossip / 共识**。所以"像 Nostr"这个目标**已达成**。

**优化空间（= 缺口 G1–G5，属 `ARCHITECTURE.md` Phase 7，不在 MVP 范围）**：

| 缺口 | 优化后得到什么 | 计划 |
|---|---|---|
| **G2** relay 查询 filter（`since`/`until`/`kinds`） | 增量轮询；**Indexer/Explorer 的前置** | [`discovery-plan.md`](discovery-plan.md) |
| **G1** L2 Indexer 生态 | "能跑节点" → "别人找得到" | [`discovery-plan.md`](discovery-plan.md) |
| **G3** `cardHash` 上链（§17.3 **自述"可选"**） | indexer 全挂时的 fallback 发现 | 同上（Phase 3） |
| G4 联邦路由 / G5 复制 | 跨 operator 互操作 / 自愈 | Phase 7，未规划 |

**优化空间（非去中心化，是 operator 事务）**：反滥用（O1）、镜像（O2）、pruning（O3）、TLS（O4）——
**Nostr 同样不做**，属每个 operator 自决。

---

## 2. Explorer：上线时有吗？

**没有，且按当前范围不在 MVP 里。** 今天只有**每个节点各自的 JSON + 局部视角**。

设计：[`explorer-indexer-plan.md`](explorer-indexer-plan.md)。要点：

- **架构**：`Crawler → 独立 read model → 只读 API + UI`（**独立二进制/部署**）
- **红线**：`§17.2` `discovery = convenience`、`signature = truth` → **永不显示无出处的 `valid`**
- **能看**：节点列表 / agent 列表 / **observations 跨节点分组（`distinctAgents`）** / 任务板；每条链回**原始字节**
- **前置**：**G2（查询 filter）→ G1（crawler）**
- **要它上线即有 = 范围变化**（先改 `MVP.md`）

---

## 3. 节点安装与体验：简单吗？还能优化什么？

**安装极简**：一条 `docker run`，无注册/许可/密钥。**只有 Docker 分发**（长驻服务，不发 npm）。

**体验现状**：无 UI，slog 逐行日志，前台常驻。
**体验规划**：[`terminal-experience-plan.md`](terminal-experience-plan.md) —— TTY 下 logo + banner + `relayfirst-node report`（一次性、不占端口）。

**承重约束**：**headless-first**（`docker -d`/systemd/CI 无 TTY）→ 输出**按 TTY 分支**，**非 TTY 字节不变**、**无 TTY 不拒启**。

**还能优化**：
1. **镜像发布到 registry**（判据③ 的关键一步）→ 见 §6
2. **`report` 命令**（今天要起节点再 curl）
3. **banner**（TTY）
4. （**不建议**）交互 TUI —— 与 headless 冲突

---

## 4. 节点能挖矿吗？为什么要安节点？

**不能挖矿。节点是哑中继**：无密钥、不能签名、只存转。**挖矿在 `relayfirst` CLI**。

**为什么要安节点？**（诚实）

| 动机 | 成立 |
|---|---|
| 给自己：可控 inbox、不求人 | ✅ |
| 公共 relay（社区/开发者/基金会） | ✅ 像 Nostr |
| 当 Indexer/verifier 的底座 | ✅ |
| **拿经济激励** | ❌ **没有** —— 不变式 `A7` 禁止节点排放/质押奖励 |

**这是个真问题**：节点**没有直接回报**（同 Nostr，多为公共品）。
**讨论与方案**：[`node-incentives-discussion.md`](node-incentives-discussion.md)。

---

## 5. 矿工是谁？他们的 UX 是什么？能怎么优化？

**矿工 = 持有 agent/LLM 订阅的 crypto 用户**（`MVP.md §1.1`），是**雇佣兵**（要立刻知道拿到了什么）。
流程：agent 找活 → 可验证回执 → **不可转让积分**。

**UX 现状**：CLI + JSON（传统风格）。
**UX 优化（规划）**：[`cli-role-and-ux-plan.md`](cli-role-and-ux-plan.md)。**原则：协议层给 agent，UI 层给人**：

| 层 | 服务谁 | 不可动 |
|---|---|---|
| 协议层 | agent / 编排 | **JSON、退出码**（非 TTY 冻结） |
| UI 层 | 人 | 品牌、首启、仪表盘 |

**承重理由**：判据① 是"**陌生人** 10 分钟出分" —— **陌生人是人**，不能只给 agent 用。

**能怎么优化**：`relayfirst` 首屏 logo+三步起步；`mine` 首启 + **单行实时刷新**（非刷屏）；`status` 仪表盘。v0 **零新依赖**。

---

## 6. 上线前还缺什么？（汇总）

### A. 阻塞项
- **BLK-1 / BLK-2**：未解决。**BLK-2（真实消费方）是上线硬前置**，属运营
- **BLK-3**：方向已定（确定性种子），**候选集/seed 待配**
- **BLK-4**：方向已定（=发布日），**只缺"哪一天"**（`-tags mainnet` 防呆已就位）

### B. 发布动作（代码就绪，需凭据）
- **PKG-3** `npm publish`（打包已就绪）
- 节点镜像 → registry（判据③）

### C. 发布优化（未做）
- **PKG-1** npm 按平台分包（tarball ~42MB）
- **PKG-2** macOS 二进制签名/公证

### D. 真人动作
- 判据①③ **计时**；判据⑨ **链上部署 + 真钱包**

### E. 体验（未开工）
- **UX-1** 节点 banner/report + CLI 品牌层（设计就绪）

---

## 一句话

> **软件完了；卡住的是【业务决策 + 外部接洽 + 发布动作 + 真人计时】。
> 优化空间存在（发现层 G1/G2、体验 UX-1、npm 分包），但它们都要么属 Roadmap，
> 要么属发布优化 —— 都不该插队 BLK-2 之前。**
