# 修正版规格：Agent 密钥管理 + out-of-process signer

> **状态：⏸ 延后到 v2（2026-10-07 裁定）** —— **不是上线必需**，**未写代码**。
> 本文件是外部提案的**修正版**，逐条解决其七处问题；保留只为**未来**参考。

---

## ⚠️ 必要性裁定（先读这段，否则会把它当待办）

**实据：今天 LLM 是【远程 HTTPS API】，不是本地进程。**
（`internal/llm/openai.go` · `anthropic.go` = `http.NewRequestWithContext` POST 出去；
`mine` / CLI 里**无 `exec.Command`** → 没有本地 agent 子进程拿 `env`。）

→ **LLM 读不到本地内存/env，只看到 HTTP 请求体；密钥不在里面。**
→ 本规格防的是**未来**场景：**本地 agent 拿到工具权限、能调 CLI**。**今天不存在。**

| 层 | 内容 | 今天必需？ |
|---|---|---|
| **Tier 1** | 把 key 从 **env** 移进 0600 文件/keychain | ⚠️ 半必需（**通用**密钥卫生，与 LLM 无关；且**还要推翻 no-init**）|
| **Tier 2** | signer daemon + domain policy + mine 接线 + MCP HTTP + token | ❌ **未来**（仅当"本地 agent 挖矿"）|
| **Tier 3** | PoSR / bind | ❌ **不要**（已拆出）|

**裁定：整个延后到 v2。** 理由：为不存在的威胁写多日项目，还会牵出 daemon、依赖、运维复杂度。

---

> 相关：[`ADR-0009`](../decisions/ADR-0009-agent-key-management.md)（为什么这样做的决策）、
> ADR-0004（分角色）、ADR-0007（回执不可委派）、ADR-0008（MCP 零密码学）。

---

## 0. 与原提案的差异（一眼看全）

| # | 原提案 | 修正 | 为什么 |
|---|---|---|---|
| E1 | 直接实现 `defirst init` | **先论证推翻** no-init 决策（ADR-0009 D2），**你批准** | `README`/`getting-started` **明文**记录"刻意为不实现"；推翻须论证 |
| E2 | "node-verifier key" + "PoSR binding" | **无 node key**（ADR-0004）；两把 = **agent key + verifier key**；**PoSR/bind 拆出** | 节点**无密钥**是硬性质；PoSR 未定义 |
| E3 | "只签 receipt schema" | **per-key domain 白名单**（agent: receipt/event/delegation/card；verifier: 只 assertion） | 只签 receipt 会**破坏 session/card** |
| E4 | "MCP 暴露 `sign_receipt`" | **MCP 保持零 crypto，通过 HTTP 调 signer** | 与 ADR-0008 + 导入图门禁冲突；HTTP 客户端更强 |
| E5 | 测"key never in agent memory" | 测**结构化命题**（key 不进 LLM 输入 / MCP 无 crypto / 无 env-argv 泄露） | "内存里没有"**不可断言**（空过） |
| E6 | 假定有 BIP-39 | **显式加依赖**（不手写，A1） | 仓库无 BIP-39 |
| E7 | "localhost-only" | **+ bearer token（0600）/ Unix socket 权限** | 任何本地进程都能连 localhost |

---

## 1. 两把密钥（独立）

```text
agent key     签 receipt(relayfirst:1) · event(event-1) · delegation(delegation-1) · card(relayfirst:1)
verifier key  只签 assertion-1
```

**必须独立**：不同熵、不同地址。**理由 = 角色分离与归因**（ADR-0004/0007），**不是**某个 "PoSR binding"。

**⚠️ 没有 "node key"。** 节点进程**结构上无密钥**（CI 门禁强制）。

---

## 2. `relayfirst init`（**待 D2 批准**）

```text
① 生成两把独立 secp256k1 密钥（agent / verifier）
② 存储：优先 OS keychain；回退：密码加密 keystore 文件（0600）
③ 显示 BIP-39 mnemonic 一次 → 强制人【手动键入确认】→ 才完成
④ 幂等：已存在则拒绝；--force 需交互确认 + 数据丢失警告
⑤ 只打印 NodeID(节点地址) 与 agent 地址；【不打印任何密钥】
```

**loss semantics 写进输出**：**丢 key = 丢身份**（积分与回执归因绑地址）。

**依赖**：BIP-39 走库（`tyler-smith/go-bip39` 或 go-ethereum hd），**不手写**（A1/E6）。

---

## 3. Signer 进程（核心）

```text
扩展 relayfirst-verifier 或新建 relayfirst-signer
  解锁后内存持密钥；只暴露【本机】签名 API
  认证：bearer token（0600 文件）/ Unix socket 权限   ← E7
  暴露：sign(payload, keySelector) → signed
  【无任何导出 key 的 API】  ← 备份 = init 的 mnemonic
```

**policy = per-key domain 白名单**（E3）：

```text
每个密钥一个【允许的 EIP-712 domain】集合；
  不在集合 → 拒绝；结构不符 → 拒绝；原始 hash → 拒绝。
每次签：记日志（timestamp · domain · signer address）。
```

**代码注释必须写清 why**（原提案的核心洞察，保留）：

> signer 挡住了"看到密钥"，但**被注入的 agent 仍能调用 signer**。
> **盲签**会让它用**它构造的** EIP-712 payload 外泄（如 Permit2 授权）。
> **policy 是第二道墙。**

---

## 4. `mine` 接线

```text
agent(LLM) 产出 receipt 【content】  →  CLI 发给 signer
signer 验 schema → 签 → 返回签名
CLI 组装签名回执 → 提交
```

**断言（可测的结构化命题，E5）**：

| 断言 | 怎么测 |
|---|---|
| 密钥**不进 LLM 输入** | provider 只收 task/spec，**从不收 key**（现有结构即如此；加测试锁住） |
| **MCP 无 crypto** | CI 导入图门禁（已存在） |
| 无 **env/argv** 泄露 | key 只从 **signer 的** keystore/token；CLI 不再收 `--key`（迁移后） |

**不测**"内存里没有密钥"—— **不可断言**。

---

## 5. MCP（**不反转 ADR-0008**，E4）

```text
MCP 保持零密码学（stdlib only，CI 门禁钉住）
MCP 只多一个【本地 HTTP 客户端】→ 调 signer
```

→ "**MCP 永不持密钥**"仍成立，且"不可导出"是**结构性质**（MCP 里没有密钥可导）。
**MCP 不新增 `sign_receipt` 的实现** —— 它**转发**到 signer。

---

## 6. PoSR / `relayfirst bind` —— **拆出**

**不在本规格。** 原提案的 "PoSR binding" **未定义**且与"节点无密钥"冲突。
**`bind` 子句需单独一份设计**（先回答"绑定的语义是什么、谁证明、防什么"）。

---

## 7. 任务分解（批准后）

| id | 任务 | 依赖 | 备注 |
|---|---|---|---|
| **AS-1** | **keygen 原语**（助记词 → 两把可复现密钥）。**注**：secp256k1 **已有依赖**（`decred/…/secp256k1`）→ 只缺 BIP-39/32 **小库**（不手写，A1）；**一把助记词 + 两条 BIP-44 路径**（非两把助记词 —— 原提案的"熵独立"是为不存在的 PoSR 服务的）| D6 | 纯函数，不写盘、不签名 |
| **AS-2** | `relayfirst init`（keystore + mnemonic 确认 + 幂等） | AS-1, **D2 批准** | 0600 / OS keychain |
| **AS-3** | signer 进程（本地 API + token 认证 + 无导出） | — | 或扩展 verifier |
| **AS-4** | **per-key domain 白名单** + 日志 | AS-3 | **security-critical**；变异验证 |
| **AS-5** | `mine` 接线（agent 产 content → signer 签） | AS-3/4 | key 不进 LLM |
| **AS-6** | MCP HTTP 客户端（零 crypto 不变） | AS-3 | CI 门禁继续钉 |
| **AS-7** | 测试（keygen 独立 / policy 拒 foreign / init 幂等 0600 / E2E） | AS-1..6 | policy 要有变异验证 |
| **AS-8** | 入门文档更新 | AS-2 | 含 loss semantics |
| **AS-9** | ADR-0009 → accepted | — | 批准后 |

**PoSR/bind**：单独 plan，**不进本表**。

---

## 8. 反模式（不要做）

| ❌ | 为什么 |
|---|---|
| 照原提案"只签 receipt" | 破坏 session/card/event |
| 给 MCP 加 crypto | 反转 ADR-0008，门禁 FAIL |
| 只靠 localhost | 任何本地进程（含被注入 agent）都能签 |
| 手写 BIP-39 / 派生 | **A1** |
| 测"内存无密钥" | 不可断言 → 空过测试 |
| 引入 "node key" | 违反 ADR-0004（节点无密钥） |
