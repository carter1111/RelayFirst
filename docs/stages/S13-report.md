# S13 验收报告 — E2EE + 密钥层级 + 委托 + MCP

- 日期：2026-10-05（补写于 2026-10-06）
- 状态：✅ **S13-1…S13-5 全部达成**

## 一句话结论

S13 的正确形态**不是"给消息加密"，而是"把密钥权威分层"**：
owner / session / verifier 各持其权，**回执永不可委派**（[ADR-0007](../decisions/ADR-0007-receipts-never-delegable.md)）。

## 交付

| 模块 | 内容 |
|---|---|
| `internal/e2ee` | X25519 + XChaCha20-Poly1305 + HKDF-SHA256（**A1：全用库**） |
| `internal/delegation` | **无密码学**的 Scope 闭集 + Grant + 撤销规则（节点可安全引用） |
| `internal/delegationsign` | grant 的签/验（EIP-712 `delegation-1`） |
| `internal/eventsign` | 事件签/验 + `AuthorizeEventWithGrant` / `AuthorizeDerivedScope` |
| `cmd/relayfirst-mcp` | **零密码学** MCP server（[ADR-0008](../decisions/ADR-0008-mcp-handwritten.md)） |
| `cmd/relayfirst/session.go` | 事件生产者 + 委托签发者 + 消费方（`session open/close/show/grant/verify`） |

## 对应验收判据

| 判据 | 状态 |
|---|---|
| **② 第三方离线验证回执** | ✅ 加密下仍成立：密文放进 `result.value`，无密钥第三方 `Validate(nil)` 通过 |
| S13 三条设计点 | ✅ 全部拍板（均按建议） |

## 实测数字

- **S13-5**：`TestPrivateReceipt_ThirdPartyVerifiesWithNoKey`（判据 ② 在加密下的形态）；**9 项**新测试
- **S13-3**：`AuthorizeEvent` 是唯一入口，合成"验签/未撤销/窗口/scope"四件事；**26 项测试**
- **S13-3d**：签发者 + 消费者闭环；**17 项测试**；**变异验证**：跳过授权 → 3 项 FAIL
- **S13-4**：`cmd/relayfirst-mcp` 导入图**只有标准库**；CI 门禁强制

## 偏差与遗留

- **回执永不可委派**：决策已独立成 **ADR-0007**（此前只有 notes）。
- **MCP 手写**：偏离"用 SDK"的默认，理由记录为 **ADR-0008**。
- **S13-2b** 的外层回执集成**由 S13-5 交付**（此前误标"未做"，已核正）。
- **密钥不由工具生成**：见 `README.md` / `getting-started.md` —— "替用户落盘私钥"是资产控制决定。

## 下一步阻塞

无（S13 自闭环）。
