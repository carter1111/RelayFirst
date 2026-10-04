# ADR-0005: CLI 框架 —— 实测为手写，修正选型表

- 状态：**accepted**（2026-10-04 裁定：写 ADR 修正选型表，不引入 cobra）
- 日期：2026-10-04
- 影响层级：**L0**（`MVP.md` §8.1 选型表）
- 来源：外部评审清单 P3 第 11 条（清单中称 "ADR-3"）

> **⚠️ 编号说明：** 外部清单称之为 "ADR-3"，但 `docs/decisions/` 已有 `ADR-0003-dedup-ledger-scope.md`。
> 为避免两个 `ADR-0003` 共存，本文件正式编号为 **ADR-0005**（下一可用序号）。
> 清单中 "ADR-3" ↔ 本文件的对应关系在此写明，以免日后对不上。

---

## 背景

`MVP.md` §8.1 的选型表把 CLI 框架定为 **`github.com/spf13/cobra`**（类别 ②：编码/协议层，"优先用库"）。

**但实测与选型表不符：**

```text
cmd/relayfirst/main.go    1938 行，手写
grep -c 'spf13/cobra'     → 0（全仓库无导入）
go.mod                    → 无 cobra
```

**CLI 的真实形态：**

| 维度 | 实测 |
|---|---|
| 子命令 | 10 个（`verify` / `id` / `mine` / `stats` / `config` / `status` / `receipts` / `anchor` / `verify-receipt` / `version`），参数用 `switch` 分派 |
| 标志 | ~20 个（`--key` / `--db` / `--relay` / `--epoch` / `--export` / `--refetch` …），手写 `strings.HasPrefix(arg, "--")` 解析 |
| 嵌套子命令 | 有（`anchor root|proof|verify|check`、`config set|get|show`），同样用嵌套 `switch` |
| 依赖 | **零**（标准库 + 本项目内部包） |

---

## 决定

**采纳既有事实：CLI 保持手写，并修正 `MVP.md` §8.1 选型表。**

**为什么不引入 cobra（论证，而非"懒得改"）：**

| 理由 | 说明 |
|---|---|
| **1. cobra 的依赖树不空** | cobra 依赖 `spf13/pflag`（POSIX 风格 flag）。引入即多两个第三方依赖，而本项目**其余部分刻意保持极窄依赖**（见 §8.3：SQLite 选纯 Go 驱动正是为了 A3） |
| **2. 本项目已有一条"用库还是手写"的判据** | `MVP.md` §8.0 的三分法：**密码学原语永远用库**（因为写错是灾难）；**业务数据结构永远自研**（因为没有现成轮子）；**中间层"优先用库，除非有具体理由"**。CLI 属于中间层 —— 所以这里需要的是**具体理由**，而下面第 3–5 条就是 |
| **3. 规模不匹配** | cobra 解决的是"命令树很大、需要自动补全/help 生成/man page"的问题。本项目 **10 个扁平子命令 + ~20 个 flag**，用 cobra 等于用一个命令树框架管一个 switch |
| **4. 已有一个真实的 CLI 已建成并测试** | `relayfirst` 的 `--help` 输出、错误信息、`--key` 的**安全约定**（不经命令行、只走 env，见 `main.go:113`）都已成型。重写到 cobra 是**纯搬迁风险**，收益是把 1938 行换成 1938 行 + 2 个依赖 |
| **5. 手写解析的一个具体好处** | 现在的 `strings.HasPrefix(arg, "--")` 版本**能在 flag 出现前就拒绝它**，并给出本项目的自定义错误措辞（例如"a command-line flag would leak a key into shell history"，`main.go:113`）。cobra 的错误措辞是框架的，要覆写反而更多代码 |

**⚠️ 但这不是"手写永远更好"。** 触发重新评估的条件（写在下面，避免这个决定变成教条）：

```text
若出现以下任一情况，重新评估 cobra：
  ① 子命令树超过 ~20 个，或出现三层以上嵌套
  ② 需要 shell 自动补全（bash/zsh/fish）
  ③ 需要从同一份定义生成 man page 或文档
  ④ 出现第二个需要共享 flag 定义的二进制（如 relayfirst-verifier 需要复用 --key/--chain-id）
```

**④ 已经不是假设** —— `cmd/relayfirst-verifier`（S10-0）**已经复制**了 `--key` / `--chain-id` 的解析逻辑。
**因此本 ADR 明确记下**：这是手写方案的**第一个可见代价**，若**第三个**二进制再复制一次，天平就该倾斜。

---

## 备选与取舍

| 选项 | 优点 | 缺点 |
|---|---|---|
| **A. 保持手写，修正选型表**（裁定） | 零依赖；已有 CLI 与测试不动；错误措辞可控；与项目"窄依赖"取向一致 | 复制 flag 解析（已发生一次，见触发条件 ④） |
| B. 引入 cobra 并重构 | 生态标准；自动 help/补全 | +2 依赖；纯搬迁风险；`--help` 输出与错误措辞全部要重写并重新测试；与 §8.0"中间层优先用库"的精神一致但**缺乏具体理由** |
| C. 引入轻量替代（`pflag` 单用，或 `urfave/cli`） | 只补 flag 解析，不接管命令树 | 同样是新增依赖；且**当前 flag 解析未出过问题**，属于"没问题先修" |
| D. 什么都不做（选型表继续与实现不符） | — | **不可接受**：选型表是 L0，与实际不符会让后续每个读者做出错误决策（这正是本 ADR 存在的原因） |

---

## 后果

1. **`MVP.md` §8.1 已修正**：CLI 框架一行由 `spf13/cobra` 改为"**标准库手写**"，并注明理由与触发条件。
2. **`TASKS.md` ADR-3 标 done。**
3. **不引入任何新依赖**，`go.mod` 不变。
4. **记录一项已知代价**：`relayfirst` 与 `relayfirst-verifier` 目前各自手写 flag 解析。
   若出现第三个二进制，**必须先评估是否抽公共解析层或改用 cobra**，不得再复制第三次。

**不可逆的部分：** 无。本 ADR 记录的是**已经发生的事实**，改变的是文档而不是代码 ——
所以它没有引入任何技术债，只是停止让文档说谎。

---

## 编号冲突的处理

`docs/decisions/` 现存：

```text
ADR-0001-eip712-in-house-thin-layer.md
ADR-0002-semantic-extract-as-inference-cost.md
ADR-0003-dedup-ledger-scope.md      ← 上一轮新增
ADR-0005-cli-framework-handwritten.md ← 本文件
```

**`ADR-0004` 被 `ADR-0004-node-identity-crypto.md` 占用**，而外部清单称本文件为 "ADR-3"。
两个来源的编号因此都对不上。**处理方式：本文件取下一可用序号 ADR-0005**，
并在 `DOCS.md` 按实际序号登记。**清单里的 "ADR-3" 就是本文件**，对应关系记在此处。

## 证据

```text
MVP.md §8.1                —— "CLI 框架 | github.com/spf13/cobra | ② | 生态标准"（已修正）
cmd/relayfirst/main.go     —— 1938 行，switch 分派 + strings.HasPrefix 解析
grep -rn 'spf13/cobra' .   —— 0 处
go.mod                     —— 无 cobra / pflag
cmd/relayfirst-verifier/   —— S10-0 新增，同样手写（触发条件 ④ 已发生一次）
```