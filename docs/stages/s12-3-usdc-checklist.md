# S12-3 部署清单：最小 USDC 通道

> **状态：清单（可执行）。** S12-3 判为"机制已定、实现待外部条件" —— **外部条件 = 选链 + 测试币 + 部署**。
> 本文给出**照做步骤**，做多少由你定。
>
> 相关：`contracts/RelayBounty.sol`（非托管防双领）、`MVP.md` §6.5 / §12（**最小通道，非 x402**）。

---

## 0. 先明确这条通道**是什么、不是什么**（读合约注释）

**"不托管"是裁决，不是简化。** `RelayBounty`：

- **不持有、不转移、不能没收**任何代币（**文件里没有 `transfer` 调用**）。
- 只做一件需要**共享、防篡改**的事：**防双领**（`claim(bountyId, minerId, receiptId)` → `claimed` 位图）。
- **不判断 claim 是否有效** —— 有效性是**回执签名 + 验证结论**，**链下**判（索引，不裁决）。

**所以"USDC 通道"= Requester 直接 `transfer` 给 Miner**；合约**只记"谁领过"**。
这正是 `MVP.md` §12 说的"**最小通道**"，**不是** x402 适配器。

---

## 1. 前置（需要外部条件）

| # | 需要 | 怎么拿 |
|---|---|---|
| **1** | **选定链** | 建议 **Base Sepolia**（测试网）：本仓库默认 `chainId = 8453`（Base 主网），测试网对应 **84532**，与代码里的 agent id 前缀一致 |
| **2** | **测试网 ETH**（gas） | 通过 Base Sepolia faucet（官方或社区） |
| **3** | **测试网 USDC** | Circle 的测试网 USDC faucet。**地址从 Circle 官方文档copy，不要凭记忆填** |
| **4** | **一个部署私钥** | **不要用有真实资产的 key**；测试网专用，放环境变量 |

> **⚠️ 私钥纪律**：用 `--private-key` 会**进 shell 历史/进程表**。Foundry 支持从环境变量读；测试网 key 也当密钥对待。

---

## 2. 部署（一条命令，无需 forge-std）

本仓库**刻意不引入 forge-std**，所以**不用 `forge script`**。用 `forge create`：

```bash
export RPC_URL=<Base Sepolia RPC，如 https://sepolia.base.org>
export DEPLOYER_KEY=<测试网私钥>

forge create contracts/RelayBounty.sol:RelayBounty \
  --rpc-url "$RPC_URL" \
  --private-key "$DEPLOYER_KEY" \
  --broadcast
```

- **构造参数：无**（`RelayBounty` 没有 constructor）。
- 输出里记下 **`Deployed to: 0x…`** —— 这是 `bountyContract` 地址。
- **验证**：`canClaim(bountyId, minerId)` 对任意 (id, miner) 起初返回 `true`。

> **`RelayPoints` / `RelayAnchor` 同理**（若也要上测试网）：`RefayPoints` 的 constructor 需要
> **`rootSource` = 已部署的 `RelayAnchor` 地址** + **`rootOperator` 地址**，**先部署 `RelayAnchor`**。

---

## 3. 端到端跑一遍（"通道可用"的证明）

```text
① Requester 建赏金（链下）      → bountyId（bytes32，Requester 自选命名空间）
② Miner 做事、产出回执           → receipt.json（已签名，离线可验）
③ Miner 在链上记 claim：
     RelayBounty.claim(bountyId, minerId, keccak(receiptId))
   → 若 minerId 已领过该 bounty → revert AlreadyClaimed
④ Requester 看到 claim 后，
   直接 USDC transfer 给 miner   ← 【不经过合约】
```

**第 ③ 步的作用**：让"同一份工作被领两次"**在链上不可重写地**被挡住。
**第 ④ 步是普通 ERC-20 转账** —— 合约**不参与**，也**看不到**金额（它没有金额字段）。

---

## 4. 判据：什么时候算"接上了真实 USDC"

```text
① RelayBounty 部署到某测试网，地址记录在案
② 一次 claim 成功（canClaim 从 true → false）
③ 重复 claim 同一 (bountyId, minerId) → revert AlreadyClaimed
④ Requester 用测试网 USDC 直接 transfer 给 miner，链上可见
```

①–④ 齐 → S12-3 从"部分"转 **done**。**合约侧无新代码**（`RelayBounty` 已就绪且已测）。

---

## 5. 诚实的边界（不声称什么）

| 项 | 说明 |
|---|---|
| **不是 escrow** | 合约**不托管**；Requester **可以拿了工作不付钱**。这是"不托管"的代价，**已被接受**（`MVP.md` §6.5 两条独立通道） |
| **不是 x402** | 无逐字包裹、无支付通道协议；**最小** |
| **claim 不等于"已赚"** | 合约只记"谁领过"，**不判断有效性** —— 有效性在链下（签名 + 验证） |
| **依赖外部** | 选链、faucet、部署**都不是代码能做的**；本文是清单，不是自动化 |
