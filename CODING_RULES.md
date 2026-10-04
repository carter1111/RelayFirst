# RelayFirst — CODING_RULES.md（编码规范）

> **本文件展开 `MVP.md` §8 的技术选型，并规定 Go / TypeScript 的编码约定。**
> **选型的权威在 `MVP.md` §8（L0）；本文件（L3）只展开细节，不改选型。**
>
> 违反本文件可增补，**但不得违反 `MVP.md` §8**。若本文件与 `MVP.md` 冲突，以 `MVP.md` 为准，并修正本文件。

---

## 1. 三分法（最重要的一条）

**别把技术选型误读成"全自研"。**

| 类别 | 策略 | 例子 |
|---|---|---|
| **① 密码学原语** | **永远用成熟库，绝不自研** | keccak256 · secp256k1 · `ecrecover` · X25519 · ChaCha20-Poly1305 |
| **② 编码 / 协议** | **优先用库**，除非有具体理由 | EIP-712 hashing · A2A 序列化 · HTTP · SQLite |
| **③ 业务数据结构** | **必须自研**（世界上没有现成轮子） | RelayEnvelope · Delegation · Receipt · 计分 |

> **规则：密码学原语永远用库；业务数据结构永远自研；中间那层看情况。**

**"自研"只指第 ③ 类。** 第 ③ 类没有库，因为**它就是 RelayFirst 的产品本身**。

### 1.1 红线

```
❌ 绝不手写：keccak256 / secp256k1 / ecrecover / X25519 / ChaCha20 / HKDF
✅ 必须自研：Receipt / artifactKey 计算 / 计分公式 / 去重账本
```

**手写密码学 = 灾难性安全漏洞。** 没有例外。

---

## 2. 依赖清单（锁定）

| 用途 | 选择 | 类别 |
|---|---|---|
| **keccak256** | `golang.org/x/crypto/sha3` | ① |
| **secp256k1 签名 / 验签** | `github.com/decred/dcrd/dcrec/secp256k1/v4`（纯 Go, MIT） | ① |
| **`ecrecover`** | `decred/dcrd/dcrec/secp256k1/v4`（`RecoverCompact`）或 `go-ethereum/crypto` | ① |
| **EIP-712 hashing** | `apitypes` **或**自研薄层 —— **二选一，都行**（见 §5） | ② |
| **EIP-712 (TS 侧)** | `viem`（MIT） | ② |
| **A2A 序列化** | `a2a-go`（**仅** Agent Card + Task 语义） | ② |
| **SQLite** | **`modernc.org/sqlite`**（纯 Go，无 CGO） | ① |
| **HTTP** | 标准库 `net/http` | ② |
| **CLI** | `github.com/spf13/cobra` | ② |
| **ETH client** | `go-ethereum/ethclient`（**仅**锚定脚本） | ① |
| **日志** | 标准库 `log/slog` | ② |

### 2.1 引入新依赖前

**必须问：**

```text
□ 标准库能替代吗？
□ 是否引入 CGO？（见 §3）
□ 是否属于"手写密码学"的替代品？（那可以引）
□ 是否在 MVP 范围内？（不是就别引）
```

**不需要批准就能引的情况：** 标准库、`golang.org/x/*`。
**需要写 ADR 的情况：** 任何第三方重依赖（如新 HTTP 框架、ORM）。

---

## 3. 无 CGO 约束（硬性）

**整个项目必须能用 `CGO_ENABLED=0` 构建。**

```bash
CGO_ENABLED=0 go build ./...   # 必须 exit 0
```

**理由：** `docker run` 一条命令能跑的前提是**静态单二进制**。CGO 引入 glibc 依赖，破坏这一点，并让交叉编译复杂化。

| 需求 | 必须选 | 不能选 |
|---|---|---|
| SQLite | `modernc.org/sqlite` | `mattn/go-sqlite3` |
| secp256k1 | `decred/dcrd/dcrec/secp256k1/v4` | 需 CGO 的变体 |

**CI 门禁：** 此命令失败即构建失败。

---

## 4. KAT 向量（硬性）

**跨语言 EIP-712 哈希必须字节相同。**

```text
testdata/eip712-vectors.json
  10 用例：嵌套 struct · 动态数组 · 长 string · 空值 · 多字节 UTF-8
  每例含：{ types, domain, message, expectedHash }
```

**CI 门禁：**

```bash
# TS（viem）算 == Go 算 == expectedHash
# 不一致 → 构建失败
```

**生成方式：** 用 `viem` 生成 `expectedHash` 作为基准，Go 侧对齐。

> **有这组向量，自研 `hashStruct` 与用 `apitypes` 的差别只剩约 200 行。**
> **没有这组向量，用库也会在半年后炸。**

**对应验收判据 ⑥**（见 `MVP.md` §11）。

---

## 5. EIP-712：用库还是自研

**两条路都可行。决定因素不是 license，而是 §4 的 KAT。**

| 选项 | 优点 | 缺点 |
|---|---|---|
| **A. `apitypes`** | 零代码；go-ethereum 维护 | 拖入 `signer/core`；LGPL-3.0 |
| **B. 自研薄层（~200 行）** | 依赖干净；可读；无 CGO | 要写代码 + 测 |

**关于 LGPL：** go-ethereum 是 LGPL-3.0。**LGPL 对"链接使用"通常可接受** —— 它被大量商业产品使用。只有**静态链接 + 闭源分发**才需认真对待。

**决策建议：** 不想纠结 → A；在意依赖干净 → B。**选定后写 ADR**（见 `DOCS.md` §4.4）。

**注意：EIP-712 是编码规范，不是密码学原语。**

```text
hashStruct(s) = keccak256(typeHash ‖ encodeData(s))
动态 bytes/string → keccak256(内容)
数组 → keccak256(拼接的 encodeData)
```

规范完全确定，无歧义 —— 所以自研**安全**，但用库也**完全正确**。

---

## 6. Go 编码规范

### 6.1 目录结构

```text
cmd/
  relayfirst-node/     ← 单进程节点入口（最小兼容；SQLite）
  relayfirst/          ← CLI（init / mine / status / receipts / verify）
internal/
  envelope/            ← RelayEnvelope / Receipt 结构 + hashStruct
  state/               ← 确定性状态机（若需要）
  relay/               ← 节点逻辑（store-and-forward）
  discovery/           ← Agent Card / RFN-04 信息文档
  mining/              ← 任务生成 + 执行（probe/extract/compute）
  scoring/             ← artifactKey / 计分 / epoch / 去重账本
  verify/              ← 独立验证器（离线）
  crypto/              ← 签名 / 验签封装（薄，全部委托给库）
  storage/             ← sqlite（modernc）
```

### 6.2 约定

| 项 | 约定 |
|---|---|
| 格式化 | `gofmt` / `goimports`（CI 检查） |
| 错误处理 | **不吞错**；`errors.Is` / `errors.As`；包装用 `%w` |
| 命名 | 缩写保持全大写（`ID`, `URL`, `EIP712`）；不用 `Get` 前缀 |
| 日志 | `log/slog`，结构化字段 |
| 测试 | 表驱动；`_test.go` 同包 |
| 注释 | 导出符号必须有注释，**以符号名开头** |

### 6.3 测试要求

**每个 internal 包必须有测试。** 重点覆盖：

```text
✅ KAT 向量（哈希一致性）      ← 硬性
✅ 回执签名 / 验签 / 篡改检测
✅ artifactKey 去重（第 2 次提交 = 0 分）
✅ 计分公式边界（novelty=0 / quality=0）
✅ 重取验证（篡改 anchor → 拒绝）
```

**不做的事：** 不追求覆盖率数字。**追求"关键不变式有测试"。**

### 6.4 提交约定

```
<type>(<scope>): <subject>

type: feat | fix | test | docs | refactor | chore
scope: envelope | mining | scoring | relay | cli | docs
```

**例：**

```
feat(envelope): add Receipt struct with task/result/verification
test(envelope): add 10 KAT vectors for cross-language parity
fix(scoring): dedupe artifactKey globally instead of per-agent
```

**规则：**

- **每个提交对应一个 `TASKS.md` id**，在正文引用（如 `Refs: S1-5`）
- **KAT 相关改动必须单独成提交** —— 便于回溯
- **不提交** secrets / API keys / `.env`

---

## 7. TypeScript 编码规范

**MVP 只需要 TS 做两件事：**

```text
① KAT 基准生成（用 viem）      ← S1-6
② 可选：客户端 SDK（后期）
```

### 7.1 约定

| 项 | 约定 |
|---|---|
| 运行时 | Node ≥ 20 |
| 包管理 | `pnpm`（或 npm，二选一并锁定） |
| 严格模式 | `strict: true` + `noUncheckedIndexedAccess: true` |
| 格式化 | `prettier` |
| 命名 | 类型 `PascalCase`；变量/函数 `camelCase`；常量 `SCREAMING_SNAKE` |
| 导入 | 具名导入优先；`import type` 用于纯类型 |

### 7.2 关键库

```text
viem          ← EIP-712 hashing / 签名（MIT，业界标准）
```

**绝不用 TS 手写 keccak256 / secp256k1。** 用 `viem` 或 `@noble/hashes`。

### 7.3 KAT 生成脚本

```text
scripts/gen-kat.ts   ← 用 viem 生成 expectedHash 到 testdata/eip712-vectors.json
```

**这个脚本的产物是 §4 的门禁基准。改动它必须单独提交并说明原因。**

---

## 8. 安全与保密

| 规则 | 说明 |
|---|---|
| **不提交 secrets** | API keys / 私钥 / `.env` 一律 `.gitignore` |
| **不硬编码地址** | 地址/token 从 config 读 |
| **不打印私钥** | 包括日志、错误信息、debug 输出 |
| **签名前校验 domain** | 防跨域重放 |
| **积分无转账接口** | 见 `MVP.md` §6.1 —— 积分不可转让，不要实现 transfer |

---

## 9. 提交前自检（CI 等价）

```bash
# 必过
CGO_ENABLED=0 go build ./...     # §3
go vet ./...                      # 静态检查
go test ./...                     # 单测
gofmt -l .                        # 格式（应无输出）

# KAT 门禁（§4）
go run ./cmd/kat-check            # TS 与 Go 对同一向量必须一致
```

**四项全过才算可以提交。**

---

## 附：与 `MVP.md` 的对应

| 本文件 | `MVP.md` |
|---|---|
| §1 三分法 | §8.0 |
| §2 依赖清单 | §8.1 |
| §3 无 CGO | §8.3 |
| §4 KAT | §8.4 |
| §5 EIP-712 二选一 | §8.2 |
| §6 Go 规范 | §8、§12 |
| §7 TS 规范 | §8（"只做 TS"） |