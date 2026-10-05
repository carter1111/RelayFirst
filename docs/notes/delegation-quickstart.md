# Delegation — 使用指南（S13-3）

> **本文是操作指南，不是规范。** 权威定义在 `MVP.md` / `ARCHITECTURE.md` §3.4；
> 本文只回答"我怎么跑起来"。
>
> 状态：**已实现**（S13-3d 签发者 + 消费者闭环，见 `TASKS.md` §10.5）。

---

## 1. 两个密钥，各司其职

| 密钥 | 谁持有 | 干什么 | 环境变量 |
|---|---|---|---|
| **owner key** | 人 / 冷端 | **签发** grant（授权） | `RELAYFIRST_OWNER_KEY` |
| **session key** | agent / 节点 / 热端 | **签事件**（干活） | `RELAYFIRST_SESSION_KEY` |

session key 存在的意义是：**热端不拿主密钥也能干活**。它只能做 grant 显式授权的那几类事。

### ⚠️ 硬边界：回执永不可委派

**没有能让 session key 签回执的 scope，一个都没有。** 这不是"有校验禁止"，而是
**Scope 是闭集、根本没有那个值** —— 程序无法表达这个请求。

原因：回执是**唯一赚积分的东西**。允许委派回执 = 必须放宽"签名者地址 == agentId"这条
检查；放宽后**无法自我限定**在"只允许某类"，判断错一次 = **一把泄露的热密钥无限刷积分**。
不委派则完全不动那条不变量。详见 `internal/delegation/delegation.go` 的包注释与
`TASKS.md` S13-3。

---

## 2. 身份模型（一句话）

**事件的 `actor` = 实际签名者（session key），owner 的身份随 grant 带外传递。**

这与 `ARCHITECTURE.md` §4.2 的 `RelayEnvelope` 一致：`agentId`（=签名者）+
`delegationId`（正在行使的 grant）。所以 `eventsign.Verify` 的 `signer == actor`
检查**保持不变**，权限问题由 grant 裁决。

---

## 3. 完整流程

### 步骤 0 — 拿到两个 agentId

```bash
# session key 的 agentId（你要授权给谁）
relayfirst id <SESSION_KEY_HEX>

# owner 的 agentId（谁在授权）——grant 会自动从 owner key 派生，无需手填
relayfirst id <OWNER_KEY_HEX>
```

`agentId` 形如 `agent:eip155:8453:0x...`。**身份从密钥派生，不可自选。**

### 步骤 1 — owner 签发 grant

```bash
relayfirst session grant \
  --key <OWNER_KEY_HEX> \
  --session-key agent:eip155:8453:0x7099...79c8 \
  --scopes session:event,task:progress \
  --valid-for 24h
```

| flag | 含义 |
|---|---|
| `--key` | **owner** 的私钥（或 `RELAYFIRST_OWNER_KEY`）。grant 由它签。 |
| `--session-key` | 被授权的 session agentId。**必填。** |
| `--scopes` | 逗号分隔。可授权：`session:event` / `task:progress` / `task:lifecycle`。默认 `session:event`。 |
| `--valid-for` | 窗口长度，如 `30m` / `24h`。默认 `24h`。 |
| `--grant-nonce` | owner 对该 key 的单调计数器。**撤销 = 递增。** 默认 `1`。 |
| `--out <path>` | 顺带把签名后的 grant 写文件。 |

`--relay` 可选：给了就**同时**把 grant 作为 `kind=grant` 的 envelope 发到 relay
（`id = keccak256(grant)`，**重发即同 id，节点会自动去重**）。

> **grant 走带外**：relay 只是众多传递方式之一。grant 是**带签名的普通 JSON**，
> 可以贴聊天、放文件、发邮件 —— 任何通道都行。**它不需要在链上，也不需要节点帮忙。**

签发时会**自动 `Verify()` 自检**：不合格的 grant 拒绝输出。

### 步骤 2 — session key 签事件（照常）

```bash
relayfirst session open --key <SESSION_KEY_HEX> --relay http://localhost:8080
```

`session open/close` 用的是 session key，事件 `actor` 就是 session key 的 agentId。
（链状态存在 `.relayfirst-session-state.json`，跨进程单调。）

### 步骤 3 — 消费方验证（读侧）

```bash
relayfirst session verify \
  --relay http://localhost:8080 \
  --agent agent:eip155:8453:0x7099...79c8 \
  --grant ./grant.json
```

它会：拉 `GET /messages/{agentId}` → **只取 `event` kind**（其余计数不解析）→
逐条跑授权检查 → 输出每条 `authorized` / `refused` + 原因。

`--grant` 可以是**文件路径**，也可以是**内联 JSON**（直接粘贴对方给你的 grant）。

---

## 4. 撤销（nonce）

**撤销 = owner 发布更高的 nonce。**

```bash
# 撤销所有 nonce <= 1 的 grant
relayfirst session grant --key <OWNER> --session-key <SID> --grant-nonce 2 ...
```

消费方验证时带上最新 nonce：

```bash
relayfirst session verify \
  --relay http://localhost:8080 \
  --agent agent:eip155:8453:0x7099...79c8 \
  --grant ./grant.json \
  --grant-nonce 2          # ← 比 grant 里的 nonce 大 = 视为已撤销
```

### ⚠️ 诚实的边界

**nonce 的"新鲜度"就是撤销的"新鲜度"。** 消费方如果只有旧 nonce，
**分辨不出"已撤销"和"仍有效"**。

默认行为：不传 `--grant-nonce` 时，用 grant **自己**的 nonce，输出会明确标注
`"no fresher nonce supplied"` —— 绝不假装撤销是即时的。

---

## 5. 为什么消费方是 CLI 而不是节点

校验需要 `eip712`（secp256k1）。而 **relay 节点一旦能验签，就能伪造**
（`MVP.md` §7.1）。所以：

- **节点**：只存/转字节，`go list -deps` 证明它**链接不到任何签名代码**（CI 门禁）。
- **客户端**：拉字节 + 自己判定。节点撒谎时客户端仍能独立下结论。

这也是为什么 `relayfirst session verify` 必须由你自己跑，而不能"信节点说它验过了"。

---

## 6. 常见错误

| 报错 | 原因 |
|---|---|
| `--session-key equals the owner key` | owner 给自己授权没有意义，被拒 |
| `scope "..." is not grantable` | scope 拼错，或写了 `receipt:*`（**不存在**） |
| `the signer is not this grant's session key` | 事件签名者与 grant 里的 sessionKey 不符 |
| `scope "..." was not granted` | 事件类型超出了 grant 授权的范围 |
| `revoked (grant nonce N, current M)` | `--grant-nonce` 比 grant 的 nonce 大 = 已撤销 |
| `no delegable scope ... may not emit it` | 超时类事件**刻意不可委派**（不能让别人帮你过期） |

---

## 7. 相关代码与命令

| 位置 | 作用 |
|---|---|
| `cmd/relayfirst/session.go` | `session open/close/show/grant/verify` |
| `internal/delegation` | scope 闭集 + grant 形状 + 撤销规则（**无密码学**） |
| `internal/delegationsign` | grant 的签/验 |
| `internal/eventsign` | 事件签/验 + `AuthorizeEventWithGrant` / `AuthorizeDerivedScope` |
| `internal/protocol` | `KindGrant` / `KindEvent` envelope 类型 |

权威定义：`MVP.md`（范围）、`ARCHITECTURE.md` §3.4 / §4.2（数据形状）、
`docs/notes/s13-e2ee-plan.md`（S13 完整方案）。
