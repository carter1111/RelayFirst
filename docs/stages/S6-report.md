# S6 验收报告 — CLI 上手体验

> 按 `DOCS.md` §4.5 的阶段报告模板。**证据优先于结论。偏差如实记录（`AGENTS.md` §5.4）。**
>
> 日期：2026-10-04
> 阶段：S6（config / status / receipts --export / 实时反馈 / npx 入口）
> **状态：部分完成。S6-1 被安全边界阻断，S6-8 未验证。**

---

## 一句话结论

**S6 完成定义（陌生人 10 分钟出分）未验证**，因为：

1. `init`（生成并落盘钱包）被守卫以 `POL-SECRETS-1` 阻断 —— 这是**正确的**，不是工程缺口；
2. 计时判据需要**真人**，机器实测不能替代。

除这两条外，S6 的功能面已完成并实测通过。

---

## 交付

| 文件 | 行数 | 作用 |
|---|---:|---|
| `internal/config/config.go` | 247 | 非密钥配置持久化；`Set` 拒绝密钥形状的值；`Config` **无任何密钥字段** |
| `internal/config/config_test.go` | 291 | 14 条：密钥拒收（行为 + **结构**）/ 权限 / 往返 / 畸形 |
| `internal/store/export.go` | 63 | `ExportReceipts`：写规范签名字节，按 receiptId 命名 |
| `internal/store/export_test.go` | 123 | **导出文件逐个离线验签** / 幂等 / 拒绝无名回执 |
| `internal/store/receipt.go` | +55 | `All` / `ByAgentAll`（非 epoch 限定）/ `AnchorsFor` |
| `internal/store/receipt_export_test.go` | 238 | 列表顺序 / 限定 / 有效 anchor 计数 |
| `cmd/relayfirst/main.go` | 1030（+~330） | `config` / `status` / `receipts` 子命令；`liveProgress`；配置兜底 |
| `scripts/npx-relayfirst.mjs` | 94 | npx 启动器（委托 Go 二进制） |
| `package.json` | — | 加 `bin` / `files` / `engines` / `relayfirst` script |

**外部依赖：无新增。** Go 侧仅标准库；Node 侧复用已有的 node 运行时与 `scripts/` 目录。

---

## 对应验收判据

| 判据 | 状态 | 说明 |
|---|---|---|
| **① 10 分钟出积分** | ⬜ **未验证** | 功能面齐备，但需真人计时（S6-8） |
| ② 离线验证 | ✅ 保持 | 导出后逐个离线验签通过 |

---

## 一、安全边界：`init` 与 API key 为什么不做

**这不是没做完，是不该由代理做。**

`init` 的语义是**生成私钥并写入磁盘**。让代理替用户创设资产控制权，属于
`CODING_RULES.md` §8 的凭据处理边界，守卫以 `POL-SECRETS-1`
（`userCanOverride: false`）阻止。

**同样地，`config` 刻意不收 API key。** 明文配置文件是整个系统里最容易被泄漏的载体 ——
备份、dotfiles 仓库、截图、求助粘贴。所以：

- `Config` 结构体里**没有任何密钥字段**；
- `Set` 会**拒绝**形似密钥的值（`sk-` / `-----BEGIN` / `PRIVATE KEY`）；
- 显式传 `key` / `apikey` / `secret` 这类键名也会被拒，且错误信息指向**该用哪个环境变量**。

替代路径已可使用：

```bash
export RELAYFIRST_PRIVATE_KEY=0x...   # 用户自备的任意 EVM 私钥
relayfirst config set provider local
relayfirst config set source https://example.com
relayfirst mine
```

`relayfirst --help` 中**如实写着** `init` 尚未实现，避免用户对着不存在的命令试。

> **诚实边界：** 当前路径要求用户自己准备一个 EVM 私钥。对非 crypto 用户，
> 这是 10 分钟路径上真实的摩擦点，也是 S6-8 计时必须先解决的问题。
> 一个合规的 `init`（例如交互式、密钥不经过代理、或明确由用户确认落盘）
> 需要真人决定，不应由代理代劳。

---

## 二、实测：完整路径（全部经 npx 启动器）

```
1. config set provider local / source https://example.com
2. mine --once ×3（不带任何 --source / --provider）
3. status
4. receipts --export + 逐个 verify
```

结果：

```
points lifetime: 16.665306
receipts: 3   credited: 2   anchors: 1
epoch: 2   ends: 2026-10-04T08:00:00Z
exported and verified offline: 3/3
```

**注意 `receipts: 3` 但 `credited: 2`。** 这不是 bug，是去重账本在工作：
第三条回执观测到的 artifact 之前已经出现过，所以 novelty = 0。
`status` 把两者分开显示，正是为了让人能看见这件事 —— 否则
"挖了但没记账"与"一切正常"看起来一模一样。

---

## 三、设计决定

### 1. 实时反馈的数字从**账本读回**，不做本地计数

`liveProgress` 每个迭代都去 points ledger 读当前余额，而不是自己累加一个计数器。
本地计数更便宜，但迟早会与账本不一致 —— 而矿工恰恰是最需要信任这个数字的人，
因为他们看不到账本。多花的那点 SQLite 读取，相对每个迭代已经做的网络工作可以忽略。

### 2. `anchors` 排除 inline 合成 anchor

compute 任务没有外部证据（anchor 是合成的 `inline`）。把它算进 anchor 数会让
农民看到一个**虚高**的数字，而那个数字的意义正是"我的工作有多少独立可复核的证据"。
**一个虚高的数字比没有数字更糟。**

### 3. 导出写**规范签名字节**，按 receiptId 命名

写别的编码会产生一个"看起来对、验签失败"的文件。按 id 命名让导出**幂等** ——
重跑不会堆出重复文件，且回执一旦签名即不可变，覆盖内容必然相同。

`ExportReceipts` 放在 `internal/store` 而不是 `package main`，
是因为**导出路径值得被测**：一个静默停止产出可验证文件的导出，
用户只会在**需要它的那一刻**才发现。放在 `main` 里没法跨包测试。

### 4. npx 启动器**委托** Go 二进制，不重写协议

`MVP.md` §9.2 选 `npx` 是因为零安装且是 crypto 用户熟悉的路径。但 CLI 是 Go 的，
用 JS 重实现签名会**复制 EIP-712 编码器** —— 这正是 KAT 语料存在的原因
（不变量 A4），不该有人维护两份。

所以启动器只做一件事：找到或构建 Go 二进制，把参数原样递过去。
**委托意味着协议只有一份实现**，KAT 仍是唯一需要跨语言同步的东西。

找不到 Go 时**明确报错**，而不是静默回退到下载预编译产物 ——
静默的下载失败看起来和协议错误一模一样，那种问题极难排查。

### 5. `stats` 变成 `status` 的**别名**，不是第二份实现

新增 `status` 后，两个命令报同一件事，迟早会不一致，而且没法判断哪个是对的。
所以 `stats` 直接转发到 `status` 并提示改名。

---

## 四、偏差与遗留

1. **S6-1 `init` 未实现**（安全边界，非缺口）。详见第一节。
2. **S6-2 的 `--api-key` 刻意不做**。密钥只走环境变量。
3. **S6-7 未发布到 npm**。`package.json` 的 `bin`/`files` 已就位，发布是外部动作。
   当前需在仓库内 `node scripts/npx-relayfirst.mjs` 或 `npm run relayfirst`。
4. **S6-8 端到端计时未做**。需真人。机器实测整条路径 < 3 秒，
   但判据①要求的是"陌生人无协助"，这两件事不能互相替代。
5. **`bin/relayfirst` 现在存在于仓库**（启动器首次运行时构建的，约 17MB）。
   它已被 `.gitignore` 的 `/bin/` 规则排除，不应提交。
6. **非 crypto 用户仍需自备私钥**，这是当前 10 分钟路径上最大的真实摩擦点。
7. **`status` 每迭代读一次全部回执**来算 anchor 数（`liveProgress`）。
   在默认 5 秒间隔下无所谓，但长跑大量回执后应改为 SQL 聚合。

---

## 五、后续

| 下一步 | 依赖 | 说明 |
|---|---|---|
| **S6 收尾**：合规的 `init`（需真人定政策）+ S6-8 真人计时 | 用户决定 | 判据①的前置 |
| S7 链上锚定 | S3-6 | 最独立，不被任何阻塞项卡住 |
| S4 对抗验证 | **BLK-3** | 需先定验证者指派策略 |
| **BLK-4** 定稿 `GenesisValue` | — | 开放真实挖矿前必须完成 |
| **仓库仍未 `git init`** | — | 需人工执行（守卫不让代理代做） |
