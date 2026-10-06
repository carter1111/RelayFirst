# S10 验收报告 — 节点升级 + 可归因验证

- 日期：2026-10-05（补写于 2026-10-06）
- 状态：✅ **达成**

## 一句话结论

节点从"哑存储"升级为"能索引/查询/中转，但**仍不能验签**"；"可归因验证"落到一个**独立组件**
（`internal/assertion`），因此**两种信任模型同时成立**：无密钥节点 + 持密钥可归因验证者。

## 交付

| 模块 | 内容 |
|---|---|
| `internal/sqlite/observations.go` + `internal/node/observations.go` | `GET /observations`（按 subject 索引）、`GET /observations/{id}/evidence`（按 contentHash 分组，`distinctAgents`） |
| `internal/sqlite/tasks.go` + `internal/node/tasks.go` | `POST/GET /tasks`、`POST /tasks/{id}/claim` |
| `internal/assertion` | 可归因验证：节点签**自己的结论**（EIP-712 `assertion-1`）；`Check` 把断言当**输入评估** |

## 对应验收判据

| 判据 | 状态 |
|---|---|
| **⑩ 节点不可信但能干** | ✅ 两半：① 能索引/查询/中转 ② 客户端在节点撒谎时仍能独立判定 ③ 结论可归因 |

## 实测数字

- S10-1/2/3 端到端 **20 项**（含"索引不可用不阻断投递""不仲裁""不按本地钟过滤""`futureField` 存活"）
- S10-5/6 共 **16 项**（核心 `TestCheck_ClientDecidesIndependently`：验证者对改写字节签"valid"，客户端仍拒绝）
- **节点导入图仍不含** `eip712`/`receipt`/`publish`/`scoring`/`mining`/`assertion`（门禁强制）

## 偏差与遗留

- **裁决记录**：`MVP.md` §7.1（节点哑）与 §7.3（节点可验证）冲突 → 裁定为**索引器与验证者是两个角色**
  （见 `TASKS.md §10.2b`）。最小节点保持无密码学；"可归因验证"是独立可选组件。
- **claim 是"兴趣"非"授予"**：节点无权威授予排他性；响应恒 `granted:false`。
- **未做**：节点的"实时"全量流（hub 只按 agentId 扇出）。

## 下一步阻塞

无（S10 自闭环）。
