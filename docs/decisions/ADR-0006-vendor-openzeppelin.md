# ADR-0006: 引入 OpenZeppelin（第一个 Solidity 依赖）

- 状态：**accepted**（2026-10-04 裁定：vendor OpenZeppelin）
- 日期：2026-10-04
- 影响层级：**L0**（`foundry.toml` 的依赖策略；`MVP.md` §8.1 未列 Solidity 库）
- 来源：S11（SBT 积分徽章）实现时的依赖决策

---

## 背景

**仓库此前刻意零 Solidity 依赖：**

```text
foundry.toml:  libs = []
注释:          "No forge-std, so no dependency tree to fetch."
lib/           不存在
```

`RelayAnchor.sol` 是一个 197 行的自包含合约，**不 import 任何东西**。
这个选择有明确理由：合约是**部署后不可改**的，每多一个依赖就是**多一份别人可以改动的代码**进入你的不可变产物。

**但 S11 需要 ERC-721。** 而 ERC-721 不是"随便写写就行"的接口：

| 风险 | 说明 |
|---|---|
| 接口面大 | `transferFrom` / `safeTransferFrom` / `approve` / `setApprovalForAll` / `ownerOf` / `balanceOf` / `tokenURI` / `supportsInterface` … |
| 接收者回调 | `_safeMint` 必须调 `onERC721Received` 并**校验返回 selector**，写错会**永久锁死** token |
| 已知陷阱 | `safeTransferFrom` 的重入面、`approve` 的竞态（ERC-721 规范本身讨论过）、`_update` 的 hook 位置随版本变化 |
| 钱包依赖 | 钱包按标准行为展示 NFT；一个"大致对"的实现会让徽章**显示不出来或显示错** |

**手写 ERC-721 的真实成本不是行数，而是"你成了唯一审计者"。** 这与 §8.0 三分法一致：
ERC-721 属于**类别 ②（编码/协议）** —— "**优先用库，除非有具体理由**"。这里没有反向理由。

---

## 决定

**引入 OpenZeppelin Contracts，vendored 到 `lib/openzeppelin-contracts`，固定 tag `v5.7.0`。**

**"vendored" 的含义（重要）：** 代码**进仓库**，不做 `forge install` 的动态拉取。
理由：合约是**不可变产物**，构建必须**可复现**。一个"构建时从网络拉版本"的流程，
会让同一个 commit 在不同时间构建出**不同字节码** —— 而字节码决定了链上行为。

**具体范围（sparse checkout，不是整仓）：**

```text
lib/openzeppelin-contracts/contracts/token/ERC721/
lib/openzeppelin-contracts/contracts/token/common/      （ERC2981 等被引用的部分）
lib/openzeppelin-contracts/contracts/utils/
lib/openzeppelin-contracts/contracts/interfaces/
```

**实际使用到的只有 `ERC721`（含其继承链）。** 其余是传递依赖。

---

## 备选与取舍

| 选项 | 优点 | 缺点 |
|---|---|---|
| **A. Vendor OpenZeppelin v5.7.0**（裁定） | 工业标准、审计最充分、钱包兼容性最好；版本固定，构建可复现 | 打破 `libs=[]`；仓库 +2.5M；引入 OZ 的继承链 |
| B. 自实现最小 ERC-721 | 保持零依赖；代码量可控（~200 行） | **你成为唯一审计者**；`_safeMint` 的 receiver 校验、`approve` 竞态等陷阱要自己踩；未来每个改动都是链上不可逆的 |
| C. 引入 solmate / solady | 比 OZ 小，gas 更优 | 同样新增依赖；生态与审计面积小于 OZ；`solady` 的激进优化牺牲可读性 |

**为什么 A 而非 B，尽管 B 保持了项目的既有取向：**

ERC-721 **不是密码学原语**（那是"永远用库"），也**不是业务数据结构**（那是"永远自研"）。
它在中间层，判据是"**除非有具体理由**"。而这里的理由**指向用库**：

> **合约不可变，所以依赖选择的不对称性在于"写错的代价付不起"。**
> 手写 ERC-721 省下的是 2.5M 仓库体积，赌上的是"一个不可升级的合约里有一个你没发现的转账漏洞"。

**A 与 B 不是等价的 trade-off**，这一点必须写清楚 —— 否则这个决定看起来只是"图省事"。

---

## 后果

1. **`foundry.toml`**：`libs = []` → `libs = ["lib"]`，新增
   `remappings = ["openzeppelin-contracts/=lib/openzeppelin-contracts/"]`。
2. **`lib/openzeppelin-contracts` 进仓库**（~2.5M）。**必须提交**，否则构建不可复现。
3. **`RelayPoints.sol`** 使用 `ERC721`；**`IERC5192` 自实现**（2 个函数，且是别的工具读的标准形状，
   不需要从 OZ 引入）。
4. **CI 门禁不变** —— 现有 13 道全绿（含 `forge test`）。新增的合约测试纳入 `forge test`。
5. **A5 合规审计已覆盖新合约**（S11-7）：`compliance_audit.go` 的 `defaultTargets` 加入
   `contracts/RelayPoints.sol`。**实测立刻捕获了一处**（见下方"执行中发现的问题"）。
6. **`MVP.md` §8.1 需要补一行 Solidity 库**（原表只有 Go/TS 侧选型）。

**不可逆的部分：**

- **`RelayPoints` 一旦部署，OZ 的代码就是链上产物的一部分。** 换 OZ 版本 = 部署新合约。
  这正是固定 tag 的理由。
- **若日后要把 OZ 移出仓库**，必须同时解决"构建可复现"的问题（否则改的是构建确定性）。

---

## 执行中发现的问题（如实记录）

**A5 守卫在覆盖新合约的第一次运行就 FAIL：**

```text
FAIL  contracts/RelayPoints.sol:15  "promised return"  — promises a return
```

**原因不是违规，而是断行：** 合约注释写的是

```text
Points are unpriced and carry no
promised return.
```

而 `internal/compliance` 的 scan 是**按行**的（`TestScan_ScopeIsPerLine` 明确锁定这个语义），
所以**跨行的白名单短语匹配不上**。

**处理**：把白名单短语**放在同一行**（措辞未改）。**没有改守卫的语义** ——
放宽成"跨行匹配"会削弱按行的精确性，而那个精确性有测试在守。

**这是守卫的一个已知局限，值得记住：** 白名单短语若被断行，会**误报**。
方向是安全的（误报而非漏报），但会浪费复核时间。

---

## 证据

```text
foundry.toml                   —— 原 libs = []（零依赖）
lib/openzeppelin-contracts     —— v5.7.0，sparse checkout
contracts/RelayPoints.sol      —— 使用 ERC721；自实现 IERC5192
contracts/interfaces/IERC5192  —— 2 个函数
forge test                     —— 27 passed（含 12 项新合约测试）
scripts/ci.sh                  —— 13/13 全绿
compliance_audit.go            —— defaultTargets 含合约；实测捕获一处断行误报
内部参考：internal/compliance/claims.go:150 —— 白名单含 "carry no promised return"
```