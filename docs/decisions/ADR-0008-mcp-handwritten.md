# ADR-0008: MCP server 手写（偏离"用官方 SDK"的默认）

- 状态：**accepted**（2026-10-06 记录）
- 日期：2026-10-06
- 影响层级：**L3**（技术选型；`MVP.md` §8.0 三分法的类别 ②）
- 来源：S13-4（MCP server 零密码学）实现；此前**未记录**为何不用官方 SDK

---

## 背景

`MVP.md` §8.0 对"编码 / 协议"类（类别 ②）的判据是"**优先用库，除非有具体理由**"。
MCP 是协议，所以**默认应当用官方 SDK**。

但 `cmd/relayfirst-mcp` 是**手写的 ~430 行标准库 JSON-RPC**，**零外部依赖**。
**此前没有任何文档说明为什么** —— 这正是本 ADR 要补的。**偏离不是问题，无记录的偏离才是。**

### 实测现状（`cmd/relayfirst-mcp/main.go`）

- 传输：**stdio**，newline-delimited JSON-RPC 2.0
- 方法：`initialize` · `tools/list` · `tools/call`
- 工具：5 个（`list_tasks` / `claim_task` / `list_agents` / `query_observations` / `relay_message`）
- **导入图：只有标准库**（`relayfirst/internal` **一个都没有**）—— 由 CI 门禁钉住

---

## 决定

**保持手写；把"为什么不用官方 SDK"写下来（本 ADR）。** 现在不迁移。

---

## 备选与取舍

| 选项 | 优点 | 缺点 |
|---|---|---|
| **A. 手写 stdlib（裁定）** | **零外部依赖**（依赖 = 攻击面）；覆盖实际用到的 surface；导入图门禁可证明"零密码学" | 协议演进要自己跟；非标准特性要自己加 |
| B. 用官方 Go MCP SDK | 全 spec conformance；sampling / elicitation / OAuth 现成 | **新增依赖树**；对"供应链偏执"的项目是**实打实的新攻击面**；我们**用不到**它多数的价值 |
| C. 手写 + 声明支持的协议版本 | == A，但要**显式声明**（当前自声明 `2025-06-18`） | — |

**为什么 A：** 这**不是偷懒，是 defensible 的 tradeoff**。

> **官方 SDK 的价值 —— 全 spec conformance、sampling、elicitation、OAuth —— 我们基本用不上。**
> 而**手写的零外部依赖**，对一个**节点不能验签、MCP 不能签名**这种**靠导入图证明安全**的项目，
> 是**真优点**：**每个依赖都是攻击面**，而 MCP 跑在 **agent 进程内**、最易被 prompt injection 触达。

**5 个工具的薄转发层**，手写 430 行 stdlib **够了**。

---

## 后果与重新评估触发条件

**现在**：不迁 SDK，不新增依赖。

**什么时候会变重要**（满足其一即**重估**）：

```text
1. MCP 工具从 5 个长到几十个 → 手写维护成本反超
2. 需要 sampling / elicitation / OAuth（官方 SDK 现成，手写要自造）
3. 客户端开始要求 2025-06-18 之后的新协议特性（我们自声明的版本会落后）
4. 外部贡献者进来（人家预期看到 SDK）
```

**与既有约束一致**：这一决定**不改变**"MCP 零密码学"（S13-4 的核心安全属性），
后者由 **CI 导入图门禁**强制，与本 ADR 无关。

---

## 证据

```text
cmd/relayfirst-mcp/main.go        —— 包注释已说明"零依赖/不持密钥"；本 ADR 补"为何不用 SDK"
scripts/ci.sh                     —— MCP 导入图门禁：不得含任何 relayfirst/internal 包
测试                              —— tools/list 无签名类工具；relay_message 原样转发 base64
docs/notes/mcp-setup.md           —— 接入指南
```
