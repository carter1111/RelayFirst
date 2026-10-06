# S11 验收报告 — SBT 积分徽章（链上）

- 日期：2026-10-05（补写于 2026-10-06）
- 状态：✅ **合约侧达成**；⚠️ 真钱包可见需外部验证

## 一句话结论

积分从"链下账本"变为**不可转让的 SBT 徽章 NFT**：转让 **revert**、累计值走 **Merkle claim**、
`tokenURI` 是**自包含 data URI**（无服务器依赖）。

## 交付

| 文件 | 内容 |
|---|---|
| `contracts/RelayPoints.sol` | ERC-721 + **ERC-5192** + `_update` 转让拦截 + 累计 claim + on-chain SVG `tokenURI` |
| `contracts/interfaces/IERC5192.sol` | 2 个函数的自实现接口 |
| `contracts/test/RelayPoints.t.sol` | 15 项（含转让/claim/元数据） |
| `contracts/test/RelayPointsAnchor.t.sol` | **4 项（2026-10-06 补）**：**真 `RelayAnchor` 部署**端到端 claim |

## 对应验收判据

| 判据 | 状态 |
|---|---|
| **⑨ SBT 可见且不可转让** | 🟢 **合约侧达成**（转让 revert + 累计 claim + 自包含 metadata）；**真钱包渲染**需外部 |

## 实测数字

```
forge test   45 passed（S11 相关 19：15 + 4 真 anchor）
```

- **转让 revert**：`transferFrom` **与** `safeTransferFrom` 两条路径都测。**变异**：移除 revert → 3 项 FAIL。
- **claim 与真 anchor**：`submitRoot` 后真 claim 成功；抓到 **stub 抓不到的叶/节点编码不匹配**
  （`leafHash=keccak256(0x00‖leaf)`、`nodeHash=keccak256(0x01‖l‖r)`）。**变异**：改 `_leaf` 字段序 → 3 FAIL。
- **跨 epoch 根被拒**（错 epoch 失败、自身 epoch 成功）。
- **`tokenURI` 不编造**：只返回合约**真实持有**的 `agentId` + 累计 points（不造 `rank`/`receipts`）。

## 偏差与遗留

- **`tokenURI` 字段做减法**：`MVP.md` §6.1 草图里的 `receipts`/`rank`/`verifiedRate` **链上没有** →
  **不编造**（常量 rank 会被钱包当有意义的值显示）。
- **未达成**：**真钱包可见积分 metadata** —— 需**真钱包**（外部动作）；**合约已与真 `RelayAnchor` 对跑**（已补）。

## 下一步阻塞

真钱包渲染（人工）。
