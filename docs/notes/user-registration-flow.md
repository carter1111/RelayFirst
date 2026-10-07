# User Registration Flow

> 版本：v0.6 — 绑定机制已定（2026-10-07）
> 日期：2026-10-07
> 状态：设计完成，待 ADR-0009 定稿（D2 批准）+ Cursor 实现
> 关联：`incentive.md`（激励机制）、ADR-0004（split-binary）、ADR-0007（回执不可委派）、ADR-0009（agent key 管理，proposed）
>
> 术语（ADR-0009）：**没有 "node key"**——两把 key 是 **agent key**（干活者）与 **verifier key**（验证者角色）；relay 本身永远无密钥（ADR-0004，CI 强制）。绑定机制：agent 以 delegation-1 签意向，verifier 以 assertion-1 确认，不改 signing policy。

## 0. 原则

1. **运作无门槛，赚钱要身份**：relay 无 key 也能跑（dumb relay，只转发）；领奖励才需要身份。
2. **"平台" = 用户本机 CLI**：key 的生成、存储、签名永远只发生在用户本地。服务器碰 key 是红线。
3. **一份助记词，两把独立 key**：HD 派生（agent `m/44'/60'/0'/0/0` + verifier `m/44'/60'/0'/0/1`），用户只备份一份；协议看到两个独立地址。
4. **LLM 永不见 key**：split-trust——LLM（不可信）只产出内容，signer（可信本地进程）签名。
5. **地址 = 身份**：v1 无 key rotation。丢 key = 丢身份；主动换 key = 新身份（旧积分仍可领，见 §4）。

## 1. Key 架构

```
BIP-39 助记词（12词，用户抄纸上，只显示一次）
  └── master seed
       ├── m/44'/60'/0'/0/0  → agent key      → agent 地址（签回执/事件/委托/card，Layer 1）
       └── m/44'/60'/0'/0/1  → verifier key   → verifier 地址（只签 assertion-1，Layer 0）
```

| | 存储 | 密码 | 运行时 |
|---|---|---|---|
| **npm 原生** | `~/Library/Application Support/relayfirst/keystore/`（macOS）或 `~/.local/share/relayfirst/keystore/`（Linux），Web3 Secret Storage 加密 JSON，文件 0600、目录 0700 | OS 钥匙串（Keychain/Secret Service/Credential Manager），无感 unlock | signer daemon，内存持有，mlock，退出清零 |
| **Docker** | **必须挂 volume**：`-v relayfirst-keys:/data/keys`（不挂=删容器丢 key） | 密码文件 mount（0600），**不走 env**（env 泄漏） | 同左，跑在容器内或 sidecar |

- 生成必须用 CSPRNG（Go `crypto/rand`），测试覆盖统计检验。
- **密码设置**（init 流程内）：init 时设置 keystore 解密密码 → 优先存 OS 钥匙串；Linux 无头机无钥匙串时，降级为密码文件（0600，Docker secret 或 mount）❌（fallback 细节待定）。
- **Signer daemon 生命周期**：由 `node start` / `mine` 按需拉起；首次使用需解锁（钥匙串/密码文件）；机器重启后需重新解锁；崩溃后 key 只在内存，重启即清零，需重新解锁。建议提供 `relayfirst signer status`（proposed）。
- Signer 只暴露本机 API，**需 bearer token 或 Unix socket 权限**（ADR-0009 D5：纯 localhost 不够，任何本地进程都能连）。无 key-export 接口。备份唯一通道 = init 时的助记词。
- **Signing policy（安全关键）**：per-key EIP-712 domain 白名单——agent key：`relayfirst:1`（receipt/card）· `event-1` · `delegation-1`；verifier key：**只** `assertion-1`。拒绝 raw hash、不在白名单的 domain、结构不符的 payload、盲签；每笔签名打日志。理由：被 prompt injection 的 LLM 看不见 key 但能调 signer，policy 是第二道墙。

## 2. Flow A：Relay / Verifier 注册

> 关键澄清（ADR-0009）：relay（中继）与 verifier（验证者）是**两个角色**。relay 永远无 key；"Node 注册"实际分两段——先跑无身份的 dumb relay，再按需接入 verifier 身份拿 Layer 0。

```
阶段一  无身份运行（零门槛）
        $ npm i -g relayfirst        # 或 docker run ...
        $ relayfirst node start
        → dumb relay：可转发消息，无奖励，无需任何 key

阶段二  接入 verifier 身份（要赚 Layer 0 才做）
        $ relayfirst init
        → [1] 生成新助记词  [2] 导入已有助记词（迁移，BIP-39 checksum 校验；错词报错指位）
        → 设置 keystore 密码 → 存钥匙串（或密码文件，见 §1）
        → 显示 12 词（只一次）→ **抽查**："请输入第 3、7、11 个词"（答对才继续；"我已备份"打字确认不算验证）
        → 派生 verifier key（m/44'/60'/0'/0/1）→ keystore/verifier.json（0600）
        → 输出 verifier 地址: 0xabc…（即 Layer 0 的领奖身份）
        → 幂等：已存在则拒绝覆盖（--force 需交互确认）
        → 中断恢复：init 写一半崩溃 → 下次检测到半写文件，提示清理后重来，不静默覆盖
        → Docker：确认 volume 已挂载，否则 abort 并报错

阶段三  verifier 工作
        → relay 配置关联该 verifier 身份 → 开始签 attestation → tenure 累积 → Layer 0 奖励（incentive.md §3）
        → 身份可用于 RFN-04 收费声明、RFN-12 声誉（未来）
```

## 3. Flow B：Agent 注册

```
Step 1  获取 agent key（三选一）
        [a] 本机已有助记词（已 init）：直接派生 m/44'/60'/0'/0/0，无需新备份 ✅推荐
        [b] 没有：走完整 init（生成新助记词，同时派生两把；备份抽查同 §2）
        [c] 导入已有助记词（迁移，checksum 校验）
        ❌ 不允许：复用 verifier key（同一地址又当 verifier 又当 agent——角色混淆，归因作废）

Step 2  keystore 落盘
        → keystore/agent.json（0600）

Step 3  signer 持有，LLM 隔离
        → receipt 内容由 LLM 产出 → signer 校验 schema → 签名 → 提交
        → 断言：私钥材料永不进入 LLM 进程（无 env、无 CLI 参数、无 MCP 返回）

Step 4  运行挖矿
        $ relayfirst mine
        → 无 key 时：任务照做，收据不交（隐身干活）；首次提示 [是否现在创建钱包？Y/n]
        → 有 key：收据签名提交 → Layer 1 积分 + SBT 工作徽章

Step 5  PoSR 绑定（拿 1.25×）
        ✅ 机制（2026-10-07 定，ADR-0009 D7）：$ relayfirst bind → agent 以 delegation-1 签绑定意向（"我的 work 经由 verifier Y 提交"）→ verifier 以 assertion-1 确认服务关系。不改 signing policy。
        → one-binding-per-identity，不叠加（incentive.md §4）。attestation 具体格式待设计。
```

## 4. Flow C：换 key / 恢复 / 迁移

| 场景 | 操作 | 已赚积分 | SBT | Tenure | 未来 earning |
|---|---|---|---|---|---|
| 主动换 key（有旧 key） | 生成新 key = 新身份 | **还在**，旧 key 可 claim | 留在旧地址 | 从零 | 新身份重赚 |
| 丢 key | — | **领不出**，真没了 | 留在旧地址，与你无关 | 从零 | 新身份重赚 |
| 换机器（迁移） | 旧助记词 → 新机器 import | 跟着身份走，不受影响 | 同上 | **保留**（身份没变） | 继续 |

- 用户文案（必须写进 UI）：**"换 key 不致命（钱还在），丢 key 才致命。备份保的是领钱权，tenure 才是真正丢不起的。"**
- **双轨：助记词（默认）+ 私钥（高级）** ✅（2026-10-07 决策）
  - **默认轨（助记词）**：生成、备份、迁移、导入全走 BIP-39。HD 派生，一份备份管所有 key。普通用户只见这一轨。
  - **高级轨（raw private key）**：
    - `init --import-key`：导入单把私钥，需 `--role agent|verifier` 指定用途。导入的 key 是独立单 key，不参与 HD 派生（私钥反推不出助记词）。
    - `relayfirst export --address 0x…`：导出指定地址的私钥（HD 派生或导入的均可），交互式强确认 + 警告（"持有此 key 即控制该身份"）。用途：HSM / 外部签名器迁移。
    - 标注"高级用户"：默认关闭，不进主流程文档首页。
  - **原则**：私钥 hex 默认不出现在用户眼前；signer daemon 依然无 export API（export 是 CLI 本地特权操作，直接解密 keystore 文件，不走 signer）。
- 导入 hygiene：no-echo 读取、不进 shell history、不打日志；助记词是 HD 树的备份单位（一把私钥备份不了一棵树）。

## 5. 多设备 / 多 Relay / 共 key

### 5.1 多设备

- **Agent key：可以**。助记词 import 到第二台设备即可。收据是 EIP-712 签名声明（无 nonce），多设备并发签名无冲突；同一身份，work 累加，5% cap 共用。每台设备跑自己的 signer daemon。
- **Verifier 身份：一 verifier 身份 = 一台活跃 relay**。两台机器共用同一 verifier 身份跑 relay = attestation 归因混乱。
  - **例外（合法）**：HA 主备 failover——同一 verifier 身份，一主一备，不叠加奖励，只提高 liveness。这是推荐的高可用做法。
- **多 relay 的正确姿势**：`relayfirst init --index <n>` 派生第 n 个 verifier 身份（`m/44'/60'/0'/0/<n>`，n=2,3…；n=1 是首个）——一份助记词备份，N 个独立 verifier 身份。机群运营者也可每机独立 `init`（运营隔离）。
- **Shared fate 警告**：多设备共用助记词 = 一损俱损。某设备沦陷无法单独吊销（v1 无 rotation），只能弃身份。不可信设备不要导 key。

### 5.2 同机多 relay

- **有奖励**：每个 relay 配独立 verifier 身份、独立 tenure、独立 Layer 0 份额。
- **但自收敛**：① 固定池稀释——全网 relay 越多每份越薄；② liveness——每个 verifier 身份独立过 challenge，机器休眠则全挂；③ 边际成本实在（CPU/带宽），边际收益递减。
- **1.25× 只绑一个**：agent 的 PoSR binding 指向单一 relay/verifier 身份。
- **不用 IP 反女巫**：IP 是弱信号（NAT/VPN），误伤正常用户。靠经济设计（tenure + 稀释），不靠 IP 指纹。

### 5.3 共 key（多 relay / 多 agent 同一把 key）

- **是，都给该 key——但这是惩罚，不是奖励。** 协议按地址记账：
  - 多 relay 同 verifier key → tenure 归因混在一起 → **一份** Layer 0（不翻倍）；多出来的 relay 隐形。
  - 多 agent 同 key → 只看到一个 agent → work 累加**共用 5% cap**（更快触顶），1.25× 一份。
- **结论**：共 key 是自我惩罚，协议不需要禁止——激励已经指向正确做法（分 key）。唯一合法例外是 HA 主备（§5.1）。

## 6. 安全要求汇总（给实现）

1. CSPRNG 生成；HD 派生路径固定（BIP-44）。
2. Signer signing policy：per-key domain 白名单，拒绝盲签（§1）。
3. 私钥材料永不进入 LLM/网络进程；signer 无 export API；signer API 需 bearer token/Unix socket 权限。
4. 文件权限：keystore 0600、目录 0700、密码文件 0600。
5. Docker volume 为启动前置检查项（未挂载则拒绝 init）。
6. 助记词备份必须抽查验证，不接受打字"我已备份"。
7. 所有"不"：不进浏览器、不粘贴到聊天框、不走 env、不上服务器。

## 7. 未决 / Roadmap

- ❌ ADR-0009 定稿：D2（实现 `init`，推翻 no-init）待用户批准。
- ✅ PoSR bind 机制（2026-10-07 定）：agent delegation-1 意向 + verifier assertion-1 确认（ADR-0009 D7 已改写收录）；attestation 具体格式待设计。
- ❌ OS 钥匙串库选型（go-keyring vs 自研）+ Linux 无头机密码 fallback spec。
- ❌ `init --index` 多 verifier 派生的 CLI spec（含 `--role` 校验）。
- ❌ Docker 密码文件 vs Docker secrets 细节。
- ❌ 多设备同步（roadmap）：助记词是当前唯一跨设备方案。
- ❌ 硬件钱包支持（roadmap）：热 key 定位不变。
- ❌ v2 key rotation registry（合约，未排期）。

## 8. 实现难度评估

> 结论：技术上不难——全是成熟积木（BIP-39/44、keystore、EIP-712、Merkle），无发明项。真正的成本在**安全 review**（signer policy）和 **UX 细节**（备份流程、Docker 坑）。

| 模块 | 难度 | 说明 |
|---|---|---|
| HD 钱包 / keystore / init CLI | 低 | go-ethereum `accounts/hd` + `accounts/keystore`，调库 |
| OS 钥匙串 | 低-中 | `go-keyring`；麻烦在 Linux 无头机 fallback |
| Docker volume | 低 | 主要是文档 + 启动警告（容器内可靠检测 volume 较 trick） |
| Signer daemon + policy | 中 | 实现不难，**安全关键**——policy 写错=后门，review 预算放这里 |
| `mine` 接线 | 中 | 集成活，件都是现成的 |
| Claim 合约 | 中-高 | 链下简单；贵的是 Solidity + 审计费 |

- 预估：熟悉代码库的 Go 开发，wallet+signer+init+bind 约 **1–2 周**（claim 随 TGE，不急）。
- 对比：S13（E2EE）、S11（SBT 合约）已做完，都比这个难。

## 9. 领钱（claim 衔接）

> 注册→赚钱→领钱的最后一公里。细则见 `incentive.md` §4 "Claim UX"，此处只定衔接。

- **两种 key，两笔 claim**：agent key 领 Layer 1，verifier key 领 Layer 0。`relayfirst claim` 一次处理本地所有身份。
- **earning 地址固定，payout 地址自由**：`--to` 指定收钱地址，可与身份地址不同。
- **CLI 内完成**：签名在本地 signer，key 不出本机。
- 注册流程的终点不是"开始挖矿"，而是"能领到钱"——任何 onboarding 文档必须包含 claim 步骤。

## 10. Changelog

- **v0.5**（2026-10-07）：ADR-0009 对齐——术语统一为 agent key/verifier key（删"node key"）；Flow A 重写为两段式（无身份 dumb relay → 按需接入 verifier 身份）；补 8 项：助记词抽查验证、密码设置与钥匙串 fallback、signer 生命周期与认证、init 中断恢复、导入 checksum 校验、`init --index` 多 verifier 派生、PoSR bind 待重设计标注、新增 §9 claim 衔接；§7 未决更新（ADR-0009 待 D2 批准）。
- **v0.4**（2026-10-07）：新增 §8 实现难度评估（全成熟积木；review > 实现；1–2 周预估）。
- **v0.3**（2026-10-07）：双轨制落字——助记词（默认）+ 私钥（高级：`--import-key` 需指定 role、`export` 交互强确认；signer 依然无 export API）。
- **v0.2**（2026-10-07）：新增 §5 多设备/多节点/共 key（agent key 可多设备、node key 一机一 ID、HA 主备例外、HD 派生多 node、同机多节点自收敛、共 key 自我惩罚）。
- **v0.1**（2026-10-07）：完整流程版。确立 HD 单助记词双 key、Docker volume 生死线、signer policy、换 key/丢 key 区分、PoSR 绑定步骤。
