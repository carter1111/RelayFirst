# S1 验收报告

> 按 `DOCS.md` §4.5 的阶段报告模板。**证据优先于结论。**
>
> 日期：2026-10-03
> 阶段：S1（回执 + 签名 + KAT + Verifier）

---

## 交付

| 文件 | 行数 | 作用 |
|---|---:|---|
| `go.mod` | 8 | Go 模块（`github.com/relayfirst/relayfirst`） |
| `internal/eip712/encode.go` | ~430 | EIP-712 编码：`encodeType` / `typeHash` / `encodeData` / `hashStruct` / domain |
| `internal/eip712/sign.go` | ~120 | 签名 + `ecrecover` + 地址/私钥解析 |
| `internal/eip712/helpers.go` | ~60 | JSON 归一化（保住大整数精度） |
| `internal/eip712/kat_test.go` | ~140 | **KAT 跨语言门禁** |
| `internal/eip712/sign_test.go` | ~200 | 签名/验签/篡改检测 |
| `internal/receipt/receipt.go` | ~330 | `Receipt` struct（严格对齐 `MVP.md` §4） |
| `internal/receipt/canonical.go` | ~330 | 确定性 canonical JSON |
| `internal/receipt/receipt_test.go` | ~370 | 结构校验/签名/防篡改 |
| `internal/receipt/verify_e2e_test.go` | ~170 | 离线验证端到端（外部测试包） |
| `cmd/relayfirst/main.go` | ~140 | CLI：`verify` / `id` / `version` |
| `scripts/gen-kat.ts` | ~280 | 用 viem 生成 KAT 基准 |
| `scripts/ci.sh` | ~90 | CI 等价门禁 |
| `testdata/eip712-vectors.json` | 52 vectors | **跨语言契约** |
| `.gitignore` / `package.json` | — | 工程基建 |

**外部依赖（2 个）：** `golang.org/x/crypto`（keccak256）、`github.com/decred/dcrd/dcrec/secp256k1/v4`（secp256k1）。
**npm 依赖（2 个运行时）：** `viem`、`tsx`（仅 KAT 生成）。

---

## 对应验收判据

| 判据 | 状态 | 证据 |
|---|---|---|
| **⑥ 跨语言 KAT 字节相同** | ✅ **达成** | 10 向量 × 3 阶段（domain / message / digest）全过 |
| **⑦ `CGO_ENABLED=0` 构建** | ✅ **达成** | 静态二进制 5.4M，`otool -L` 零 libc 引用 |
| ② 第三方离线验证回执 | ✅ **达成**（S1 时部分） | CLI 离线验签通过。**S1-10 补齐 anchor 重取**（`--refetch`，opt-in），默认路径仍完全离线 —— criterion ② 不被削弱 |
| ④ 伪造工作量被拦住 | 🟡 **部分** | 篡改/生成类任务已拦；**去重与刷量属 S3** |
| ① 10 分钟出积分 | ⬜ 未开始 | S2/S6 |
| ③ 陌生人起节点 | ⬜ 未开始 | S5 |
| ⑤ 对抗验证闭环 | ⬜ 未开始 | S4 |

---

## 实测数字

### KAT 门禁（判据 ⑥）

```
reference implementation: viem, 10 vectors
PASS: TestKAT_Vectors (全部 10 个子用例)
```

三个阶段**分别比对**，所以失败能定位到具体层级（domain / message / digest），而不是只报"哈希不同"。

覆盖的场景（都是实现真正分歧的地方）：

```
minimal                  单字段
relay-receipt-domain     链无关 domain（RelayReceipt 实际形状）
nested-struct            嵌套 struct（encodeType 必须含内层类型）
dynamic-array-uint256    动态数组
dynamic-array-struct     结构体数组（递归 + 数组哈希）
fixed-array              定长数组（长度断言）
long-string              长字符串（分块无关）
empty-values             空字符串 / 空数组 / 零值
multibyte-utf8           中文 + emoji（字节 vs 字符长度）
signed-int-and-address   负 int256 补码 + address 左填充
```

### 测试

```
go test ./... -v
  --- PASS 计数：29（顶层）
  --- PASS 计数：35（子测试）
  FAIL 计数：0
```

### 构建与静态性（判据 ⑦）

```
CGO_ENABLED=0 go build ./...   → exit 0
go vet ./...                    → exit 0
gofmt -l .                      → 空
二进制大小                       → 5.4M
otool -L 中 libc 引用数           → 0（完全静态）
```

### 端到端 CLI 实测

```bash
$ relayfirst id 0x4c08...
agent:eip155:8453:0x2c7536e3605d9c16a7a3d7b1898e529396a65c23

$ relayfirst verify receipt.json
{ "valid": true, "taskType": "probe", "resultValue": "200", "anchors": 1, ... }
```

### 红队（诚实记录：这是 S1 能做的部分）

| 攻击 | 结果 | 输出 |
|---|---|---|
| 篡改 `result.value` 200→500 | ✅ 拒绝 | `signer mismatch: declares 0x2c75... recovers 0x2638...` |
| 篡改 anchor `contentHash` | ✅ 拒绝 | `signer mismatch: ... recovers 0x7d2e...` |
| 任务类型 `probe`→`summarize` | ✅ 拒绝 | `task type "summarize" is not accepted (allowed: probe/extract/compute)` |
| 未签名回执 | ✅ 拒绝 | `receipt has no signature` |
| 未知字段注入 | ✅ 拒绝 | `DisallowUnknownFields` |

**第三行是关键的：** 不变量 A2（只允许有 ground truth 的任务类型）**在代码里强制**，不只是写在文档里。

---

## 实现中发现并修掉的两个真实 bug

> 保留记录，因为它们都是"看起来能用、实际会静默出错"的类型。

### Bug 1：`v` 字节双重偏移（已修）

`decred` 的 `SignCompact` 返回 `[header ‖ r ‖ s]`，其中 **header 已经是 `27 + recoveryID`** —— 正是以太坊的 `v`。我最初按"recoveryID"处理并再加 27，得到 `v = 54`，验签全部失败。

**修正：** 只把 `v` 从首位移到末尾，不加偏移。

**为什么值得记：** 这个 bug 会让**自签自验**失败，很容易当场发现；但如果某处只做签名不做验签（例如只写不读的路径），它可能一直被掩盖到上线。

### Bug 2：数字字符串被当十六进制解析（已修）

原实现在解析裸字符串整数时用 base-16。这会把 `"250"` 静默解释成 `0x250 = 592`。

**修正：** 裸字符串按十进制解析；仅 `0x` 前缀走十六进制。

**为什么值得记：** 这是**静默的错误哈希**，不会抛错。只有 KAT 向量能抓住它 —— 恰好验证了 `CODING_RULES.md` §4 的判断："**没有这组向量，用库也会在半年后炸**"。

---

## 偏差与遗留

### 偏差

| 项 | `TASKS.md` 要求 | 实际 | 说明 |
|---|---|---|---|
| S1-9 verifier CLI | 需验证 anchor 重取 + result 重算 | **S1 时为验签 + 结构校验** | anchor 重取依赖网络，S1 时延后至 S2-5；CLI 当时已注明这一边界。**已于 S1-10 补齐**（见下） |
| — | — | 新增 `internal/devtools/gen_demo.go` | 生成演示回执的脚本（`//go:build ignore`），便于手工验收 |
| — | — | 新增 `scripts/ci.sh` | `CODING_RULES.md` §9 的门禁脚本，S1 未要求但立刻有用 |

**没有隐藏偏差。** 上面三项都是显式选择。

### 遗留（属后续 stage）

```
⬜ artifactKey 计算       → S3-1
⬜ 全局去重账本           → S3-2
⬜ 计分公式               → S3-3
⬜ 对抗验证 + 质押        → S4
```

**已补齐（原列于遗留，后于 S1-10 完成）：**

```
✅ anchor 重取验证        ← 曾记作 "→ S2-5"；S2-5 交付了 internal/mining.AnchorConsistency，
                            但直到 S1-10 才接上 CLI。见下。
```

#### S1-10 补齐：`verify --refetch`

**背景：** S1 的 verifier 只验签 + 结构。这是**刻意的**：重取 anchor 需要网络，
而 acceptance criterion ②（关掉服务器仍可离线验证）要求默认路径**不触网**。

**做法：** 把重取做成**显式 opt-in**，而不是改默认路径。

| 路径 | 验的是什么 | 触网 | 用途 |
|---|---|---|---|
| 默认 | **回执**（签名 + 结构） | ❌ | criterion ②：服务器全关也能验 |
| `--refetch` | **声明**（源现在是否仍返回记录的字节） | ✅ | 发现漂移 |

**为什么必须 opt-in，而不是默认开启：** 重取验的是**声明**不是回执，
且**内容会变** —— 一份诚实回执可能只是过期就失败。这正是验证窗口（`MVP.md` §5.4）
存在的原因。把它设为默认，会用一个**有网络依赖、且会误伤诚实工作**的检查
去削弱 criterion ②。

**输出始终报告 `mode`（`offline` / `refetch`）。** 一个本想重取却漏了 flag 的调用者
必须能看出来；且"没有报错"在什么都没取的情况下不能被读成"证据成立"。

**顺带修掉一个真实缺口：** `parseFlags` 接受任意 flag 名，所以 `--refeth`（笔误）
会被接受、静默走离线路径、并报 `valid`。现已让 `verify` 校验 flag 集合并拒绝未知 flag ——
对这个功能尤其重要，因为整个卖点就是"调用者选择了触网检查"。

**实测（5 条测试，`cmd/relayfirst/verify_refetch_test.go`）：**

```
TestVerify_DefaultIsOffline              → PASS（httptest 断言未被访问）
TestVerify_RefetchAcceptsMatchingAnchor  → PASS
TestVerify_RefetchRejectsDriftedAnchor   → PASS（valid 仍为 true，只有 anchorConsistency 失败）
TestVerify_RefetchUnreachableSourceIsNotAPass → PASS
TestVerify_RefetchRejectsUnknownFlag     → PASS
```

`RefetchRejectsDriftedAnchor` 断言 `valid` **保持 true**：回执是真实的，
只是它的**声明**漂移了。把"漂移"与"伪造"混为一谈，会让复核者去找一个不存在的伪造者。

---

## 下一步阻塞

| 阻塞 | 阻塞 S1 吗 | 状态 |
|---|---|---|
| **BLK-1** 订阅可程序化驱动？ | ❌ **不阻塞**（S1 与订阅无关） | ⬜ 仍未核实 |
| BLK-2 真实消费方？ | ❌ 不阻塞 | ⬜ |
| BLK-3 验证者指派策略 | ❌ 不阻塞（S4 前需要） | ⬜ |

**S2 可以立即开工**，且 S2 的推理额度接入（S2-6）会**直接触碰到 BLK-1**：

- 若先用 **BYO API key**（已规划），S2 可完整跑通。
- 若要走"驱动订阅"，则**必须先解决 BLK-1**。

**建议：** S2 按 BYO API key 推进，BLK-1 并行核实。

---

## 建议的 ADR

`DOCS.md` §4.4 要求"不可逆的 trade-off"写 ADR。S1 实际选定了一项：

**ADR-0001：EIP-712 采用自研薄层，而非 `apitypes`**

- 理由：保持依赖干净（无 `signer/core`）、无 CGO、可读
- 代价：需自行维护 ~430 行编码层 + KAT 向量
- **风险已被 KAT 覆盖**：10 向量与 viem 字节一致，证明自研层正确
- 促发条件：若未来需要支持 `apitypes` 的额外类型（如复杂嵌套数组），需重评

**建议在 S2 开工前补写该 ADR。**
