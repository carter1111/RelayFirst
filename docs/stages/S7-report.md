# S7 验收报告 — 链上锚定（便宜版）

> 按 `DOCS.md` §4.5 的阶段报告模板。**证据优先于结论。偏差如实记录（`AGENTS.md` §5.4）。**
>
> 日期：2026-10-04
> 阶段：S7（Merkle tree + `RelayAnchor.sol` + proof 生成/验证 + 跨语言语料）
> **状态：代码层完成并通过跨语言门禁；链上未部署（刻意）。**

---

## 一句话结论

Merkle 锚定层已实现，且 **Go、Solidity、viem 三方对同一数据得出同一 root**。
**没有任何东西上链** —— 没有节点、没有资金账户、没有部署权限，而这些都不是
应该由代理去做的事。

---

## 交付

| 文件 | 行数 | 作用 |
|---|---:|---|
| `internal/merkle/merkle.go` | 336 | RFC 6962 域分离 + keccak256 + 2 的幂补齐；`Root` / `Prove` / `Verify` |
| `internal/merkle/merkle_test.go` | 321 | 18 条：确定性 / 顺序敏感 / 篡改检测 / **域分离** / 1–33 叶全覆盖 |
| `internal/merkle/vectors_test.go` | 189 | **Go 侧对已提交语料的门禁**（不变量 A4） |
| `contracts/RelayAnchor.sol` | 197 | `submitRoot` / `verifyProof` / `foldProof`；**无结算、无奖励、无 owner** |
| `contracts/test/RelayAnchor.t.sol` | 313 | 15 条：**跨语言一致性** + 合约行为 + 篡改拒绝 |
| `contracts/test/MerkleVectors.sol` | 664 | **生成文件**：语料作为 Solidity 常量 |
| `testdata/merkle-vectors.json` | 863 | 规范语料（10 棵树，每叶一 proof） |
| `internal/devtools/gen_merkle_vectors.go` | ~300 | 一次计算，**同时**产出 `.json` 与 `.sol` |
| `cmd/relayfirst/main.go` | +~230 | `anchor root` / `anchor proof` / `anchor verify` |
| `cmd/relayfirst/main_test.go` | 209 | **该包首个测试**：叶子选取、排序可复算、参数校验 |
| `foundry.toml` | ~14 | Foundry 配置；`libs = []`（无依赖） |
| `scripts/ci.sh` | +~35 | **Merkle 语料新鲜度** + `forge test` 两道门禁 |

**外部依赖：无新增。** Solidity 侧**不依赖 forge-std**（见下）。

---

## 对应验收判据

| 判据 | 状态 | 说明 |
|---|---|---|
| **④ 伪造工作量被拦住**（anchor 侧） | ✅ 推进 | 篡改叶/兄弟/root/索引**全部被拒**；Go 与 Solidity 都测 |
| ⑥ TS/Go KAT 字节相同 | ✅ 扩展 | 新增 **Merkle** 跨语言语料，且 CI 校验新鲜度 |

**S7 完成定义核对：**「回执可被链上证明，成本可忽略」→ 🟡 **代码层达成，链上未验证**。

---

## 一、跨语言一致性：这是本阶段的核心

一个**只和自己一致**的验证器什么也证明不了。锚定的全部价值在于
**独立实现**能得出同一 root —— 否则链上验证失败，而每个单侧测试都通过。

### 三方实测（同一批 5 条真实回执）

```
Go      root = 0x2af45716d0d9fdd3b1f52e542ecaef2ee367e59a5dd2a8cacb99f18718c3d395
viem    root = 0x2af45716d0d9fdd3b1f52e542ecaef2ee367e59a5dd2a8cacb99f18718c3d395
Solidity      → forge test 的 test_ProofsMatchGoGeneratedVectors PASS
```

viem 那一行是**额外的**独立检查（不在 CI 内）：
它用的是完全无关的 JS 实现，仍得出同一 root。

> **过程中我自己的第一次 viem 复算得出了不同的 root，而那是脚本的 bug** ——
> 把 `toBytes`（hex→bytes）和 `concatHex`（hex 拼接）混用，叶子被算成了
> `"0x00"` 的 **ASCII 字节**而非字节 `0x00`。
> 修好后立刻一致。记下来是因为：**不一致时先怀疑自己的复算脚本**，
> 而且这类"文体类型混用"的 bug 不会报错，只会静默给出另一个 hash。

### 为什么语料有两份产物

`internal/devtools/gen_merkle_vectors.go` **一次计算**同时写出：

- `testdata/merkle-vectors.json` —— 给 Go（及任何其他语言）
- `contracts/test/MerkleVectors.sol` —— 同一数据的 Solidity 常量

Solidity 侧本可以用 cheatcode 读 JSON，但那需要引入 cheatcode 接口 **+** 一个 JSON 解析器进测试，
比被测对象本身还复杂。**从同一次运行产出两种格式**，把"两个语言比对的数据来自同一来源"
变成结构事实，而不是约定。

---

## 二、Merkle 构造：为什么用标准构造，以及两处刻意的替换

`A1` 禁止手写密码学原语，而这个道理**同样适用于用它搭出来的结构**：
一棵形状微错的 Merkle 树**完美地验证自己的证明**，所以错误在你自然会写的每个测试里都看不见。

本实现遵循 **RFC 6962** 的域分离方案：

```
leaf(id)         = keccak256(0x00 ‖ id)
node(left,right) = keccak256(0x01 ‖ left ‖ right)
root()           = keccak256("")            // 空 epoch
```

**那一个前缀字节是全部要点。** 没有它，内部节点 `keccak256(l ‖ r)` 与叶 `keccak256(id)`
取值空间相同，攻击者就能**把一个两元素子树的内部节点当成单个叶**提交，
从而为一枚从未进过树的回执拿到有效证明。

### 替换 1：keccak256 而非 SHA-256

RFC 6962 规定 SHA-256。但**在链上验证证明正是这棵树存在的理由**，
而 EVM 的原生 hash 是 keccak256。若坚持 SHA-256，就得在 Solidity 里手写一个 SHA-256 ——
**恰恰是要避免的那种自定义密码学**。

`golang.org/x/crypto/sha3`（EIP-712 已在用）提供 keccak256，**未引入新依赖**，
且让项目只有**一份** keccak 实现，回执签名与 Merkle 树不可能彼此漂移。

### 替换 2：补齐到 2 的幂

RFC 6962 对奇数叶按"最大 2 的幂处分割"来平衡。规范清晰，但**树的形状依赖于叶数**，
两个语言很容易实现得不一样 —— 而形状不一致是**静默的**，直到某个证明失败才暴露。

改为**补齐到 2 的幂**后，两边都只有一种无歧义的形状：每个内部节点都有两个孩子，
每个证明长度都是 log2(n)，验证是一个带索引的单层循环。填充叶用**全零 receipt id 的叶**，
选它是因为它不可能与真实回执碰撞（回执 id 是签名载荷的 hash，造出全零需要原像攻击）。

**代价：** root 会承诺补齐后的宽度，所以验证者需要知道该 epoch 的回执数。
这可以接受 —— 该数字可以从"用来重算 root 的同一批回执"推导出来。

---

## 三、`RelayAnchor.sol`：刻意做小，以及为什么不按原样写

### 实际 197 行，不是 `TASKS.md` 写的 ~50 行

核心逻辑确实很小（`submitRoot` + `foldProof` 各十几行）。行数来自：

- 按 operator 命名空间隔离（见下）
- 自定义 `error`（比 revert string 省 gas 且信息更全）
- 解释**为什么不做某件事**的注释

**我没有为了凑数字删掉注释或命名空间。** 那两样都是承重的。

### 与 `MVP.md` §4.3 的偏差：mapping 的类型

文档写：

```solidity
mapping(uint256 epoch => bytes32 root)
```

**这无法编译**：mapping 需要**键类型 + 值类型**两个参数，而 `uint256 => bytes32`
在这里是**一个**类型对、却被放在了"键"的位置。作者的意图显然是
"epoch 为键、root 为值"。

实现为 `mapping(uint256 epoch => bytes32 receiptsRoot)`。

### 额外的偏差：按 operator 隔离

单层 `mapping(uint256 => bytes32)` 是**共享的**：任何人 `submitRoot(epoch, x)`
都能覆盖任何人的 root，anchor 立刻失去证据价值。

所以实现为：

```solidity
mapping(address operator => mapping(uint256 epoch => bytes32 receiptsRoot)) public roots;
```

这是**最小**修法，且让合约保持**无 owner、无 admin、无注册表** ——
这里没有特权方，这是刻意的。

### epoch 不可覆盖

同一 `(operator, epoch)` 只能写一次。一个 epoch 是**已完成**的工作窗口；
允许改写等于允许事后修改，**而那正是锚定要防止的事**。
若真需要取代某个锚定，那应该是**新的 epoch**，不是编辑。

### 明确不做

合约里**没有** `settlement` / `escrow` / `claim` / `withdraw` / `dispute`，
也**不给提交者任何奖励**。任何"能依据回执转移价值"的逻辑都会关掉
"永不发币"的退路（不变量 A5）。

---

## 四、Solidity 测试不依赖 forge-std

惯例是 `import "forge-std/Test.sol"`，但那会拉进一个必须**联网获取**的依赖。
而被测性质是**对已提交语料的纯计算** —— 不需要 cheatcode、不需要 fork、
断言也不超出普通 `require`。

去掉依赖后，Foundry 项目只有两个源文件，测试可**离线运行**。

代价：`test_OperatorsAreNamespaced` 无法用 `vm.prank` 真正模拟第二个 operator。
我**没有假装做到了** —— 该测试改为直接断言命名空间不互相别名，
并在注释里写明这一点。

---

## 五、CLI：只算值，不提交

```
relayfirst anchor root   --db ... [--epoch N] [--show-ids]
relayfirst anchor proof  --db ... --receipt <id>
relayfirst anchor verify --db ... --receipt <id> --index N --width N --sibling H ...
```

**`anchor root` 与 `anchor proof` 只打印值，绝不签交易、绝不连链。**
产出这些值必须**完全正确**；提交它们要花钱且不可逆，属操作者。

`anchor verify` 会**自己从 store 重算 root**，而不是信任 proof 携带的 root ——
否则这个命令只是在验证"proof 与其自身一致"，毫无意义。

叶子排序按 **receipt id**，不按插入时间。插入时间是写入者本地的，
按它排序会让 root **只有写入者能复算**，那就不叫锚定了。有测试锁定这一点
（同一集合的三种输入顺序 → 同一 leaf 序列）。

---

## 六、证据

### 门禁

```
./scripts/ci.sh            → 7/7 PASS
  CGO_ENABLED=0 go build   → PASS
  go vet                   → PASS
  gofmt                    → PASS（空）
  go test ./...            → PASS
  KAT corpus               → PASS (53 vectors)
  Merkle 语料新鲜度         → PASS (10 trees, .json 与 .sol 同步)
  forge test               → PASS
```

### 测试数

```
go test ./... -count=1 -v   → 289 PASS / 0 SKIP / 0 FAIL（S6 时为 258）
forge test                  →  15 PASS / 0 FAIL
```

### 新鲜度门禁非空转（实测）

```
篡改 testdata/merkle-vectors.json 的一个 root → 门禁 FAIL
还原                                          → 门禁 PASS
```

一个**不会失败**的门禁等于没有门禁，所以这条是实测的。

### CLI 端到端（真实回执）

```
anchor root    → epoch 2, receipts 4, width 4, root 0x8807dd...
anchor proof   → index 0, width 4, 2 siblings, 自带自检通过
anchor verify  → verified: true（root 从 store 重算）
篡改 sibling   → verified: false + 原因
```

---

## 七、偏差与遗留

1. **链上未验证。** 无节点、无资金账户、无部署权限。**未部署、未提交任何 root。**
   `forge build` 与 `forge test` 通过，但"真链上跑通"**没有发生**。
2. **S7-3（每日提交 root）未做**，且**不应由代理做**：那是签交易、付 gas。
   需要一个人（或一个 cron）持有资金账户。
3. **`RelayAnchor.sol` 197 行 vs 计划 ~50 行**，原因见第三节，属刻意。
4. **已记录两处与 `MVP.md` §4.3 的偏差**（mapping 类型、per-operator 命名空间）。
   **`MVP.md` 尚未同步** —— 按 `AGENTS.md` §5.1，文档与实现偏差应记录；
   这里记在报告与 `TASKS.md`，`MVP.md` 的修订留给用户确认，因为 §4.3 是 L0。
5. **viem 复算不在 CI 内。** 它是一次性人工检查，不是门禁。若要常态化，
   应加入 `scripts/ci.sh`（依赖 `node_modules` 里已有的 viem）。
6. **`anchor` 命令每次读全部回执**（`receipts.All(0)`）。对本地规模无所谓，
   但回执量大了应改为按 epoch 过滤的 SQL 查询。
7. **空 epoch 的 root 是合法的**（`keccak256("")`），但 CLI 会**警告不应锚定它** ——
   锚一个空 epoch 是笔无意义的链上花费。
8. **补齐宽度必须被验证者知道。** 这是一个**真实的可用性代价**：
   验证者只拿到 proof 不够，还需要该 epoch 的回执数/宽度。合约把 `width` 存在链上，
   CLI 把 `width` 放进 proof 输出，所以链路是闭合的 —— 但值得知道。

---

## 八、用户需要自己做的事（部署步骤）

**代理没有做、也不能做这些。** 完整步骤如下：

```bash
# 1. 编译（无需网络）
forge build

# 2. 部署 —— 需要你自己的资金账户与 RPC
forge create contracts/RelayAnchor.sol:RelayAnchor \
  --rpc-url "$YOUR_RPC" \
  --private-key "$YOUR_KEY"     # 经环境变量或 keystore 传入，别写进 shell history

# 3. 为某个 epoch 算 root
relayfirst anchor root --db ./relayfirst.db --epoch 2 --show-ids

# 4. 提交（写一次，不可改）
cast send "$DEPLOYED" "submitRoot(uint256,bytes32,uint256)" \
  2 "$ROOT" 4 --rpc-url "$YOUR_RPC" --private-key "$YOUR_KEY"

# 5. 之后任何人、无需许可、**无需任何 store**，都可验证自己的回执
relayfirst anchor proof --db ./relayfirst.db --receipt "$RECEIPT_ID"
relayfirst anchor check --receipt "$RECEIPT_ID" --root "$ROOT" \
  --index "$INDEX" --width "$WIDTH" --sibling "$SIB1" --sibling "$SIB2"
```

**第 5 步是锚定的真正意义。** `anchor check` **不读本地数据库、不连任何服务器** ——
只拿你公布的 root 与 proof 就能算出包含关系。命令输出里会写明这一点：

```json
{
  "verified": true,
  "mode": "against a supplied root, no local store consulted",
  "note": "this check used no RelayFirst server and no local receipt store; ..."
}
```

`--root` **刻意不从 proof 里读取**：proof 自带一个 root 字段，若用它来验证就是循环论证 ——
伪造的 proof 只要带上它自己能折叠出的 root，检查就永远通过。

实测（5 枚回执的 epoch）：

```
无 --db 通过                → verified: true
换成错误的 root             → verified: false, "the proof does not fold to the supplied root"
```

> **提醒：** 上面的 `$YOUR_KEY` 应由**你自己**填入，经环境变量或 keystore。
> 不要把私钥粘进任何命令、脚本或截图。

---

## 九、后续

| 下一步 | 依赖 | 说明 |
|---|---|---|
| **S7 收尾**：真人部署 + 提交一个 root | 用户资金账户 | S7 完成定义的最后一米 |
| `MVP.md` §4.3 修订（mapping 类型 + 命名空间） | 用户确认 | L0 文档，按 `AGENTS.md` §5.1 需用户改 |
| **S4 对抗验证** | BLK-3（已选**机制优先**） | 机制可先做，指派做成可插拔接口 |
| **S6 收尾**：合规 `init` + 真人计时 | 用户定政策 | 判据①前置 |
| **BLK-4** 定稿 `GenesisValue` | 用户 | **注意**：epoch 起点变化会改变 epoch 号，从而**改变每棵树的成员** |
| **仓库仍未 `git init`** | 用户 | 守卫不让代理签提交 |
