# ADR-0007: 回执永不可委派（Session Delegation 的作用域闭集）

- 状态：**accepted**（2026-10-05 裁定）
- 日期：2026-10-05
- 影响层级：**L0**（触及不变式 A5/A6 的边界；`MVP.md` §8 / §12 的 Session Delegation 范围）
- 来源：S13-3（Session Delegation）实现时的核心决策

---

## 背景

S13 要引入 **Session Delegation**：一把**热密钥（session key）**代表 owner 行动，
这样 agent / MCP server / 节点**不必持有主密钥**。最自然的设计是给 session key 一个**作用域**，
让它签作用域内的任何东西 —— **包括回执**。

**回执是整个系统里唯一"赚积分"的东西**（`MVP.md` §6）。所以"session key 能否签回执"
直接决定了**一把泄露的热密钥能不能无限刷积分**。

### 关键事实：回执今天【结构上】不可被委派

`receipt.Validate` 要求**签名密钥的地址 == `agentId` 里的地址**。
session key 是**不同密钥** → 恢复出**不同地址** → 回执被拒。
**这不是有人写的规则，是那条检查的推论。**

**所以"允许委派回执"= 必须放宽那条检查** —— 而那条检查是"**一个 agent 不能冒充另一个**"的**唯一依据**。

---

## 决定

**回执永不可委派 —— 且不是"有校验禁止"，而是"程序无法表达这个请求"。**

`delegation.Scope` 是**闭集**，**没有 `receipt` 这个值**：

```go
const (
    ScopeSessionEvent  Scope = "session:event"
    ScopeTaskProgress  Scope = "task:progress"
    ScopeTaskLifecycle Scope = "task:lifecycle"
)
```

`ScopeForReceipt` **刻意不定义**（`delegation.go` 写明：stub 返回 `""` 会成为可传递、可比较、
可存储的值，日后就会被"赋予意义"）。

**因此**：`AuthorizeEvent` 收到回执作用域时**在编译期就无从构造**；一个未知/未来的作用域字串
会被 `Scope.Valid()` 拒绝。

---

## 备选与取舍

| 选项 | 优点 | 缺点 |
|---|---|---|
| **A. 闭集，无 receipt 值**（裁定） | 回执委派**无法表达**；不触碰 `receipt.Validate`；老回执零影响 | 日后想委派回执要**改类型**（这是优点） |
| B. 有 `ScopeReceipt` 值但 validation 拒绝 | 保留"显式禁止"的可见性 | 离"被接受"**只差一次重构**；且要写"哪种签名能签哪种对象"的分支 —— **判断错一次 = 无限刷分** |
| C. 允许委派回执（session key 可签回执） | 最灵活；session key 能干一切 | 必须**放宽 `receipt.Validate` 的 signer 检查**，而放宽**无法自我限定在"只会话级"**；一把泄露热密钥 = **无限积分** |
| D. 用 `delegationId` + 单独 policy 约束回执委派 | 理论可做 | 引入**第二套授权语义**；两套语义分歧处就是绕过点 |

**为什么 A 而非 B：** B 是一个**可被一次重构打开的门**；A 是一扇**没有门的地方**。

---

## 后果

1. **`receipt` 的 signer 检查一字不改** —— 它仍是"不可冒充"的唯一依据。
2. **session key 只能签事件**（`session:event` / `task:progress` / `task:lifecycle`）。
   即便 session key 泄露，损失上界是**在授权范围内的噪声**，**不是积分**。
3. **`eventsign.ScopeOfEvent` 无默认值** —— 未分类的事件类型**拒绝授权**（不是兜底到某个 scope）。
   **超时事件刻意不可委派**（命令注释写明：relay 观测 deadline 产生，不是 owner 授权行为）。
4. **历史回执零影响**（A9）：未改 `receipt` 的任何字节。

**不可逆的部分：** 若日后真要委派回执，那**不是"加个 scope"**，而是**改不动不变式**，
需要**改 ADR + 反刷量分析 + A6/A7 复审**。

---

## 证据

```text
internal/delegation/delegation.go     —— Scope 闭集；无 ScopeForReceipt（注释说明理由）
internal/delegation/delegation.go     —— AuthorizeEvent 合并"验签/未撤销/窗口/scope"四件事
internal/eventsign/authorize.go       —— ScopeOfEvent 无默认值；超时事件拒绝
测试                                  —— TestNoScopeCoversReceipts（变异：加 receipt scope → FAIL）
                                      —— TestScopeOfEvent_RefusesWhatIsNotDelegable
                                      —— TestScopeOfEvent_NeverReturnsAReceiptScope
docs/notes/s13-e2ee-plan.md           —— 决策的完整论证
```

**变异验证**：给 `delegation` 加一个 receipt scope → `TestNoScopeCoversReceipts` FAIL。
