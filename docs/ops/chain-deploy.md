# 判据 ⑨ 链上部署步骤（RelayAnchor + RelayPoints）

> **用途**：判据 ⑨「SBT 在钱包可见 + 不可转让 + Merkle claim 拿累计值」。**需要你选链。**
> **现状**：合约与测试**全绿**（`forge test` 47 项）；**刻意未上链**（`S7-report.md`：无资金账户/无部署权限）。
> **配套**：`contracts/`、`docs/stages/S7-report.md`、`RelayPointsAnchor.t.sol`（真合约互测）。

---

## 0. 先选链（决定 gas 与 faucet）

| 选项 | 说明 |
|---|---|
| **L2（推荐）** | claim 合约放**低成本 L2**（`incentive.md §4`）；gas 可忽略。选一个你已有钱包/有 faucet 的测试网起步 |
| 测试网先行 | 先在 testnet 走完全流程（部署 → submitRoot → claim → 钱包看到 SBT），再上主网 |

**需备**：一个**部署钱包**（有该链原生 gas）、目标链 RPC、`RELAY_OPERATOR`（谁发布 root）。

---

## 1. 合约与依赖（读源码确认的构造参数）

| 合约 | 构造参数 | 作用 |
|---|---|---|
| **`RelayAnchor`** | **无参** | `submitRoot(epoch, root, width)` / `verifyProof(operator, epoch, leaf, index, siblings)`；**无结算/无奖励/无 owner**；按 `(operator, epoch)` 命名空间隔离 |
| **`RelayPoints`** | `(address rootSource, address rootOperator, string name, string symbol)` | ERC-721 + ERC-5192；`rootSource` = **RelayAnchor 部署地址**，`rootOperator` = 发布 root 的 operator 地址 |
| `RelayBounty` | （USDC，S12，非判据⑨）| — |

> **`rootOperator` 在部署时固定、不可改**（`RelayPoints.sol` 注释）：可变的 operator 会让部署者事后把 claim 重定向到自己选的根 —— 而那种权威正是设计**刻意没有**的。

---

## 2. 部署（foundry；`foundry.toml` 已配 `solc 0.8.26` + OZ remapping）

```bash
export PATH="$HOME/.foundry/bin:$PATH"
forge build

# 1) Anchor（无参）
forge create contracts/RelayAnchor.sol:RelayAnchor \
  --rpc-url <RPC> --private-key <DEPLOYER_KEY>
#   → 记下 ANCHOR_ADDR

# 2) Points（依赖 Anchor 地址 + operator）
forge create contracts/RelayPoints.sol:RelayPoints \
  --rpc-url <RPC> --private-key <DEPLOYER_KEY> \
  --constructor-args <ANCHOR_ADDR> <OPERATOR_ADDR> "RelayFirst Points" "RFPTS"
#   → 记下 POINTS_ADDR
```

- [ ] `RelayAnchor` 部署，记 `ANCHOR_ADDR`
- [ ] `RelayPoints(ANCHOR_ADDR, OPERATOR_ADDR, name, symbol)` 部署，记 `POINTS_ADDR`

---

## 3. 接上激励线的 balance root（**关键：这是⑨与激励线的接点**）

```text
relayfirst settle --epoch <n>
  → 输出 balanceRoot（= 累计可用 points 的 Merkle root，MVP §6.2b）
```

**operator 提交该 root**（`submitRoot`，**一笔低成本交易**；`incentive.md` 建议每周一次）：

```bash
# 用 operator 私钥（= 部署 Points 时的 rootOperator）
cast send <ANCHOR_ADDR> "submitRoot(uint256,bytes32,uint256)" \
  <epoch> <balanceRoot> <width> \
  --rpc-url <RPC> --private-key <OPERATOR_KEY>
```

- [ ] `relayfirst settle` 产出 `balanceRoot`
- [ ] `submitRoot` 上链成功（同一 `(operator, epoch)` **只能写一次**）

---

## 4. 用户 claim → 钱包看到 SBT

```bash
relayfirst claim --agent <agentId> --epoch <n>     # 产 proof（无 key）
# 用 claim 合约提交（claimPoints(agentId,total,epoch,index,proof)），或复用现有工具
```

**验证（判据 ⑨ 的三条）**：
- [ ] **钱包可见**：徽章 NFT 出现在真钱包，**metadata 含积分**
- [ ] **不可转让**：`transferFrom` / `safeTransferFrom` **必须 revert**（不是仅 `locked()` 声明）
- [ ] **Merkle claim 拿累计值**，且**漏 claim 不丢分**

> ⚠️ **一条容易被忽略的接点**：`claim` 证明的 total 现在是 **`earned − burned`**（2026-10-08 裁定，`status-map.md §2`）。
> 若 claim 合约与根对不上，先查这里，别先怀疑合约。

---

## 5. 落档

- [ ] 部署地址（`ANCHOR_ADDR` / `POINTS_ADDR` / chainId）记进 `docs/stages/S7-report.md`
- [ ] `status-map.md §1`：判据 ⑨ 从 🟡 → ✅（或如实说明差哪条）
- [ ] `mvp2-launch-readiness.md`：同步
