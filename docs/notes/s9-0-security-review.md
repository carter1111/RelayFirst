# S9-0 安全审查结论（发布前阻断项）

- 日期：2026-10-04
- 审查对象：S9-0 版本化地基（commit `193dcda`）
- 结论：**发现 4 个发布前阻断项 + 1 个高 + 3 个中 + 3 个低**
- 状态：**B1 已修**（本文件写作时）；**B2/B3/B4/H1 未修**

> **这份文档存在的理由：** S9-0 改的是**验证路径**，而验证路径的缺陷是**静默**的。
> 一次"看起来通过了"的测试不能证明安全 —— 必须**用真实攻击测**。
> 本轮的四个阻断项里，有三个正是这么发现的。

---

## 0. 最重要的一条：我的测试通过了一个错误的东西

**B1 是真实缺陷，而且它躲过了一个"通过"的测试。**

```text
TestMarshal_OmitsEmptyPayload 用了 json.Marshal   → 通过 ✅
真实路径用的是 MarshalCanonical                    → 丢弃 payload ❌
```

**教训：** 测试断言的是**我以为的**序列化路径，不是**实际的**。
**验证"测试是否有效"，必须打到真实的执行路径上。**

---

## 1. 发布前阻断项（must fix before first public release）

### B1 — `MarshalCanonical` 丢弃 `Payload`，签名 blob 在持久化/导出时被销毁 ✅ **已修**

**严重度：critical** · `internal/receipt/canonical.go:145-157`

`canonicalJSON` 的 `case Receipt:` **逐键枚举，漏掉了 `payload`**。而 `MarshalCanonical` 是
**唯一**的持久化序列化器：

| 调用点 | 用途 |
|---|---|
| `store.Save` | 落盘 |
| `store.SaveVerification` | 更新验证结论 |
| `store.ExportReceipts` | **用户导出** |
| `publish.ReceiptEnvelope` | 向 relay 投递 |

**实测（已确认）：**

```
canonical contains payload: false
payload survived canonical round trip: false
```

**为什么致命：** S9-0b2 的整个设计前提是"blob 会存活"。经 canonical 路径它**不存活** ——
用 `Payload` 签的回执会被当成结构性回执存/导出。一旦未来加字段，
**导出的产物不再可验**。这正是 A9 存在的理由，也顺带解绑了 `receiptId`（见 B2）。

**修法：** 在 `case Receipt:` 加 `{"payload", t.Payload}`（放最后，`omitempty` 保证旧回执字节不变）；
加**经 `MarshalCanonical`** 的往返测试。

### B2 — `receiptId` 未签名，却是去重/积分/存储/verdict 的键

**严重度：critical** · `internal/receipt/receipt.go:163-170`

`receiptId` **不在 `signedPayload` 内**，且没有验证器重算它。但所有幂等控制都以它为键：

| 控制 | 位置 |
|---|---|
| store 冲突 | `store/receipt.go:61` |
| 节点/邮箱去重 | `sqlite/mailbox.go:81` |
| 积分幂等 | `scoring/points.go` (`byReceipt`) |
| verdict 查找 | `verification/verdicts.go:56,170` |

而线上的 id 是**攻击者选定的**（`protocol.Envelope.ID`）。

**攻击：** 拿一份有效签名回执，用**任意新 `receiptId`** 重发。每个新 id 都绕过 store 冲突、
邮箱去重、积分幂等。**这直接破坏 A6（全局去重）** —— artifact 账本在单个部署内仍挡 novelty，
但任何以 `receiptId` 为键的消费者都能被诱导重复计数。

**修法：** 让 `receiptId` 被签名（加入 `signedPayload`，`Validate` 重算比对），
或**不再把它当信任输入**，改为从已验证的 payload 派生。

### B3 — `schema` 未签名，且验证器忽略其 major（跨版本签名混淆）

**严重度：critical（G2）** · `internal/receipt/receipt.go:287, 204-233`

`TypedData` 只绑定 `{agentId, epoch, payloadHash}`，domain `Version` **硬编码 "1"**。
所以 **v1 与 v2 回执的签名字节完全相同**，只有未签名的 `schema` 字符串区分它们。

**攻击：** 中间人把一份 v1 签名回执**改标为 `v2`**（或 `v1.999`），**无需改签名**，
它就在 v2 规则下通过。这正是 A9 §② 被**反转**：版本选择器本身未签名，
而验证器**依据未签名的标签**选择规则。一旦 v2 改变验证语义，这就是跨版本伪造原语。

**修法：** 把 schema（至少 major）**绑进签名载荷或 EIP-712 domain/message**；
验证器**从已签名的 schema** 取规则。未签名的 `schema` 只留作人类可读提示。

### B4 — 严格解码使"增量字段"无法通过 wire；A9 §④ 的承诺**尚未成立**

**严重度：high（发布门槛设计缺口）** · `internal/receipt/receipt.go:480-489`

`Unmarshal` 仍带 `DisallowUnknownFields`。而 S9-0b2 只在**内存中**容忍增量字段 ——
`checkPayloadConsistency` 会拒，因为结构化 struct **装不下它不认识的字段**。

**实测：**

```
内存内验证带增量字段的 payload = payload does not match the structured fields...
Unmarshal 带未知顶层字段        = json: unknown field "newTopLevelField"
```

**所以一份带新字段的 v1.1 回执，在解码和一致性检查两处都被拒。**

**关于"严格解码是不是单向门"：是。** 已部署到用户机器的验证器**无法被追溯改造**；
若它拒绝未知字段，则"旧回执永久可验"对**任何带新增字段的回执**都失效。

**关键：这不是翻一个 flag 能修的。** 容忍解码 + 一致性要求一致性检查
**只比对 blob 中实际存在的字段**，而非完整结构重建。那就是 S9-0b，标注为待办是正确的。

**发布前必须改的是【说法】，不只是【flag】：**
- **不要**把当前状态描述为满足 A9 §④
- **要么**在发布前落地 S9-0b，**要么**明确把首发范围限定为"暂无增量字段"并写成已知限制

---

## 2. 高

### H1 — `canonicalJSON` 没有数字分支，`float64` 会崩溃/破坏验证

**严重度：high** · `internal/receipt/canonical.go:35-200`

类型开关处理了 `int*`/`uint*`，**没处理 `float64`/`json.Number`**；反射随后报错。
`Task.Spec` 是 `map[string]any`，任何非字符串 JSON 值都会解成 `float64`。

```
spec 含数字 → 往返后类型 float64 → verify 报 "canonicalJSON: unsupported type float64"
```

`MarshalCanonical` 在导出路径上，`checkPayloadConsistency` 在验证路径上 ——
**一个含数字的 spec 要么让导出崩，要么让合法签名回执验证失败。**

**修法：** 加 `float64`/`json.Number` 分支（整数不带 `.0`，拒绝非有限值）+ 数字 spec 往返测试。

### H2 — 生产调用方不区分 `UnsupportedError` 与 `ValidationError`

**严重度：high** · `cmd/relayfirst/main.go:245-252`、`mining/verdict.go:59`、`mining/loop.go:271-274`

两个错误类型**只在 `schema_test.go` 里被正确使用 `errors.As`**。真实调用方把任何错误
映射为 `false` 或原样返回。

**后果：** S9-0a 的**全部意义**就是"过旧的验证器说'我验不了这个版本'而不是'这是伪造'"。
没有消费者分支，操作员看到通用失败就**推断为伪造** —— 正是该文件注释说要避免的误判。

**修法：** 在 CLI（独立退出码/信息："请升级验证器"）、`SelfCheckVerdicts`、矿工自检处
分支 `errors.As(err, &un)`。

---

## 3. 中

### M1 — 语料绊线可被"重新生成"悄悄重写

**严重度：medium** · `gen_frozen_receipts.go:150-187`、`corpus_test.go`

门禁只断言**已提交文件可验**，不锁其**字节/哈希**。生成器**原地重写**语料文件。
开发者改了 canonical 写入器 → 重新生成 → 提交，**门禁通过，而历史产物已被重写**，
append-only 意图失效。

**修法：** 提交语料的 SHA-256 清单并在测试中断言；生成器**拒绝覆盖**已存在文件。

### M2 — 语料总是用**当前** build 的规则验证，G2 无法表达

**严重度：medium** · `corpus_test.go:70-92`

所有条目都用 `r.Validate(nil)`（当前规则）验证，**没有 per-major 验证入口**。
所以钉住的是"v1 产物在**今天的**规则下可验"，而非"在 **v1 规则**下可验"。
G1 可接受，但不满足 G2 —— 也因此 **B3 的跨版本混淆对当前语料不可见**。

### M3 — `Payload` 被当作"已签名字节"，但没有证据它被签过

**严重度：medium** · `receipt.go:186-198,371-390`

`SignedPayload` 无条件返回 `[]byte(r.Payload)`，一致性检查只比字段-vs-blob。
一份原本结构性签名的回执，**加上等于其结构字节的 `Payload` 后仍可验**（字节相同 → 哈希相同）。
这个特定情形无害，但**验证器无法区分"这个 blob 就是被签的"与"它恰好哈希相同"**。
消费者**不得**把 `Payload` 当权威元数据。

---

## 4. 低

- **L1 — `agentId` 的 chainId 未绑定。** `Validate` 只比对地址部分（`receipt.go:415-430`），
  `eip155:<chainId>` 可被改。与 B3 同根。
- **L2 — 语料里没有结构性路径的回执。** 生成器**总是**冻结逐字 payload，
  所以两份提交文件都带 `payload`。**A9 §① 关于"遗留路径永久可验"的声明，
  只被同 build 的单元测试钉住** —— 正是语料本该覆盖的盲点。
- **L3 — 测试断言错误字符串**（`schema_test.go:98,310`）。作为次要检查可接受，
  但 H2 的修法必须分支**类型**而非子串。

---

## 5. 约束状态

| 不变式 | 状态 |
|---|---|
| **A1** 不手写密码学 | ✅ OK（`internal/eip712` + secp256k1 库） |
| **A3** no-CGO | ✅ OK（`CGO_ENABLED=0 go build ./...` exit 0） |
| **A4** KAT 门禁 | ✅ 未受影响 |
| **A5** 积分性质 | ✅ 未受影响 |
| **A6** 全局去重 | ⚠️ 结构未变，但 **B2 削弱了它的执行面** |
| **A9** 版本兼容 | ❌ **B1/B3/B4 各违反一条**（§④/§②/§④）；M2/L2 使 §⑥ 的 CI 证据弱于声明 |

---

## 6. 发布门槛

```text
首发前必修：    B1（已修）· B2 · B3 · H1 · 并解决 B4
声称 A9 合规前：H2 · M1 · M2 · L2
可后置：        M3 · L1 · L3
```

**⚠️ 在 B2/B3 修好之前，不应声称满足 A9。** 当前状态是"版本化地基已开始，
但签名字节与信封字段之间仍有未绑定的信任输入"。

---

## 7. 对 S9-0 任务分解的影响

| 任务 | 影响 |
|---|---|
| S9-0a schema 语法 | ✅ 完成（但 **B3** 说明"未签名"是缺陷 → 需补签名绑定） |
| S9-0b 未知字段容忍 | ⏸ **升级为发布门槛**（B4） |
| S9-0b2 逐字 blob | ✅ 完成（**B1** 已修；**M3** 提示消费者约束） |
| **新增 S9-0h** | **绑定 `receiptId` 与 schema 到签名载荷**（B2 + B3） |
| **新增 S9-0i** | **`canonicalJSON` 数字分支**（H1） |
| **新增 S9-0j** | **生产调用方区分 `UnsupportedError`**（H2） |
| **新增 S9-0k** | **语料 SHA-256 清单 + 生成器拒绝覆盖**（M1） |
| S9-0f 语料 | 🔸 需补 **L2**（一份结构性回执）与 **M2**（per-major 入口） |
| S9-0g 门禁 | 🔸 G1 已接入；G2 依赖 M2 与 S9-0h |

**结论：S9-0 比原计划大。** 但这些都是"验证路径"的正确性问题 ——
**在开放公共节点之前修，成本最低；之后修，就是分叉。**