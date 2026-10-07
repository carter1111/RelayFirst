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
>
> **⚠️ 分两栏**：**① 未定案**才真属于 Planning（P1）；**② 已裁决/已实现**是**参考** ——
> 保留在这里只为"一眼看全"，**它们不再是待定规划**。

### 0A. 未开工 / 待定（**真正的 Planning 待办**）

| Feature 文档 | 一句话 | 对应问题 |
|---|---|---|
| [`discovery-plan.md`](discovery-plan.md) | **发现层 G2→G1→G3** 实施计划 | 去中心化 / Explorer |
| [`explorer-indexer-plan.md`](explorer-indexer-plan.md) | Explorer 架构与红线 | Explorer |
| [`node-incentives-discussion.md`](node-incentives-discussion.md) | 节点动机（BLK-5） | **已延期**（待"好的完整激励方案"）|
| [`../gtm/blk-2-first-consumer-plan.md`](../gtm/blk-2-first-consumer-plan.md) | BLK-2 首个消费方行动方案 | **技术侧完成、demo 可跑；接洽未开始（人的动作）** |
| [`impl-assessment.md`](impl-assessment.md) → [`impl-planning.md`](impl-planning.md) → [`impl-tasks.md`](impl-tasks.md) | **激励机制落地**：D1 现状评估 → P0/P1/BLOCKED 切分 → 可派任务 | ✅ **已进 `TASKS.md §14`**（IMP-1..5/TEST-1）；**DOC-1(iii)+PRE-1(a) 已裁决**；**E 已裁决（两 root）**；剩 L0-1/L0-2 文档同步 |

### 0B. 已裁决 / 已实现（**参考**，非待定 —— 不要当成待办）

| 文档 | 状态 | 备注 |
|---|---|---|
| [`terminal-experience-plan.md`](terminal-experience-plan.md) | ✅ **已实现** | 节点 logo/banner/`report`/`status`/`inspect`（UX-1 节点部分） |
| [`node-tui-dashboard-plan.md`](node-tui-dashboard-plan.md) | ✅ **已实现** | `relayfirst-dashboard`（D1–D6 全交付） |
| [`multi-node-plan.md`](multi-node-plan.md) | ✅ **MN-1..3 已实现** | 去重 + 封闭性 + N=1..8 属性化测试 |
| [`npm-packaging-plan.md`](npm-packaging-plan.md) | ✅ **PKG-1 已实现**（PKG-2 决定不做） | 主包 8kB + 每平台子包 |
| [`cli-role-and-ux-plan.md`](cli-role-and-ux-plan.md) | ✅ **UX1b 已实现** | 首屏 / status / mine 品牌层 |
| [`blk-3-assignment-policy.md`](blk-3-assignment-policy.md) | **已裁决 A**（确定性种子） | 剩部署配置，机制零改动 |
| [`blk-4-genesis-decision.md`](blk-4-genesis-decision.md) | **已裁决 A**（=发布日） | 仅缺"具体哪天" |
| [`blk-1-verification-plan.md`](blk-1-verification-plan.md) | ✅ **已核实**（PARTIAL） | Codex 可行 / Claude Code 不可 |
| [`mcp-setup.md`](mcp-setup.md) | **已实现**（S12-5/6/7） | 属**how-to 参考**，不是规划 |

### 0A2. **v2 延后（近期不做）**

| 文档 | 一句话 | 为何延后 |
|---|---|---|
| [`agent-signer-plan.md`](agent-signer-plan.md) + [`ADR-0009`](../decisions/ADR-0009-agent-key-management.md) | **Agent 密钥管理 + out-of-process signer + per-key domain 策略** | **不解决今天存在的威胁** —— LLM 是**远程 API**（读不到本地密钥），无本地 agent 子进程。本规格防的是**未来**"本地 agent 能调 CLI"场景。**Tier 2/3 全非必需**；Tier 1（key 出 env）也还要推翻 no-init |
| [`user-registration-flow.md`](user-registration-flow.md) + [`incentive.md`](incentive.md) | **用户注册/身份流程（单助记词双 key、keystore、signer、claim）+ 激励框架** | **是 ADR-0009 的下游** —— key 管理延后，注册流程随之延后。`incentive.md` 自述**未到开发标准**（参数待填）|
| **r / X / farm ROI 判据** | **经济数值** | 🗣️ **已裁决（2026-10-08）：留给「平台币设计」** —— r（兑换比例）不定，**X 与 farm ROI「够不够低」一并随之** |
| **强制纳入（omission）** | **争议层（dispute）** | 🗣️ **已裁决（2026-10-08）：归 Roadmap / 2.1** —— 它就是 dispute，而 dispute 是 A2 明文**非目标**（`AGENTS.md:61`）。**不反转 A2。** 「可发现」半边已做 |
| [`impl-planning.md`](impl-planning.md) §6 **Layer 0 设计** | **Layer 0（节点经济）的测量【输入源】选项**（verifier 签心跳 / 邻居观测 / 挑战式）—— Layer 0 卡在**设计**，不卡在代码 | **待决**：谁观测"合格 epoch"（见 §6.5 三问）；**farm ROI 建模 launch-blocking** |

### 0C. 参考 / 索引（**非任务**）

| 文档 | 作用 |
|---|---|
| [`mvp2-launch-readiness.md`](mvp2-launch-readiness.md) | **上线前一览**：十条判据 + 六问 |
| [`decentralization-gap.md`](decentralization-gap.md) | 去中心化现状 vs 目标（G1–G5 / O1–O4）—— **分析**，非待办 |

---

## A. 已裁决 → 待收尾（仍需一个输入 / 一次操作）

| 项 | 已裁决 | 决策包 | 还缺什么 |
|---|---|---|---|
| **BLK-4 genesis** | **✅ A：genesis = 公开发布日 00:00 UTC** | [`blk-4-genesis-decision.md`](blk-4-genesis-decision.md) | **仅缺"具体哪天"**（用户输入）→ 我改一行常量 + 跑回归 + 用 `-tags mainnet` 验防呆解除 |
| **BLK-3 指派策略** | **✅ A：确定性种子** | [`blk-3-assignment-policy.md`](blk-3-assignment-policy.md) | 部署配置（候选集 + seed 来源，属组织决定）；**机制零改动** |

> **两项方向都已裁决，不再是"待裁决"。** BLK-3 **机制上已关闭**（现参考实现即 A）；
> BLK-4 只等你给发布日。**待裁决的只剩 `BLK-5`（节点动机）** —— 见 B 与
> [`node-incentives-discussion.md`](node-incentives-discussion.md)。

### 已做的防呆（BLK-4）

`-tags mainnet` 构建在 genesis 仍为占位符时**启动即 panic**（`internal/epoch/release*.go`），
所以占位符**不可能被误带上线**。实测见 `blk-4-genesis-decision.md` §6。

---

### A2. ✅ **已裁决（2026-10-07）：D1 emission 模型 = B（固定预算按份额）**

> 此前**只在 `emission-model-conflict.md` 标记"需人裁决"，却未登记在这里** —— 是个**会被遗忘的开放决策**。现已裁决并登记。

**裁决：模型 B（固定预算按份额）。** 理由（用户 2026-10-07）：

```text
① "头矿"叙事只在【固定盘子】下成立：早期人少分得多 × 预算 decay = 双重 premium。
   模型 A（按回执印）总量无纪律，头矿要靠 decay 硬拗。
② 5% cap 只对着【预算】才有定义 —— "5% of what？" A 回答不了。
③ Bitcoin 形：固定增发时间表，叙事现成，不用解释。
④ 实现代价可控：epoch 机制（BLK-4）+ Merkle claim（S11）零件已齐，只差"epoch 末算一次份额"。
⑤ 不选 C：复杂度×2、解释成本×2；散户听不懂的经济模型等于没有。
```

**→ 后续影响**：`Allocate`/`CapAllocation`（`internal/scoring/emission.go`）**接线才有意义**。
**顺序：先定模型（已定），再接线 —— 不要反过来。**

**✅ 接线已完成（2026-10-07）**：
- **`scoring.Settle`**（纯函数，份额 + 5% cap）+ **`WorkLedger`/`WorkRecord`**（work 累积，**条件 1**）
- **`Emit` → `RecordWork`**（回执记 work，不再记 points）；`Verdict.Points` → `Verdict.Work`；删 `BudgetFactor`
- **`ScoringSink.Finalize`**（epoch 末结算，points entry id = `settle:<epoch>:<agent>` **幂等**）
- **`relayfirst settle`** 命令；**触发器定死为显式命令**（**条件 2**，见 [`settlement-trigger.md`](settlement-trigger.md)）
- **全链路测试**：work → Settle → points → Merkle root（**条件 3**，`TestSettlementChain_...`）

**⚠️ 这触及 L0**（`MVP.md` §5.3 / §6.2 / §6.3 的两套模型需统一）→ 按 `AGENTS.md §5.1`，**要先改 `MVP.md`**。
**接线本身是新的范围**（属 S3 的延伸），需先改 L0。

## B. 需要【外部环境 / 人工】的（我无法代做）

| 项 | 为什么我做不了 | 入口 |
|---|---|---|
| ~~BLK-1~~ 订阅可程序化驱动？ | ✅ **已核实**（桌面调研完成） | [`blk-1-verification-plan.md`](blk-1-verification-plan.md)。**未做实机探针**（无订阅账号）—— 若你有账号，可选做 |
| **BLK-2** 第一个真实消费方 | 需**人去接洽** | [`../gtm/blk-2-first-consumer-plan.md`](../gtm/blk-2-first-consumer-plan.md)（画像/首封信/8 周时间盒）。**技术侧已补**：demo 命令 `relayfirst observations` 现已实现 |
| **判据 ①** 陌生人 10 分钟出分 | 定义就是**真人计时** | `TASKS.md` S6-8 / S8-1 |
| **判据 ③** 陌生人一条命令起节点 | 代码层已过（Dockerfile 实测）；**镜像未发 + 计时未做** | 见 C |
| **判据 ⑨** SBT 钱包可见 | 需**链上部署** + 真实钱包 | `contracts/` + `docs/stages/S7-report.md` |
| **S12-3** 真实 USDC | 需**选链 + 测试币 + 部署** | `TASKS.md` S12-3 |
| **BLK-3** 候选集"谁在池里" | 需**组织决定**谁当验证者 | 见 A |
| **BLK-5** 节点动机 | 🗣️ **已延期** —— 用户裁决"没有很好的完整激励方式，先放 planning"。**技术结论：节点工作量不可验证** | [`node-incentives-discussion.md`](node-incentives-discussion.md) |

---

## C. 需要【发布动作】的（一次性命令，但要有凭据）

### C1. npm —— **发三个用户工具（主包 + 每平台子包），不发节点**（**打包已就绪，只剩 `npm publish`**）

> **⚠️ 发布顺序**：**先发 5 个 `@relayfirst/<os>-<arch>` 子包，再发主包** ——
> 主包的 `optionalDependencies` 指向的版本必须**先存在**，否则 `npx` 解析不到。见 [`npm-packaging-plan.md`](npm-packaging-plan.md) §4。

| 物 | 目的 | 现状 |
|---|---|---|
| `relayfirst` CLI | `npx relayfirst …` 零安装挖矿/验证 | ✅ **打包就绪**；⏳ 未 `npm publish` |
| `relayfirst-mcp` | IDE 一行配置（MCP stdio） | ✅ **打包就绪**；⏳ 未 `npm publish` |
| **`relayfirst-dashboard`** | `npx relayfirst-dashboard` 连节点看面板 | ✅ **打包就绪**（PKG-4）；⏳ 未 `npm publish` |
| ~~`relayfirst-node`~~ | **刻意不发 npm** | 它是**长驻服务**：npm/npx 适合按需启动的 CLI/MCP（stdio），不适合守护进程 + 持久卷。**节点用 Docker** |

**已修（2026-10-06）**：原 4 个阻塞项 —— `private:true`、`bin/` 被 gitignore、tarball 只有单平台单二进制、版本过期 —— 全部处理：

- `package.json`：去掉 `private`，版本 → `0.5.0`，加 `prepublishOnly` → `scripts/build-npm-binaries.sh`，`files` **移除** `bin/npm`（PKG-1：二进制改走子包）。
- `scripts/build-npm-binaries.sh`：**交叉编译 5 平台 × 3 工具**（linux/darwin × amd64/arm64 + windows/amd64）。
- `scripts/pack-platform-packages.sh`：打包 5 个 `@relayfirst/<os>-<arch>` 子包。
- 三个 launcher：优先选 **`bin/npm/<name>-<os>-<arch>`**，再 **子包**，再本地构建，再 `go build`。
- **CI 门禁**：`private`/`prepublishOnly`/`files` 三查 + 真跑交叉编译 + **主包无二进制** + **子包平台映射** + **launcher 解析子包**。
- **实测**：主包 **8.0kB**（原 42MB）、子包 11.3MB；`env -i PATH=<only node> … --version` 跑通。

**⚠️ 一个未决点（不阻塞发布）**：

| 点 | 说明 |
|---|---|
| **macOS 未签名** | ✅ **决定不做（PKG-2）** —— `npx` 路径**不需要** Apple 证书（arm64 Go 已 ad-hoc 签；Gatekeeper 只拦被浏览器打 quarantine 位的）。见 [`npm-packaging-plan.md`](npm-packaging-plan.md) §8 |

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
| **② 发现层 + Explorer** | 📋 **已出 solid plan**；**✅ 用户 2026-10-07 确认"要做" → 排期** | [`discovery-plan.md`](discovery-plan.md) + [`explorer-indexer-plan.md`](explorer-indexer-plan.md) | **要排工期：先改 `MVP.md`**（`AGENTS.md §5.1`） |
| **P1#2 quorum 接线** | ✅ **默认已定 = 1**（2026-10-07），**待接线** | [`npm-packaging-plan.md`](npm-packaging-plan.md) 无关；见 `internal/publish/policy.go` | 把 `PublishWithPolicy` 接进 `mine`/`sink`/`card_fetch`（默认 quorum=1，**行为不变**） |
| **③ npm + 节点体验** | npm 打包就绪；节点体验有设计 | [`terminal-experience-plan.md`](terminal-experience-plan.md) | npm：`npm publish`（PKG-3）；体验：UX-1 |
| **④ 节点激励** | 🗣️ **讨论**（BLK-5，待裁决） | [`node-incentives-discussion.md`](node-incentives-discussion.md) | 回答三问（Q1 无激励 / Q2 声誉榜 / Q3 运维费） |
| **⑤ 矿工 UX 优化** | 📋 有设计 | [`cli-role-and-ux-plan.md`](cli-role-and-ux-plan.md) | UX-1 开工时 |

**⑥ 上线前还缺** → 见 `mvp2-launch-readiness.md §6`（BLK-1/2/3/4 + 发布动作 + 真人动作）。

---

## D2. 节点/dashboard 的**配置方式**（✅ 已实现，2026-10-06）

> **问题（用户 2026-10-06）**：端口是不是应该让用户配置比较好？
> **结论**：**flag（已有）+ 环境变量（建议加）+ 【不加】config 文件。** 端口**不是写死**，是**默认值**。

### 现状（实测）

| 二进制 | 现在怎么配 |
|---|---|
| `relayfirst-node` | **只读 flag**（`--listen/--storage/--public-url/--max-payload`）；**无 env、无 config 文件** |
| `relayfirst-dashboard` | **只读 flag**（`--url/--interval`）；**无 env** |
| 项目其他工具 | **有** `RELAYFIRST_RELAY` / `RELAYFIRST_SESSION_KEY` / `RELAYFIRST_GRANT_NONCE` / `RELAYFIRST_OWNER_KEY` / `RELAYFIRST_VERIFIER_KEY` / `RELAYFIRST_PRIVATE_KEY` / `RELAYFIRST_CONFIG_DIR` |

→ **节点/dashboard 是当前唯一不读 env 的两个** —— 这本身是个**不一致**。

### 最优方案（三层）

| 层 | 做不做 | 内容 |
|---|---|---|
| **① flag** | ✅ **已有，保留** | `--listen` / `--url` —— **唯一正确的"一次性覆盖"**，也是 `docker run … --listen :9000` 的用法 |
| **② env** | ✅ **建议加** | `RELAYFIRST_LISTEN` / `RELAYFIRST_STORAGE`（node）；dashboard 的 `--url` 默认**先读 `RELAYFIRST_RELAY`**（**复用现有名字，零新概念**） |
| **③ config 文件** | ❌ **不加** | 见下 |

**优先级**（两个 `--help` 都要写明）：`flag > env > default`。

### 为什么**不加** config 文件（依据充分）

| 理由 | 依据 |
|---|---|
| **节点是服务器，服务器不该带状态文件** | 发钉死的副本（Docker/systemd/多实例）用一个文件极易配错 —— 用户刚被"旧进程 + 旧端口"坑过，**状态是 bug 来源** |
| **flag 是标准** | `docker run nginx -g …`、`redis-server --port` 都用**命令行**，不用文件 |
| **env 已覆盖"长驻/容器"** | `docker run -e RELAYFIRST_LISTEN=:9000` 比挂载 config **更干净** |
| **避免第二个真相源** | 现有 config 是**矿工 CLI** 的（`relayfirst config set`），**不是节点的**；混用会让"节点配置"失去单一来源（违反 `DOCS.md §6` SSoT 原则） |

### 若做（改动很小）

1. `cmd/relayfirst-node`：`--listen`/`--storage` 默认值改为**先读 env**（flag 优先）
2. `cmd/relayfirst-dashboard`：`--url` 默认值 `RELAYFIRST_RELAY` → `http://localhost:8080`
3. 两个 `--help` 增加"优先级 flag > env > default"一行 + 换端口/主机示例

**状态**：✅ **已实现（2026-10-06）** —— `cmd/relayfirst-node` 读 `RELAYFIRST_LISTEN`/`RELAYFIRST_STORAGE`，
`cmd/relayfirst-dashboard` 的 `--url` 默认读 `RELAYFIRST_RELAY`；**优先级 `flag > env > default`** 写进两个 `--help`；
blank env 视为未设（`envOr`）。**无 config 文件**（如上述理由）。测试：`TestParseFlags_EnvThenFlagPrecedence`（node）、`TestEnvOr`（dashboard）。

---

## D3. 第三方在 RelayFirst 上建 IM —— readiness 缺口（2026-10-07 记录，**只记录不写代码**）

> **背景结论（已定）**：**底座就绪，但第三方开发者上不来 —— 高速路通了，没修上下匝道。**
> 就绪的部分：permissionless relay · 通用 envelope · E2EE · relay-set。
> 缺失的是**让"外人"能上手的那层**。
>
> **纪律**：与 `TASKS.md §11.1`（GAP 表）和 [`discovery-plan.md`](discovery-plan.md) **交叉引用，不另起编号**。
> **粗估工作量，不承诺精确排期。**

| # | 缺口 | 一句话 | 为什么是 blocker | 依赖 | 粗估 |
|---|---|---|---|---|---|
| **IM-1** | **TS SDK**（最高优先级） | relay 客户端 + envelope 收发 + relay-set + E2EE 封装 | IM 开发者是 **TS 栈**；**无 SDK = 手搓协议** | 无（但最好在 dogfood 之后定型 API） | **~2–3 周** |
| **IM-2** | **GAP-G2** relay 查询过滤 | `since`/`until`/`kinds`/`authors`（Nostr `REQ` 对应物） | **功能性 blocker** —— 无增量同步，**IM 做不出来** | **先定 `author` 字段协议设计**（见 `messaging-engine-vision.md §3`） | 天级（设计+实现） |
| **IM-3** | **IM conventions spec v0** | Kind 命名注册表（message / 已读回执 / presence / typing）+ payload 格式 | **无约定则各家互不通** | 无 | 天级 |
| **IM-4** | **Builder 文档** | quickstart（10 分钟跑起 IM demo）+ API reference + 示例应用 | 现有文档**写给 core team**；第三方要 **on-ramp** 文档 | IM-1（API 定型后写才不返工） | 天级 |
| **IM-5** | **Relay 发现 / bootstrap** | 客户端首次"连哪个 relay"的答案（起步可**硬编码种子列表**） | 无它则第一个客户端不知连谁 | 无（种子列表起步） | 小时级 |

**IM-3 的硬原则**：**relay 不感知 conventions，纯客户端约定** —— 否则把语义塞回中继，违反"哑relay"/NET-1。

### 顺序（Phase）

```text
Phase A  JimAIM dogfood        自己跑顺、踩坑 —— 在把 API 固化前暴露问题
Phase B  TS SDK + GAP-G2       第三方的【硬前置】
Phase C  conventions v0 + builder 文档 + 发现
Phase D  正式邀请第三方         first impression 只有一次，【匝道修好再开门】
```

**关键纪律（Phase D）**：**匝道修好再开门** —— 提前邀请第三方，第一次印象就废了。

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
