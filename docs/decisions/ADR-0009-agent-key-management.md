# ADR-0009: Agent 密钥管理 —— out-of-process signer 与 per-key 签名策略（草案）

- 状态：**proposed（待你批准）** —— 本 ADR **推翻**一个既有决策（`relayfirst init` 的"deliberately not implemented"），
  按 `AGENTS.md §5.1` 精神，**推翻必须先论证、由人拍板**，不能默默覆盖。
- 日期：2026-10-07
- 影响层级：**L0**（触及 `MVP.md` §8 / §9.2；触及 ADR-0004 / 0007 / 0008）
- 来源：外部提案"Agent + Node wallet init and signing policy"

---

## 背景

两条既有约束**同时成立，看起来张力**：

- **ADR-0007**：回执必须由**干活者自己的密钥**签。（owner 的普通 EVM 身份）
- **本仓库的 threat model**：**LLM 绝不得看到私钥材料**（prompt injection 会读 env / 文件 / MCP 返回）。

现状：`relayfirst mine` 用 `RELAYFIRST_PRIVATE_KEY`，**密钥在 CLI 进程里**；
LLM 只用于 semantic extract（**不经过密钥**）。所以**今天密钥没进 LLM** —— 但如果把签名移进
agent 进程，就会进。

**外部提案的核心洞察是对的，且值得记下来：**

> **"LLM 看不到密钥，但【可以调用 signer】；盲签会让它用恶意 EIP-712 payload 外泄（如 Permit2）。"**

---

## 决定（proposed）

### D1. 两把**独立**密钥，且**不叫** "node key"

系统要**两把**身份密钥，**互相独立**（不同熵、不同地址）：

| 密钥 | 签什么 | 为什么独立 |
|---|---|---|
| **agent key** | 回执 · 事件 · 委托 grant · Agent Card | 干活者身份（ADR-0007） |
| **verifier key** | **只**签 `assertion-1`（可归因结论） | 与产出者分离（ADR-0004 的分角色） |

**⚠️ 没有 "node key"。** 节点**无密钥**是 ADR-0004 的**硬性质**（CI 门禁强制）。
提案里的 "node-verifier key" 是**错误的合并**：节点与验证者是**两个角色**（ADR-0004）。

### D2. `relayfirst init` **要实现**（推翻既有决策，理由如下）

**既有决策**（`README` / `getting-started` / CLI usage）：

> "`relayfirst init` is deliberately NOT implemented. **writing key material to disk is a security
> decision that must be made deliberately, not by an agent.**"

**为什么可以说这不再冲突**（**必须写清，否则就是无声覆盖**）：

- 原决策防的是**agent 悄悄替人落盘密钥**。本 ADR **不放开**这一点：
  ① 优先 **OS keychain**，不落自定义文件 ② **mnemonic 显示一次 + 强制人手动键入确认** ③ **幂等、拒绝覆盖**。
- **人机确认**是原决策缺的那一步；补上它，风险从"agent 代做安全决策"变为"人明确地做安全决策"。

**→ 这一条需要你明确批准**（它改的是 L0 边界）。

### D3. **out-of-process signer + per-key domain 策略**（本 ADR 的核心）

**signer 是独立进程**（扩展 `relayfirst-verifier` 或新建 `relayfirst-signer`），解锁后**在内存持密钥**，
只暴露**本机**签名 API。**policy = 每个密钥一个 EIP-712 domain 白名单**：

| 密钥 | 允许的 domain（version） |
|---|---|
| **agent** | `relayfirst:1`（receipt/card）· `event-1` · `delegation-1` |
| **verifier** | **只** `assertion-1` |

**拒绝**：原始 hash · 不在白名单的 domain · 结构不符的 EIP-712 payload。
**每次签都记日志**（时间 · domain · 签名者地址）。

**⚠️ 提案写的"只签 receipt schema"是错的**：会**破坏** `session open/close`(event-1)、
`session grant`(delegation-1)、`card publish`。**白名单必须覆盖 agent 的全部合法域**（上表）。

**为什么 policy 是第二道墙**（写进代码注释）：signer 挡住了"看到密钥"，
但**被注入的 agent 仍能调用 signer**；**盲签** = 让它用**它构造的** EIP-712 payload 外泄。
所以 signer 只签**它认得的确切 schema**。

### D4. MCP **保持零密码学**，通过 HTTP 调 signer（**不反转 ADR-0008**）

提案要 "MCP 暴露 `sign_receipt`"。**这与 ADR-0008（MCP 零依赖、CI 导入图门禁）直接冲突**。

**正确做法，且更强**：MCP **不**链接任何 crypto，只多一个**本地 HTTP 客户端**调 signer。
→ **"MCP 永不持密钥"仍然成立**，且"不可导出"是**结构性质**（MCP 里根本没有密钥可导）。

### D5. **认证**：localhost **不够**

**任何本地进程都能连 localhost**（包括被注入的 agent）。signer 需要
**bearer token**（0600 文件，init 时生成）或 **Unix socket 权限**。否则"本地" = **谁都能签**。

### D6. **BIP-39 走依赖，不手写**（A1）

仓库**无 BIP-39**。助记词与 HD 派生是**密码学** → **用库**（`tyler-smith/go-bip39` 或 `go-ethereum` 的 hd），
**绝不手写**（A1）。加依赖需记理由。

### D7. PoSR / `relayfirst bind` —— **机制已定（2026-10-07，替代"拆出"）**

原"拆出"理由（提案未定义 + "node 签接受"与"节点无密钥"冲突）已被新机制消解：
**沿用现有 domain，不改 signing policy** ——

- agent 侧以 **delegation-1** 签绑定意向（"我的 work 经由 verifier Y 提交"）；
- verifier 侧以 **assertion-1** 确认服务关系（"为 agent X 提供中继"）。

verifier 签 assertion-1 在其白名单内（D3）；"节点无密钥"不受影响（签的是 verifier key，不是 node key）。
delegation 的语义本就是"委托"，比硬塞进 assertion 更顺。
one-binding-per-identity，不叠加。attestation 具体格式与 bind UX 待设计（incentive.md §10.5）。

---

## 备选与取舍

| 选项 | 优点 | 缺点 |
|---|---|---|
| **A. out-of-process signer + per-key domain 白名单**（裁定） | LLM 结构上拿不到密钥；盲签被 policy 挡；与 ADR-0004/0007/0008 全兼容 | 新进程 + 运维（解锁、token） |
| B. 密钥留在 CLI 进程（现状） | 零改动 | 密钥与 agent **同机同进程边界**；一旦移入 agent 进程即失守 |
| C. 把 signer 并进 MCP | — | **反转 ADR-0008**，门禁 FAIL |
| D. 硬件/HSM | 最强 | **Non-goal（roadmap）** |
| E. `init` 不实现（保持现状） | 不改 L0 | 用户仍要**自备**密钥（`cast wallet`/MetaMask）；但**"两把独立密钥 + 防覆盖"没有工具支持**，易配错 |

**为什么 A 而非 B/E**：本项目**已经**有"分角色 + 结构强制"的先例（ADR-0004 拆 verifier）。
把它用到 agent 侧，是**同构而非新发明**。

---

## 后果

1. **`relayfirst init`** 需实现（**待批准** D2）；**幂等、拒绝覆盖**；`--force` 需交互确认 + 数据丢失警告。
2. **新 signer 进程**（或扩展 verifier）；**localhost + token**；**per-key domain 白名单**；**每次签记日志**。
3. **MCP** 只加 HTTP 客户端，**零 crypto 不变**（CI 门禁继续钉）。
4. **`mine` 接线**：agent 产 **content** → signer **验 schema → 签** → 提交。密钥**不进 agent/LLM 进程**。
5. **新增依赖**：BIP-39（D6）。
6. **PoSR/bind 机制**：已定（D7，2026-10-07）：agent delegation-1 意向 + verifier assertion-1 确认，不改 signing policy。UX/格式待设计。

**不可逆的部分**：`init` 生成的身份密钥**一旦丢失 = 身份丢失**（积分与回执归因绑在地址上）。
**loss semantics 必须写进 `init` 的输出与入门文档。**

---

## 证据（现状取证）

```text
密钥今天不进 LLM：provider 只用于 semantic extract；RELAYFIRST_PRIVATE_KEY 只在 CLI 进程
  cmd/relayfirst/main.go（keyFromFlags / runMine）；semantic 走 llm.NewFieldResolver（无 key 传递）
节点无密钥（CI 强制）：scripts/ci.sh 的 Import-graph separation（node 不得链接 eip712/receipt/…）
四个签名域：receipt "1" · event "event-1" · delegation "delegation-1" · assertion "assertion-1"（+ card "1"）
  grep 'DomainVersion' internal/*/*.go
MCP 零依赖（CI 强制）：scripts/ci.sh 的 mcp_forbidden（不得链接任何 relayfirst/internal）
既有 no-init 决策：README.md:66 · docs/getting-started.md:104 · cmd/relayfirst/main.go:71
```

**未决**：D2 的批准（推翻 no-init）。
