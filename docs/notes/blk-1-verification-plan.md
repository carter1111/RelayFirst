# BLK-1 核实报告：订阅能否程序化驱动

- 日期：2026-10-04（核实完成 2026-10-07）
- 状态：✅ **done —— 结论 PARTIAL（一家 YES，一家 NO）**
- 目标（已达成）：书面 yes/no + 证据

---

## 0. 结论（一句话）

> **Codex（ChatGPT 订阅）可以**：官方**明确文档化**了在 CI/CD 用 ChatGPT 账号授权跑 `codex exec`。
> **Claude Code（Claude 订阅）不可以**：其 **Consumer ToS 明文禁止**用订阅做"自动化/非人类"访问，
> 程序化必须改用 **API key**。
> **→ 支持列表只写 yes 的（Codex）；Claude Code 走 BYO API key 路径。**

**这直接影响叙事**：`MVP.md §9.3` 的 thesis 是"**订阅是沉没成本，挖矿让它变现**"。
该 thesis **对 Codex 成立**（订阅即可挖），**对 Claude Code 不成立**（要么人工，要么另买 API 额度
= 正是 BLK-1 §1 说的"再买一份 API 额度来挖矿"）。**这不是工程能解的，是产品级输入。**

---

## 1. 核实矩阵（三个问题 × 两个主要订阅）

| 订阅 | (a) 技术路径 | (b) ToS 允许 | (c) 无人值守可靠 | 判定 |
|---|---|---|---|---|
| **Codex**（ChatGPT Plus/Pro） | ✅ `codex exec` 非交互 | ✅ **官方文档化** CI/CD 用 ChatGPT 账号授权 | 🟡 **需重新登录**（见 §4） | **YES**（带运维注意） |
| **Claude Code**（Claude Pro/Max） | ✅ `claude -p` 非交互 | ❌ **ToS 明文禁止**（§2） | — （b 已否，无需评） | **NO** |

---

## 2. Claude Code —— **NO**（ToS 原文，非推测）

### 2.1 禁止条款（引用原文）

Anthropic **Consumer Terms of Service**（适用于 Free/Pro/Max），"你不得以下列方式访问或使用服务"
第 7 项：

> **"Except when you are accessing our Services via an Anthropic API Key or where we otherwise
> explicitly permit it, to access the Services through automated or non-human means, whether
> through a bot, script, or otherwise."**

**逐字读**：**订阅（非 API key）下，用 bot/脚本/其他自动化访问 = 禁止。**

### 2.2 官方文档进一步收紧（引用原文）

Claude Code 的 `legal-and-compliance` 文档：

> **"Developers building products or services that interact with Claude's capabilities,
> including those using the Agent SDK, should use API key authentication through Claude
> Console or a supported cloud provider."**

> **"OAuth authentication is intended exclusively for purchasers of Claude Free, Pro, Max,
> Team, and Enterprise subscription plans and is designed to support ordinary use of
> Claude Code and other native Anthropic applications."**

即：**OAuth（订阅登录）是给"ordinary use"的；要构建产品/程序化，用 API key。**

### 2.3 社区证据（旁证，非权威但一致）

`anthropics/claude-code` issue **#36324**（标题即 "[DOCS] headless mode documentation does
not warn **it should not be used from scripts when using a subscription account**"）：

> "This documentation does not warn the user that **if they are using a subscription account
> they can be banned for using headless mode.**"
> "The policy change to not allow scripted usage of claude from subscription accounts is new
> and was poorly communicated."

**技术能跑 ≠ 允许**。`claude -p` 存在，但**用它跑订阅 = 违反 ToS，可能封号**。

### 2.4 除非……

唯一合规路径：**用 Anthropic API key**（`ANTHROPIC_API_KEY`）。
但那就**不是**"变现沉没的订阅"，而是**"再买一份按量计费的 API"** ——
正是 BLK-1 §1 判定的 **NO 场景**（经济账算不过来）。

---

## 3. Codex —— **YES**（官方文档化，非推测）

### 3.1 技术路径（引用）

`developers.openai.com/codex/noninteractive`：

> **"Non-interactive mode lets you run Codex from scripts (for example, CI jobs) without
> opening the interactive TUI. You invoke it with `codex exec`."**

### 3.2 ToS：**官方主动支持订阅在自动化中用**（关键差异）

同一文档有专门一节：

> **"Use ChatGPT-managed auth in CI/CD (advanced)**
> Read this if you need to run CI/CD jobs with a **Codex user account instead of an API key**,
> such as enterprise teams using **ChatGPT-managed Codex access on trusted runners** or users
> who need **ChatGPT/Codex rate limits instead of API key usage**."

**这与 Anthropic 相反**：OpenAI **明确文档化**了"用 ChatGPT 账号（订阅）跑自动化"，
只提醒"API key 是自动化的默认……只有确实需要时走这条路"。

→ **订阅 + 程序化（`codex exec`）= OpenAI 支持。** 挖矿 thesis 对 Codex 成立。

### 3.3 授权细节

- 非交互自动复用已保存的 CLI 登录（`~/.codex/auth.json`，内含 access token）。
- `CODEX_API_KEY` **仅在 `codex exec` 支持**（无需交互登录的替代）。

---

## 4. (c) 无人值守可靠性 —— 只在 Codex 上需要评

**已知问题**：`openai/codex` issue **#3820** 的评论：

> "The issue here is not how you get authenticated. It's that **you have to log in again after a
> while, breaking automation.** Codex is unusable in CI/CD pipeline on a subscription."

**影响**：**token 会过期需重登** → 长时间无人值守会中断。
**缓解（未做，属实现）**：用 `CODEX_API_KEY`（若可用）或**检测失效并重新登录的守护逻辑**；
`~/.codex/auth.json` 视同密码保管，不进仓库。

**输出可解析**：`codex exec` 面向脚本，输出可结构化 —— 满足"机器可解析"要求。

---

## 5. 对 MVP 的影响（产品级，需拍板）

| 项 | 结论 |
|---|---|
| **支持列表** | **只写 Codex**；文档**明确标注** Claude Code 订阅不支持程序化（ToS） |
| **Claude Code 路径** | **BYO API key**（`MVP.md` 已规划的缓解），**但明确代价**：那是**新买 API 额度**，非"变现订阅" |
| **叙事** | "订阅变现"thesis **对 Codex 成立**。**是否继续把 Claude Code 作为目标订阅，是产品决策** |
| **R1 风险** | 从"未知的生死项"→ **"已知且部分成立"**：主路径有（Codex），次路径需 API key |

**⚠️ 不做**：本任务**不写任何生产代码**，不改任何模块（BLK-1 §5 要求）。

---

## 6. 证据来源

```text
Anthropic Consumer ToS（禁止自动化访问条款）
  https://www.anthropic.com/legal/consumer-terms  （"automated or non-human means … bot, script"）
Claude Code legal-and-compliance（开发者应使用 API key）
  https://code.claude.com/docs/en/legal-and-compliance
Claude Code headless（-p 存在，但订阅下受限）
  https://code.claude.com/docs/en/headless
anthropics/claude-code #36324（订阅 headless 可能封号）
  https://github.com/anthropics/claude-code/issues/36324
Codex 非交互模式 + ChatGPT-managed auth in CI/CD（官方支持）
  https://developers.openai.com/codex/noninteractive
openai/codex #3820（订阅在 CI 需重登）
  https://github.com/openai/codex/issues/3820
```

**方法说明（诚实）**：这是**桌面调研**（条款原文 + 官方文档 + 社区 issue），
**未做实机探针**（无订阅账号）。技术路径 (a) 依据官方文档；ToS (b) 依据条款原文；
可靠性 (c) 依据社区 issue。**ToS 部分引用原文，未推测。**
