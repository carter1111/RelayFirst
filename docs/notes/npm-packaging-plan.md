# npm 打包计划：跨平台分发（PKG-1 / PKG-2）

> **状态：计划（未实现）。** 目标：`npx relayfirst` 在**没装 Go 的机器**上可用，且**每次只下自己平台的二进制**。
>
> 相关：`package.json`、`scripts/build-npm-binaries.sh`、`scripts/npx-*.mjs`；`TASKS.md §11.2` PKG-1/2；`planning.md §C1`。

---

## 1. 现状（实测）

| 事实 | 值 |
|---|---|
| 打包工具 | `relayfirst` · `relayfirst-mcp` · `relayfirst-dashboard`（**3 个**） |
| 平台 | 5（linux/darwin × amd64/arm64 + windows/amd64） |
| 产物 | **15 个二进制**（`bin/npm/`） |
| **tarball 体积** | **~42MB**（`npm pack --dry-run`） |
| 平台选择 | launcher 按 `process.platform`/`arch` **选已下载的那个** |
| **问题** | 用户**每次为 5 个平台付费**（下载 42MB，只用 1 个） |

---

## 2. 目标形态：**主包 + 每平台 optional 子包**（esbuild / @swc 的模式）

```text
relayfirst@x.y.z                  ← 主包：只含 JS launcher（小，无二进制）
  optionalDependencies:
    @relayfirst/darwin-arm64      ← 只含本平台 3 个二进制
    @relayfirst/darwin-x64
    @relayfirst/linux-x64
    @relayfirst/linux-arm64
    @relayfirst/windows-x64
```

**npm 只安装匹配当前平台 `os`/`cpu` 的 optional 子包** → 用户下载 **~8MB** 而非 42MB。

**关键机制**：
- 子包 `package.json` 带 `"os": ["darwin"]`、`"cpu": ["arm64"]` → npm **自动筛掉**不匹配的。
- 不匹配的包安装**失败被忽略**（`optionalDependencies` 的语义）—— 正是我们要的。
- **`npx` 对子包无感**：用户仍只跑 `npx relayfirst`。

---

## 3. Launcher 的解析顺序（改一处，3 个 launcher 共用）

```text
1. bin/npm/<name>-<os>-<arch>[.exe]        ← 本地构建（保留：开发 & CI）
2. node_modules/@relayfirst/<osDash>-<arch>/<name>[.exe]   ← npm 子包
3. bin/<name> · ./<name>                   ← 源码 checkout 的 go build 产物
4. 回退 go build（有 Go 时）→ 否则报错并**点名平台**
```

**为什么保留 ①**：CI 的 npm 门禁、以及"从源码跑"都依赖 `bin/npm/`；**不能只靠子包**。
**为什么 ② 用 `osDash`**：npm 平台名用 `darwin-arm64`/`linux-x64`/`win32-x64`，与 Go 的 `amd64` 不同
（`x64` ↔ `amd64`，`win32` ↔ `windows`）—— **映射必须有一处**，写在 launcher 里。

> **⚠️ 平台名映射是本计划最易错的地方**：Go 用 `amd64`/`windows`，npm 用 `x64`/`win32`。
> 现有 launcher 已做 `x64→amd64`；子包名要**同时**处理 `win32→windows`。**要有测试**。

---

## 4. 发布流程

```text
scripts/build-npm-binaries.sh            ← 交叉编译到 bin/npm/（不变）
scripts/pack-platform-packages.sh        ← 新增：把每个平台产物复制进 packages/<os>-<arch>/
npm publish packages/<os>-<arch>         ← 先发 5 个子包
npm publish                              ← 再发主包（triggers prepublishOnly）
```

**顺序不能反**：主包 `optionalDependencies` 指向的版本**必须先存在**，否则 `npx` 装不到。

---

## 5. 任务分解

| id | 任务 | 依赖 | 状态 |
|---|---|---|---|
| **PKG1-1** | 平台名映射（Go↔npm） | — | ✅ **done** —— `npm_os`/`npm_cpu`（`windows→win32`、`amd64→x64`）**只在一处**（`pack-platform-packages.sh`）；子包名用 npm 拼法，**launcher 无需映射** |
| **PKG1-2** | 3 个 launcher 加**子包解析**（§3 顺序） | PKG1-1 | ✅ **done** —— `createRequire` + `require.resolve("@relayfirst/<os>-<arch>/package.json")`（**不用 exports**，故能解析）；顺序 `bin/npm → 子包 → 本地建 → go build` |
| **PKG1-3** | `scripts/pack-platform-packages.sh`：生成 5 个子包 | — | ✅ **done** —— 每子包含 3 个二进制（**平名**）+ `os`/`cpu` + `files`；**macOS 子包带 `UNSIGNED.txt`** |
| **PKG1-4** | `package.json` 加 `optionalDependencies`；`files` **移除** `bin/npm` | PKG1-3 | ✅ **done** —— 主包 **8.0kB / 无二进制**（实测），子包 **11.3MB** |
| **PKG1-5** | **CI 门禁**：主包无二进制 + 映射正确 + **launcher 能解析子包** | PKG1-1..4 | ✅ **done** —— 三项新增断言。**变异验证**：`win32-x64` 写成 `windows-amd64` → **门禁 FAIL** |

**实测**：主包 **42MB → 8.0kB**；每平台子包 **11.3MB** → 用户下 **~11MB**（而非 42MB）。
**端到端**：临时目录只放子包（**无 `bin/npm`、无 Go**）→ launcher 跑通 `0.5.0-s6`。

---

## 6. 未决（需你定，不阻塞实现）

| # | 问题 | 建议 |
|---|---|---|
| **Q1** | **npm scope 名**（`@relayfirst/*`？`@carter1111/*`？） | 用与包名一致的 scope；**需 npm org** |
| **Q2** | 平台覆盖：**加 windows-arm64 / linux-riscv？** | 先 5 个主流；有需求再加 |
| **Q3** | **是否同时做 PKG-2（macOS 签名）** | 签名需 **Apple 证书**（外部）→ 先不做，**但要在子包里标明"未签名"** |

---

## 7. 反模式

| ❌ 不要 | 为什么 |
|---|---|
| 只发主包、删掉 `bin/npm` 路径 | 破坏 CI 门禁与源码运行 |
| 把 `optionalDependencies` 写成 `dependencies` | 装不上不匹配平台会让**整包安装失败** |
| 平台名映射散在多处 | `x64/amd64`、`win32/windows` 分歧是**经典 bug**；一处 + 测试 |
| 先发主包再发子包 | 主包指向的版本还不存在 → `npx` 装不到 |
| 声称 `npx` 零依赖却不发子包 | 又回到"42MB 或要 Go 工具链" |
