# RelayFirst — DOCS.md（文档索引）

> **本文件是 RelayFirst 所有文档的地图与权威层级定义。**
> **新增任何文档前，先读本文件。** 放错位置或搞错权威层级，比不写文档更糟。
>
> 状态：**S1–S13 代码已实现**（已是 git repo，branch `main`）。

---

## 1. 权威层级（冲突时按此裁决）

**这是本文件最重要的一张表。** 当两份文档说法不一致时，按此顺序裁决：

| 层级 | 文档 | 权威范围 | 可否偏离 |
|---|---|---|---|
| **L0** | [`MVP.md`](MVP.md) | **MVP 范围、技术选型、验收判据** | ❌ **不可偏离** |
| **L1** | [`TASKS.md`](TASKS.md) | **当前任务与状态** | ⚠️ 可调整任务顺序，不可改范围 |
| **L2** | [`HANDOFF.md`](HANDOFF.md) | 交接上下文、反模式 | ⚠️ 可补充，不可放宽约束 |
| **L3** | [`CODING_RULES.md`](CODING_RULES.md) | 编码规范 | ⚠️ 可增补，不可违反 L0 的选型 |
| **L4** | [`ARCHITECTURE.md`](ARCHITECTURE.md) | **长期路线图** | ✅ **可忽略** —— 见下方警告 |
| **L5** | `AGENTS.md` | Agent 操作契约 | ⚠️ 元层，约束"怎么做" |

### ⚠️ 关于 ARCHITECTURE.md 的最重要警告

```
ARCHITECTURE.md = 北极星 / 路线图   →  不照着实现
MVP.md          = 现在要造的东西     →  唯一有工期
```

**`ARCHITECTURE.md` 是 L4 —— 它不约束 MVP 实现。** 若有人拿它要求你实现 delegation / 结算 / escrow / E2EE，**指向本表拒绝**。

**唯一从 ARCHITECTURE.md 取用的是**：`RelayEnvelope` / `Receipt` 的签名与 hash 数据结构形状（见 `MVP.md` 附录）。

### 冲突裁决流程

```
发现两份文档矛盾
  ↓
以 L0（MVP.md）为准
  ↓
改掉低层级文档，使其与 L0 一致
  ↓
若矛盾涉及"范围"，先改 MVP.md，再同步 TASKS.md
```

---

## 2. 现有文档清单

| 文件 | 行数 | 层级 | 作用 | 何时更新 |
|---|---:|---|---|---|
| [`ARCHITECTURE.md`](ARCHITECTURE.md) | 2005 | L4 | 长期路线图（permissionless A2A 网络、RFN 标准族、结算层） | 仅当长期方向变化。**MVP 期间不动** |
| [`MVP.md`](MVP.md) | 1100 | **L0** | **MVP v2.0 范围**：A2A 本体 + 挖矿引流、回执、验证模式、SBT 积分、节点、选型、十条判据 | **范围变化时** —— 且必须先于 TASKS.md |
| [`TASKS.md`](TASKS.md) | 456 | L1 | 唯一任务源（S1–S8 + 阻塞项） | 每完成一项 / 新增任务 |
| [`HANDOFF.md`](HANDOFF.md) | 319 | L2 | 交接上下文、反模式、上手路径 | 锁定决策变化 / 新增反模式 |
| [`CODING_RULES.md`](CODING_RULES.md) | — | L3 | Go / TS 编码规范 | 引入新语言或新约定时 |
| [`AGENTS.md`](AGENTS.md) | — | L5 | AI agent 操作契约 | agent 行为出现系统性问题时 |
| [`DOCS.md`](DOCS.md) | — | 元 | 本文件（文档地图） | 新增/删除文档时 |
| [`README.md`](README.md) | — | **门面** | 一句话定位 + 10 分钟上手 + 不变式 + 命令表 + **诚实边界** | 上手路径 / 命令变化时 |
| [`docs/requirements.md`](docs/requirements.md) | — | **派生** | **需求汇总索引**（可追溯；权威仍在 `MVP.md`） | 需求变化或阶段推进时 |
| [`docs/getting-started.md`](docs/getting-started.md) | — | **指南** | 从零到首次出分；**明确说明密钥为何由用户自备** | 上手路径变化时 |
| [`docs/notes/inference-cost-gap.md`](docs/notes/inference-cost-gap.md) | — | **笔记** | 调研：挖矿不消耗推理额度的断层 | 已被 ADR-0002 修复，保留为决策依据 |
| [`docs/notes/epoch-anchoring.md`](docs/notes/epoch-anchoring.md) | — | **笔记** | **epoch 缺起点导致发行量恒为零**（已修复；起点值待定稿 → BLK-4） | 起点定稿后 |
| [`docs/notes/sqlite-write-path.md`](docs/notes/sqlite-write-path.md) | — | **笔记** | SQLite 写路径：Phase 1 度量（基线 + 第 14 道门禁）；含对 A6 因果的修正线索 | 进行中（Phase 2 未开工） |
| [`docs/notes/s9-0-security-review.md`](docs/notes/s9-0-security-review.md) | 200 | **安全审查** | **S9-0 版本化地基的对抗审查**：4 个发布前阻断项（B1–B4）+ H1–H2 + M1–M3 + L1–L3，含攻击路径与修法 | 阻断项修复后 |
| [`docs/notes/upgrade-architecture-plan.md`](docs/notes/upgrade-architecture-plan.md) | 651 | **笔记** | **MVP 2.0 → 全架构升级方案**：反分叉版本化（A9 论证）、升级顺序、库采用表、风险登记册 | 版本化地基（S9-0）落地后 |
| [`docs/notes/blk-2-first-consumer-plan.md`](docs/notes/blk-2-first-consumer-plan.md) | 200 | **行动方案** | **BLK-2：找第一个真实消费方**（价格/可达性 → 交易机器人）—— 买家画像 / demo 形态 / 首封信 / 反自我欺骗判据 / 8 周时间盒 | 拿到第一个使用者后 |
| [`docs/notes/emission-model-conflict.md`](docs/notes/emission-model-conflict.md) | — | **笔记** | **计分公式（§5.3）与 epoch 发行模型（§6.2）互相矛盾**——需人裁决 | 用户裁决后 |
| [`docs/notes/delegation-quickstart.md`](docs/notes/delegation-quickstart.md) | — | **指南** | **Session Delegation 操作指南**：`session grant`（owner 签发）/ `session verify`（消费方授权）/ nonce 撤销 / 常见错误。**回执永不可委派**是硬边界 | 委托命令 / scope 集合变化时 |
| [`docs/notes/mcp-setup.md`](docs/notes/mcp-setup.md) | — | **指南** | **MCP 接入指南（S12-7）**：Cursor / Claude Desktop 一行配置、5 个工具、`RELAYFIRST_RELAY`、"结构上不能签名"的导入图证明、诚实边界 | MCP 工具集 / 配置格式变化时 |
| [`docs/notes/blk-3-assignment-policy.md`](docs/notes/blk-3-assignment-policy.md) | — | **决策包** | **BLK-3 裁决包**：验证者指派策略的 A/B/C/D 候选 + 取舍 + 推荐；机制已就位、零改动 | 策略选定后 |
| [`docs/notes/blk-4-genesis-decision.md`](docs/notes/blk-4-genesis-decision.md) | — | **决策包** | **BLK-4 裁决包**：epoch 起点候选 + 量化影响表（不可逆，在签名载荷内）；改动 = 一行常量 | 起点日期选定后 |
| [`docs/notes/remaining-actions.md`](docs/notes/remaining-actions.md) | — | **索引** | **剩余动作清单**：分【你裁决】/【外部人工】/【发布动作】三类，写明谁做 | 每次推进后 |
| [`docs/notes/decentralization-gap.md`](docs/notes/decentralization-gap.md) | — | **笔记** | **去中心化现状 vs 目标**（目标 = `ARCHITECTURE.md` §1.6 Nostr 式 permissionless）；NET-1..4 已做对照 + 差距（发现层 L2/L3、联邦、反滥用、复制）；含范围声明 | 发现层推进后 |
| [`docs/mvp-2.0-proposal.md`](docs/mvp-2.0-proposal.md) | 611 | **决策依据** | **MVP 2.0 提案（已 accepted）** —— 论证过程；范围权威在 `MVP.md` v2.0 | 已归档（合并完成） |
| [`docs/archive/mvp-1.0.md`](docs/archive/mvp-1.0.md) | 884 | **归档** | **MVP 1.0 原文逐字副本**（SHA-256 `259469ad…`）—— 仓库无 git 历史，故显式归档 | 不再更新 |
| [`docs/decisions/ADR-0001-*.md`](docs/decisions/ADR-0001-eip712-in-house-thin-layer.md) | — | **决策** | EIP-712 用自研薄层而非 `apitypes`（S1-4） | 已 accepted |
| [`docs/decisions/ADR-0002-*.md`](docs/decisions/ADR-0002-semantic-extract-as-inference-cost.md) | — | **决策** | 语义化 extract 作为推理成本载体 | 已 accepted |
| [`docs/decisions/ADR-0003-*.md`](docs/decisions/ADR-0003-dedup-ledger-scope.md) | — | **决策** | 去重账本"全局"的作用域（A6 vs NET-2）—— 采纳"域内全局" | 已 accepted |
| [`docs/decisions/ADR-0004-*.md`](docs/decisions/ADR-0004-node-identity-crypto.md) | — | **决策** | 节点身份引入密码学 vs「节点不能验签」—— 采纳分二进制 | 已 accepted |
| [`docs/decisions/ADR-0005-*.md`](docs/decisions/ADR-0005-cli-framework-handwritten.md) | — | **决策** | CLI 框架实测为手写，修正 §8.1 选型表（含重新评估触发条件） | 已 accepted |
| [`docs/decisions/ADR-0006-*.md`](docs/decisions/ADR-0006-vendor-openzeppelin.md) | — | **决策** | 引入 OpenZeppelin v5.7.0（vendored）作为第一个 Solidity 依赖 | 已 accepted |
| [`docs/stages/S1-report.md`](docs/stages/S1-report.md) | — | **报告** | S1（回执 + 签名 + KAT + verifier）验收证据 | 已归档 |
| [`docs/stages/S2-report.md`](docs/stages/S2-report.md) | — | **报告** | S2（executor + 生成器 + 主循环）验收证据 | 已归档 |
| [`docs/stages/S2b-report.md`](docs/stages/S2b-report.md) | 172 | **报告** | S2 补完（S2-6/7/8/9）验收证据 | 已归档 |
| [`docs/stages/S3-report.md`](docs/stages/S3-report.md) | — | **报告** | S3（去重账本 + 计分 + epoch）验收证据 | 已归档 |
| [`docs/stages/S3b-report.md`](docs/stages/S3b-report.md) | — | **报告** | S3b（**挖矿→计分闭环接线** + epoch 锚点修复） | 已归档 |
| [`docs/stages/S5-report.md`](docs/stages/S5-report.md) | — | **报告** | S5（**薄节点**：收件/拉取/去重/well-known/Docker/多 relay 扇出） | 已归档 |
| [`docs/stages/S6-report.md`](docs/stages/S6-report.md) | — | **报告** | S6（CLI 上手：config/status/export/实时反馈/npx）—— **部分完成** | 已归档 |
| [`docs/stages/S7-report.md`](docs/stages/S7-report.md) | — | **报告** | S7（**链上锚定**：Merkle + Solidity + 跨语言语料）—— **代码层完成，链上未部署** | 已归档 |
| [`docs/stages/S4-report.md`](docs/stages/S4-report.md) | — | **报告** | S4（**对抗验证机制**：重执行 + anchor 重取 + 指派接口 + 承诺记录 + S4-0 接线）—— **BLK-3 仍开放** | 已归档 |
| [`docs/stages/S8-report.md`](docs/stages/S8-report.md) | — | **报告** | S8（**红队**：四项伪造攻击对真实栈全部 0 分 + 门禁状态） | 已归档 |
| [`docs/stages/S9-report.md`](docs/stages/S9-report.md) | — | **报告** | S9-0（**版本化地基**：10 项发布前阻断项全关 + 12 道门禁）—— **S9-1 未开工** | 已归档 |

---

## 3. 阅读顺序

### 3.1 人类开发者

```text
HANDOFF.md          ← 先读，建立上下文
  ↓
MVP.md §1–§5, §8    ← 范围 + 回执 + 机制 + 选型
  ↓
TASKS.md §0–§2      ← 开工前必读 + 阻塞项 + S1
  ↓
CODING_RULES.md     ← 动代码前
  ↓
（按需）ARCHITECTURE.md  ← 只读 §4 的数据结构形状
```

### 3.2 AI Agent（强制）

见 [`AGENTS.md` §2](AGENTS.md)。**顺序不可跳。**

---

## 4. 命名与放置约定

### 4.1 根目录（唯一保留）

**只有以下文件可放根目录：**

```text
ARCHITECTURE.md     长期路线图
MVP.md              范围定义（L0）
TASKS.md            任务源
HANDOFF.md          交接
CODING_RULES.md     编码规范
AGENTS.md           agent 契约
DOCS.md             本文件
README.md           门面（可选，后期补）
```

**除上述之外，任何新文档不得放根目录。** 用 `docs/`。

### 4.2 目录结构

```text
RelayFirst/
├── ARCHITECTURE.md
├── MVP.md
├── TASKS.md
├── HANDOFF.md
├── CODING_RULES.md
├── AGENTS.md
├── DOCS.md
├── README.md                    （后期补）
├── .cursor/
│   └── rules/
│       └── relayfirst.mdc        ← 自动加载的薄指针
└── docs/
    ├── decisions/
    │   └── ADR-000N-<slug>.md   ← 决策记录
    ├── stages/
    │   └── S<N>-report.md        ← 阶段验收报告
    ├── requirements.md           ← 需求汇总索引（派生自 MVP.md）
    └── notes/
        └── <topic>.md            ← 调研、实验记录
```

### 4.3 命名规范

| 类型 | 格式 | 例 |
|---|---|---|
| 决策记录 | `ADR-<4位序号>-<kebab-slug>.md` | `ADR-0001-gruth-task-types.md` |
| 阶段报告 | `S<N>-report.md` | `S1-report.md` |
| 调研笔记 | `<kebab-topic>.md` | `probe-task-sources.md` |
| 任务 id | `<stage>-<序号>` | `S1-5`、`S3-8` |

**禁止：** 空格、中文文件名、`final` / `v2` / `new` / `old` 之类无语义后缀。

### 4.4 决策记录（ADR）何时写

**需要写 ADR 的情况：**

- 偏离 `MVP.md` 的某项选型（**必须先批准**）
- 选择 trade-off 且**不可逆**（如：EIP-712 用 `apitypes` 还是自研）
- 一个决定会影响**多个 stage**

**ADR 模板：**

```markdown
# ADR-000N: <决定>

- 状态：proposed | accepted | superseded by ADR-000M
- 日期：YYYY-MM-DD
- 影响层级：L0 / L3

## 背景
<为什么需要做这个决定>

## 决定
<选了什么>

## 备选与取舍
| 选项 | 优点 | 缺点 |

## 后果
<不可逆的部分；对 TASKS.md 的影响>

## 证据
<测试/命令/引用>
```

### 4.5 阶段报告何时写

**每个 stage（S1–S8）完成时写一份 `docs/stages/S<N>-report.md`**，内容：

```markdown
# S<N> 验收报告

## 交付
<文件/模块清单>

## 对应验收判据
<勾选 + 证据>

## 实测数字
<测试数、命令输出、哈希>

## 偏差与遗留
<与 TASKS.md 的差异，如实记录>

## 下一步阻塞
```

**"如实记录偏差"是硬要求** —— 隐藏偏差会让下一个接手的人踩同样的坑。

---

## 5. 更新触发（谁改什么）

| 事件 | 必须更新 | 顺序 |
|---|---|---|
| **MVP 范围变化** | `MVP.md` → `TASKS.md` → `HANDOFF.md` | **严格按此序** |
| 完成任务 | `TASKS.md`（标 `done` + 证据） | — |
| Stage 完成 | `docs/stages/S<N>-report.md` + `TASKS.md` | — |
| 锁定决策变化 | `MVP.md` §2 → `HANDOFF.md` §4 | — |
| 新增反模式 | `HANDOFF.md` §9 | — |
| 新增/删除文档 | `DOCS.md` §2 | — |
| 长期方向变化 | `ARCHITECTURE.md` | MVP 期间**不应发生** |

**⚠️ 反模式：只改 `TASKS.md` 不改 `MVP.md`。** 这会造成范围漂移，两份文档失去单一事实来源。

---

## 6. 单一事实来源（SSoT）对照

**每个概念只有一个权威位置。** 其他文档只能引用，不得复制正文。

| 概念 | 唯一权威位置 | 其他地方 |
|---|---|---|
| MVP 范围（做/不做） | `MVP.md` §3, §12 | 引用 |
| 回执结构 | `MVP.md` §4 | 引用 |
| 任务类型约束 | `MVP.md` §5.0 | 引用 |
| 抗女巫机制 | `MVP.md` §5.2, §5.5 | 引用 |
| 积分 / emission | `MVP.md` §6 | 引用 |
| 技术选型 | `MVP.md` §8 | `CODING_RULES.md` **展开**（不复制表） |
| 验收判据（七条） | `MVP.md` §11 | `TASKS.md` §9、`HANDOFF.md` §7 引用 |
| 任务与状态 | `TASKS.md` | — |
| 反模式 | `HANDOFF.md` §9 | — |
| 长期协议设计 | `ARCHITECTURE.md` | — |
| 文档地图 | `DOCS.md`（本文件） | — |

> **规则：若你要在两个地方写同样的内容，说明其中一处应该是引用。**

---

## 7. 版本与变更记录

**MVP 期间不打版本号。** 用文件头的日期/状态行代替。

**变更历史不写在文档内** —— 用 git（待 `git init` 后）。文档内只保留**当前状态**与**修正记录**（如 `ARCHITECTURE.md` §14 那种，因为它是设计推理，不属于 git 历史）。

---

## 8. 待补文档（Roadmap）

| 文档 | 何时创建 | 作用 |
|---|---|---|
| `CONTRIBUTING.md` | 有外部贡献者时 | 提交规范 |
| `LICENSE` | 开源前 | 许可 |

**不要提前创建这些。** 空文档比没有文档更糟。