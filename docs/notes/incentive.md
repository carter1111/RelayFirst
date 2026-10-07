# SBTRelayFirst 激励机制（incentive.md）

> 版本：v0.21
> 日期：2026-10-07
> 状态：**未达到开发标准**。本文只定框架，参数逐步填满到可开发版本。
> 关联：`MVP.md`（§5.3/§6.2 Model B）、`ARCHITECTURE.md`、`planning.md`、`docs/decisions/`（ADR-0007）

## 0. 图例

- ✅ 已定（locked，不重议）
- 🔶 框架已定，参数待上线前定稿
- ❌ 未决，见 §10 未决清单

## 1. 设计原则 ✅

1. **可验证性不对称**（BLK-5 结论）：协议只为可验证的工作付费（EIP-712 回执），不为中继字节/uptime 付费。这是承重墙。
2. **不强制，但鼓励 bundle**：纯 Agent 是默认 onboarding（零摩擦）；跑节点是"多赚"，不是"门票"。
3. **预算固定**：每 epoch 固定预算 + 衰减发行，无计划外增发。
4. **每层独立防女巫**：经济层乘在可验证 work 上；SBT 不可转让 + 时间门槛；补贴有界 + sunset。
5. **用户故事一句话**："跑节点，你的 work 多算 25%；节点熬得久，有基础收入和徽章。"

## 2. 分层总览

| 层 | 名称 | 给谁 | 形式 | 状态 |
| --- | --- | --- | --- | --- |
| Layer 0 | 节点基础收入（可靠性奖励） | 可靠节点运营者 | points，小固定池 | 🔶 |
| Layer 1 | 工作奖励（含 PoSR 乘数） | 干活的 Agent | points，主池 | ✅/🔶 |
| Layer 2 | 声誉 | Agent + 节点 | SBT，不可转让 | ✅/🔶 |
| （市场层） | 手续费 | 收流量的节点 | RFN-04，运营者定价，协议预算外 | 🔶 |

**预算总览** 🔶：分阶段动态比例——**预先锁定，按 epoch 高度自动切换**，无需治理投票（可预测性 > 灵活性）：

| 阶段 | Epoch | Work pool | Node base pool | 意图 |
| --- | --- | --- | --- | --- |
| Phase 1 点火 | 0–25（约6个月） | 50% | 50% | 重赏节点，冷启动 |
| Phase 2 生长 | 26–51（约6个月） | 75% | 25% | 网络稳定，重心转回工作 |
| Phase 3 成熟 | 52 起 | 90% | 10% | 补贴最小化，手续费接棒 |

（epoch = 7 天 ✅（2026-10-07 定）；Phase 切换写死在结算逻辑里（P1 epochs 0–25 ≈ 6 个月，P2 26–51 ≈ 6 个月）。12 个月 sunset 评审时决定 Layer 0 去留，见 §3。）

**资金流向图**（每 epoch）：

```
  epoch 预算 B_n
      ├── Work pool (P1 50% / P2 75% / P3 90%)
      │     └── share_i = work_i × m_i / Σ(work_j × m_j)  [m_i=1.25 绑定 / 1.0]
      │           └──→ points_i ──┐
      └── Node pool (P1 50% / P2 25% / P3 10%)
            └── tenure-tier 加权 ──→ points_i ──┘
                                                  │
                              ┌───────────────────┘
                              ▼
                    积分账本 (SQLite, 不可转让)
                              │
              ┌───────────────┼───────────────┐
              ▼               ▼               ▼
         burn 换服务      持有待 TGE      (未来)治理
      (命名空间/折扣/     Merkle claim
        优先级)           → token (可交易)

平行轨道（不经过积分）：
  SBT 轨道：tenure / 工作史 → 链上徽章 → gating + TGE 加权（永不转 token）
  USDC 轨道：S12 bounty → 人与人之间支付
```

## 3. Layer 0：节点基础收入（可靠性奖励）🔶

**定位**：bootstrap 补贴。奖励的是**可靠性**，不是"存在"。

- 资金：独立小池，比例按阶段预设（P1 50% / P2 25% / P3 10%），不随节点数增发 🔶
- 资格：tenure ≥ 3 epochs（连续在线）+ 通过 liveness 抽查 🔶
- **"连续在线"定义** 🔶（2026-10-07 proposed）：
  - 每 epoch 划分 N 个 slot（epoch=7 天 ✅，slot=10 分钟 → 1008 slots；slot 时长待定）。
  - Verifier 每 slot 签一次轻量 attestation（心跳）；liveness 随机 challenge 抽查（协议待设计）。
  - 单 epoch 达标：在线 slot ≥ 95%，且单次连续掉线 ≤ X slots（X 待定，如 18 slots ≈ 3 小时；防"每天定时掉线"的游戏）。
  - Tenure 计数：连续达标的 epoch 数。单 epoch 不达标 → tenure 降一档（不清零，减少运维误伤）；连续两 epoch 不达标 → 清零 ✅（2026-10-07 定）。
- 分配：**tenure-tier 加权**（50% 池子下纯均分对矿场太友好；2026-10-07 压缩至最大 25% 差距——tier 是忠诚奖励，不是护城河）🔶
- 3–5 epochs：权重 1×
- 6–11 epochs：权重 1.1×
- 12+ epochs：权重 1.25×
- `share_i = weight_i / Σ(weights) × pool`
- 不按流量分配——按流量即回到 BLK-5 的不可验证陷阱 ✅
- Liveness：周期性轻量 challenge ❌（协议待设计）
- **Gas 归属** ✅（原则，2026-10-07）：心跳 attestation 不上链（off-chain 聚合，relay/settler 收集；1008 slots × ~200B ≈ 200KB/周/verifier，SQLite 无压力）；链上只有每周一次的 Merkle root 发布 → 谁记账谁付（P1 即团队，L2 上 52 笔/年，可忽略）；里程碑徽章走 EAS off-chain attestation，无 gas（真上链的 trophy 才需 mint，低频，届时定 payer）。
- Sunset：12 个月后由治理决议延续/取消；到期无决议则自动停止 ✅（原则）🔶（流程）
- 反女巫：固定池（单 epoch 损失有界 = 当期池比例）+ tenure 时间成本 + tier 加权（新女巫权重最低）+ phase-down 预设（奖金按已知时间表缩水，长期矿场 ROI 被压缩）+ sunset 安全阀 + 同机多节点自收敛（稀释+liveness+边际成本）✅（框架）
- 不用 IP 反女巫：IP 是弱信号（NAT/VPN），误伤正常用户。靠经济设计，不靠 IP 指纹 ✅（原则）

**为什么 tenure 是核心**：熬过 3 个 epoch 的节点 = demonstrated reliable。tenure 在这里不只是反女巫，更是可靠性证明——这正是要补贴的东西（笔记本节点会休眠，VPS 节点才是可靠中继）。

❌ 待定：阶段比例起步值、3 epochs 门槛、challenge 协议、slot 参数、VPS farm ROI 建模验证（§10.3，**launch-blocking**）

## 4. Layer 1：工作奖励 ✅/🔶

**Model B**（已定，`MVP.md` §5.3/§6.2）：

- 每 epoch 固定预算 `B_n = 7,000,000 × 0.85^⌊n/4⌋`（n 为 epoch 序号；即每 4 周衰减 15%）✅（2026-10-07 定）
  > 首年发行约 1.64 亿（28M × (1−0.85¹³)/0.15）。farm ROI 建模（§10.3）的前置参数已齐。
- 每回执计算 `work`（work 函数见 MVP §5.3）✅
- 份额公式：`share_i = work_i × m_i / Σ(work_j × m_j)` ✅
- 单 agent 上限：合计 **5%** ✅
  > ✅ cap 余量（2026-10-07 定）：销毁——未分配预算不增发。
- 结算：每回执 `RecordWork`（保留原始明细 + artifactKey，可审计）→ epoch 末显式 `relayfirst settle`（幂等，`settle:<epoch>:<agent>`）→ 写 points → Merkle root（settled map 即 root 输入，确定性）✅
- **显示层**（2026-10-07 需求）：积分显示至少每分钟刷新。实现为"待结算预估"（读 `work_records` 聚合，**不是**提前结算）：用户自查是本地索引查询，成本可忽略；全局榜单由记账侧每分钟聚合一次 + 缓存分发（10k 节点规模单次聚合百毫秒级）。UI 必须区分"已结算（Merkle 可验）"与"本 epoch 待结算预估"。写负载不增加（写仍是每回执 RecordWork + epoch 末 settle）。

**积分的性质** ✅（v1.0 已定）/ 🔶（TGE 细则待定）

- 积分**现在不是 token**：它是链下账本数字（SQLite `work_records` + `settle`），无 gas、无合约 ✅
- 每 epoch 结算后发布 Merkle root（链上存证或 verifier 多签），用户凭 Merkle proof 独立验证自己的积分 ✅（root 计算 D1 已实现；发布机制 ❌待定）
- 积分**不可转让**（协议不提供转账功能）——它是贡献记账，不是钱 ✅
- TGE 时按公布比例经 Merkle claim 转为 ERC-20 token（复用 S11 的 claim 模式）🔶

**Airdrop claim 机制** 🔶（TGE）
- 快照：TGE 时对累计积分做快照 → 总 Merkle root → claim 合约（D1 的每 epoch root 是基础；快照即跨 epoch 求和）🔶
- 领：用户提交 Merkle proof（"我有 X 积分"），合约链上读 SBT tenure → `tokens = X × r × (1 + tenure_bonus)` → 发放 ✅（公式框架）
- 防重领：合约 bitmap/nullifier，已领不可重领 ✅（标准做法）
- 关键：**是"证明"不是"花掉"**——prove 即领，不 burn；领完积分历史和 SBT 徽章都在 ✅（原则）
- **SBT 的角色：乘数，不是票据** ✅（原则）
  - 公式 `tokens = X × r × (1 + tenure_bonus)` 中，X（积分）是基数，SBT 推导的 tenure_bonus 是乘数。
  - 无 points 只有 SBT → 领不到（理论 edge case；实际中 tenure 与 Layer 0 points 伴生）。
  - 无 SBT 只有 points → 按 `X × r` 照领，只是没加成。
  - 不给徽章直接定价的原因：SBT 已在三处变现——① L0 tier 加权（每 epoch 多拿 points）；② TGE tenure 加成；③ 未来 perks（折扣/治理）。再直接兑换 = 为同一贡献付三次钱（double/triple counting）。
- TGE 后：积分可继续累积（下一季快照）或切换为 token 直接发放——属 post-TGE tokenomics，框架预留 ❌

**Claim UX** 🔶
- **两种 key，两笔 claim**：agent key 领 Layer 1 的 points，node verifier key 领 Layer 0 的 points。`relayfirst claim` 一次处理本地所有身份，token 打到 `--to` 指定的 payout 地址（可与 earning 地址不同——赚的身份固定，收钱地址自由）。
- **签名 CLI 优先，展示 Web 优先**（2026-10-07 调整）：签名永远在 CLI（key 不出本机）；web 做展示 + claim 发起。key 进浏览器 = split-trust 设计白做，此红线不变。
- 网站 Phase 2：只读 dashboard（查积分/SBT/tenure）+ claim 发起页，签名动作仍在 CLI 完成。
- **Web claim 设计**（2026-10-07 proposed）：不用"连接钱包"按钮——key 在本地 CLI signer 里，不在浏览器钱包里，connect 是误导。用 **ticket 配对**：① CLI `relayfirst claim --web` → 一次性 ticket/二维码（5 分钟过期）；② Web 输入 ticket 拉取该地址待领数据（只读，不碰 key）→ 展示每 epoch 明细、可领总量、tenure bonus 试算、token/gas 预估；③ 用户回 CLI `relayfirst claim --ticket X` → 本地 signer 签名 → 提交；④ Web 显示 claim 历史。红线：web 永不接触私钥/助记词；ticket 一次性+短过期；金额二次确认。约束：claim 那一刻 CLI 必须可达（手机-only 用户需回电脑操作；deferred claim 待议）。❌ 待定：gas 代付（relayer）、多身份聚合 UI。
- Gas：claim 合约放低成本 L2；gas 策略（用户自付 / relayer 代付）待定 ❌。
- SBT 不用 claim（已在链上/EAS）；只有 points→token 走 claim 流程。
- 为什么不上链 ERC-20：① gas——每 epoch 给几千个 agent mint，谁付？② 灵活性——上线前 work 函数/cap/比例都要调，链上即固化；③ 过早可转让 = 过早投机 + 证券监管风险 ✅（原则）
- **对开发的影响**：激励机制上线**不需要**为积分部署任何合约；要做的是 Merkle root 发布（每 epoch 一笔或多签），TGE claim 合约是后面的事 ✅

**积分怎么花** 🔶

- 核心约束：积分不可转让 → "花" = **烧（burn）**，不是转账。想付钱给**人**，用 USDC（S12）或未来的 token。
- **原则：积分管"赚和烧"，钱管"付和收"。** 积分负责激励对齐（earn/burn loop），不承载人与人之间的价值转移——那是钱的事。
- 用途：

1. **TGE 兑换**（主出口）：按公布比例转成可交易 token ✅（框架）
2. **Relay ID / 命名空间注册**：burn points 注册（S10-0 chargeable namespace 的自然 sink——想要名字，烧积分）🔶
3. **手续费抵扣**：出示 points 余额（Merkle proof）换 relay 费折扣，或 burn 换 voucher 🔶
4. **优先级/配额**：burn 换中继优先级、API 配额 🔶
5. **（未来）治理权重**：SBT 主导，points 为辅 ❌（待设计）

- 反垃圾选项（未定）：提交回执烧微量 points，valid 回执返还——待评估是否误伤诚实 worker ❌

**谁记账** 🔶

- 现状：跑 `relayfirst settle` 的一方记账（D1 已标注为部署问题）——即"平台记账" ✅（现状描述）
- 但它是**可验证的记账**，不是要信任的记账：回执自签名（EIP-712）+ work 函数确定性公开 + settled map→root 确定性可复算 → 任何人可独立重算验证。记账员发布假 root 会被当场发现 ✅（性质）
- 去中心化路径：P1 团队记账（诚实标注"渐进式去中心化"）→ P2 verifier 多签 root（D3 的 verifier 名单即天然 co-signer 集）→ P3 无许可结算（optimistic challenge / ZK，远期）🔶
- 未解：settler **审查（omission）**——agent 自持签名回执可证明"我被遗漏了"，但强制纳入机制待设计 ❌（→ §10.10）
- 核心原则：**记账可以中心化，验证必须去中心化。**

**Points 安全吗？** ✅（性质）/ 🔶（发布机制待定）

- **被盗**：积分不可转让——无转账功能即无可盗之物。比 token 更安全 ✅
- **伪造 proof**：用户伪造不了 Merkle proof（有效性由哈希原像/碰撞抗性保证），也改不了余额（无转账功能，无写入口）✅。真风险在别处：① settler 发假 root 可被任何人用自持回执重算发现，但强制纳入机制未定（§10.10）；② root 发布锚点未定——锚点落地前"独立验证"是无根之木；③ 钓鱼前端给你看假 root（缓解：root 以链上/多签发布为准，客户端硬编码校验源）。**注意**：Merkle 保证的是完整性（结算结果没被改），不是真实性（输入的回执是真的）——用户能往输入塞垃圾回执（自签刷 work，靠 work 函数 + verifier 防），但改不了出口数字。Garbage in, garbage out。
- **被篡改**：Merkle root 确定性可复算，假 root 当场被发现 ✅
- **平台跑路**：root 上链（每 epoch，低成本 L2）+ 回执自持 + 多 relay 备份 → 记录不死。且结算是确定性的——**settler 是可替换的**，任何人可拿回执 + work 函数接管结算 ✅（性质）🔶（root 发布链/机制待定，见 §10）
- **被稀释**：反女巫设计（cap、tenure、绑定）+ §10.3 farm ROI 建模 ✅（框架）
- **归零**：TGE 前积分无美元价是设计，不是 bug；价值来自 TGE + 真实需求（§"积分怎么花" 的 sinks + 手续费需求）✅（原则）

**为什么不统一成一个再空投** ✅（已评估，不采用）

- 统一上链 = gas 问题重现：每 epoch 给几千个 agent 更新链上分数，成本无解；降频更新（月结）= 用贵的方式重造 Merkle root。
- flow/stock 区分不消失："一个分数"是 points 换皮，"有价值的徽章求和"是用贵的方式重造 points。
- 一次性空投比连续结算**更易撸**：单次快照标准必然被针对性优化；按 epoch 连续发行 + decay + cap 更平滑。
- 其实"按数值空投"已经在做了：TGE 转换就是空投，只是拆成了**每 epoch 的连续空投**——你的直觉是对的，形式上我们选了更难撸的那种。

**PoSR 乘数**（与 Layer 1 同池，非独立池）🔶：

- `m_i = 1.25`（work 经自己节点提交 + 双签绑定）或 `1.0` ✅（框架）
- 绑定注册：agent 身份 + verifier 身份双签，一次性。出租 relay 的 verifier key = 交出身份 + tenure（裸奔，经济上非理性）✅（原则）🔶（UX 待设计）
  > ✅ 机制（2026-10-07 定，ADR-0009 D7）：沿用现有 domain，不改 signing policy——agent 侧以 delegation-1 签绑定意向（"我的 work 经由 verifier Y 提交"），verifier 侧以 assertion-1 确认服务关系（"为 agent X 提供中继"）。
- 不叠加：一个身份最多一份加成 ✅
- 零 work = 零加成（乘数，不是工资）✅
- 相对恒成立：同等 work 量，绑定者永远比未绑定者多 25% ✅（数学）
- 绝对值稀释：全网绑定率 Q 上升，绝对加成变薄；Q=100% 时人人 1.25 = 没人 1.25 ✅（数学，见 §6 算例）

内部名：Proof of Self-Relay（PoSR）；对外只说"节点加成 / 跑节点多赚 25%"。

**任务供给：P1 的工作从哪来** 🔶（2026-10-07 proposed）
> 缺口：文档此前未定义 bootstrap 期"工作"的来源。可验证 ≠ 有价值——需求侧缺失则 Layer 1 为可验证的忙碌付费。
- **P1（补贴期）**：官方任务池 + 接受自派任务。
  - 官方池：团队/协议发布的 dogfooding 任务（开发、测试、文档、审计复查）。任务入库可查，有明确验收标准。
  - 自派任务：矿工自己找活干（如用 Codex 做开源贡献）。诚实标注为补贴期获客成本。
  - 自派防刷（待 Cursor 论证）：任务去重（artifactKey/内容哈希）、复杂度下限（work 函数最小阈值）、同一模板限领。
- **P2**：开放任务市场（S12 USDC bounty）+ A2H（人发布需求）——真正的需求侧。
- ❌ 待 Cursor 论证：官方池发布/验收流程、自派防刷参数、work 函数是否按任务来源加权。

**绑定与身份规则** ✅（原则）/ 🔶（实现待定）
- **地址 = 身份**：v1 无 key rotation 机制。丢 key = 丢身份（积分/SBT/tenure stranded，链上徽章还在但与你无关）。重新开始 = 新身份，历史不迁移（tenure 从零——这本身是反女巫特性）。Claim 时 payout 地址可自由指定（赚的身份固定，收钱的地址自由）。
- **One-binding-per-identity**：每个 agent 身份最多绑定一个 node 用于 1.25×，不叠加。
- **多 node / 多 agent 矩阵**：
  - 一人多 node：每个 node 独立赚 Layer 0（tenure-tier 加权）；1.25× 只绑其中之一。
  - 一 agent 绑多 node：❌ 禁止。
  - 多 agent 绑同一 node：✅ 允许，各自独立 1.25×（女巫由 work 验证 + 5% cap 防，不由排他性防）。
  - 同一把 key 兼 verifier + agent：不禁止，不推荐（`init` 默认生成两把独立 key；混用则丢 key 两边一起丢）。
- **Key 分离安全**：verifier key 被盗 ≠ agent key 被盗（偷 verifier 签不出 agent 收据）；绑定声明单方不可伪造（机制待定，见上）；出租 relay = 交出 verifier key = 交出身份 + tenure（裸奔，经济上非理性）。
- **共 key 自我惩罚**：协议按地址记账——多 node 同 key 只算一份 Layer 0（不翻倍），多 agent 同 key 共用 5% cap（更快触顶）。激励天然指向分 key，无需协议禁止 ✅（原则）。
- **残余风险**：丢 key 无恢复（v1 最大 UX 安全债）；farm ROI 建模 pending（§10.3，多 node 是主攻向量）；未来 claim/SBT 合约需审计。

❌ 待定：X 最终值（25% 起步）、绑定注册 UX（§10.5）

## 5. Layer 2：声誉（SBT）✅/🔶

- **Agent 工作史徽章**：S11（ERC-721 + ERC-5192，不可转让）✅ 已实现
- **Node operator 徽章**：tenure / reliability 等级（铜/银/金）🔶（2026-10-07 定 tier 映射：铜 = tenure ≥3 epochs，银 = ≥6，金 = ≥12；与 Layer 0 tier 对齐）
- 先发后 gate：Phase 1 只记录、不 gate 任何奖励（不跟 bootstrap 打架）✅（原则）
- 发放挂钩可验证存活信号，**不接受自证** ✅（原则）
- 未来用途：手续费折扣、relay 目录（RFN-12）排序、治理权重、tenure-weighted 未来分配（§8）

**概念澄清：SBT ≠ 积分** ✅

- **积分（points）**：同质、可计数、当期结算、可花（未来可兑换/抵扣）。= 工资。
- **SBT**：非同质、不可转让、绑定身份、只增不减（除吊销）、**不可兑换成积分**。= 军功章 / 履历。
- 为什么必须分开：SBT 的安全属性恰恰来自"不可变现"——一旦可兑换，时间门槛即崩（可买号），女巫防御即失效。
- 两者只有两种关系，且都不等于兑换：① **gating**（如 Layer 0 要求 tenure tier）；② **加权**（如未来 TGE 按 tenure 加权）。徽章本身永远不可花。

**可交易性与三者关系** ✅

- **积分（现在）**：不可交易（协议无转账功能）→ TGE 时经 Merkle claim 转为 token
- **Token（未来 ERC-20）**：**可交易**——这是它的本职：价值交换、流通、定价
- **SBT（永远）**：不可交易、不可兑换为 token——它的本职是身份
- 关系链：`积分 --(TGE / Merkle claim)--> token`；`SBT --(gating / 加权)--> 影响 token 分配`；SBT 本身永不变成 token
- 一句话：**token 回答"你赚了多少"，SBT 回答"你是谁"**。缺一不可：无 token，无人因经济而来；无 SBT，无忠诚可言，治理可被资本女巫收购（纯 token 治理 = 价高者得）。

**为什么不能统一为一种** ✅

- 统一成 SBT（全不可转让）：杀死经济飞轮——A2H 的"人赚到美元"、挖矿的流动性激励、TGE 全没了。**徽章不能花。**
- 统一成积分（全同质数字）：杀死可验证履历——Agent LinkedIn 要的是"你做过什么"（结构化），不是"你有多少分"（一个数字）。卖掉积分 = 删掉历史。
- Flow vs Stock：积分是**流量**（赚→花/转 token），SBT 是**存量**（永久累积）。硬捏成一个，得到四不像。
- 实际只有两种工具，不是三种：积分和 token 是同一东西的不同时期（积分 = TGE 前的 token）。简化空间在叙事，不在合并：**"挖矿赚积分，积分变 token；干得久拿徽章，徽章是身份。"**

❌ 待定：发放/吊销规则、与 Layer 0 资格的联动（tier 阈值已定：铜/银/金 = 3+/6+/12+ epochs）

## 6. 算例（epoch 预算 = 7,000,000 points，B₀ 已定）

Agent 干了全网 2% work：

| 情形 | points | 说明 |
| --- | --- | --- |
| 无节点 | 140,000 | 基准 |
| 绑定节点，全网绑定率 Q=0 | 175,000 | +25% 满额 |
| 绑定节点，Q=0.5 | 155,556 | 部分稀释 |
| 绑定节点，Q=1.0 | 140,000 | 稀释归零 |

- 上限：350,000（5%）；绑定者 4% work 即触顶（乘数顶不破 cap）✅
- ⚠️ points 目前无美元价——此阶段任何美元预测都是编造。

## 7. 谁拿什么（四象限）✅

|  | 有 Agent work | 无 Agent work |
| --- | --- | --- |
| **有节点** | Layer 1 ×1.25 + Layer 0（tenure 够）+ 双 SBT | Layer 0（tenure 够）+ operator SBT；**无 points** |
| **无节点** | Layer 1 ×1.0 + 工作 SBT（默认 onboarding） | — |

VPS 纯撸节点（无 work、无 tenure）：零。这是设计，不是缺失。

## 8. Tenure-weighted 未来分配（框架位）🔶

- 节点 tenure 记入 SBT；未来 TGE 分配时作为权重因子。
- 现在：只记账，不承诺、不花预算 ✅（原则）
- 姿态（2026-10-07 定）：去中心化项目，无传统法务流程，不以"等法务"为门槛；机制如实公开记录，r（兑换比例）待定，不做具体经济承诺。

## 9. 协议明确不奖励的 ✅

- 中继字节 / uptime（BLK-5：不可验证）→ 走市场层手续费
- Claude Code 订阅挖矿（BLK-1：ToS 禁止）→ BYO API key 路径
- 上线文案只能声称支持 Codex，绝不能暗示 Claude Code（硬规则）

## 10b. 表现层与叙事（非机制，但决定用户体感）🔶

> 背景（2026-10-07）：用户以 user 视角反馈"积分太抽象，花在哪不知道；纯 SBT+空投更直观"。评估结论：病因在表现层，不在机制层——换机制是开错刀。

- **诊断**：points 抽象的三个真因——① 不可视（数字躺在 DB 里）；② sink 不具体（命名空间/折扣都还是文档）；③ TGE 转换公式未公布（不知道值多少）。三个都是表现层/待办项，没有一个需要推翻双轨机制。
- **Pure SBT + 空投的五个硬伤**（已评估，不采用）：
  1. Gas：每任务 mint 徽章上链成本无解；放链下则 SBT 变回数据库条目，失去"摸得着"感。
  2. 颗粒度：任务有大有小，徽章离散；按大小分级 → 分级函数就是 work 函数 → 数字就是积分，换皮。
  3. 公式问题："徽章多=空投多"需要 badge→token 换算表；换算表不公布则比积分更抽象（积分至少有每 epoch 固定预算），公布则等于积分重命名。
  4. 一次性空投是全 crypto 最易撸的分发（Uniswap/Blur/EigenLayer 全被女巫打穿）；连续 epoch 发行 + decay + cap 更难撸。
  5. 无 burn 则无货币政策：徽章不能烧（烧徽章=毁声誉，没人干）→ 只有增发没有 sink → 空投含金量随增发稀释。
- **最优做法**：机制双轨不变，表现层徽章化——
  1. Milestone SBTs（S11 扩展）："首个 10,000 分""连续 12 epoch""金牌运营"——分数是里子，徽章是面子。游戏都这么干：score + achievements，没人只留 achievements。
  2. **TGE 转换公式现在就公布框架**（`token = points × r + tenure 加权`，r 可待定）：消除"不知道值多少"的抽象感。❌（法务评估前不承诺具体比例）
  3. 做实 sinks：P0 = Relay ID 注册 burn（S10-0 chargeable namespace 现成）；P1 = 手续费折扣。sink 具体，积分就不抽象。
- **叙事一句话**："挖矿赚积分，积分变 token；干得久拿徽章，徽章是身份。"
- **诚实条件**：若 TGE 公式因合规不敢公布，则"抽象"无解——用户永远不知道积分值多少，这是 trade-off 不是 bug；milestone SBT 需要 S11 扩展开发成本；若坚持极简单轨，代价是放弃 burn、无货币政策、接受一次性空投的女巫风险。

## 10. 未决清单（dev-blocking，按优先级）

1. ✅ **B\_0 + 衰减**（2026-10-07 定）：7M/周，每 4 周 −15%（`B_n = 7M × 0.85^⌊n/4⌋`）。首年约 1.64 亿。
2. ❌ **X 最终值**（25% 起步，节点数建模后定）。owner：待定
3. ❌ **Layer 0 参数包**：阶段比例起步值（50/25/10）/ 3 epochs 门槛 / tier 权重（2026-10-07 已压缩为 1×/1.1×/1.25×）/ challenge 协议 / "连续在线"slot 参数与重置策略 / **VPS farm ROI 建模——launch-blocking**（50% 池下必须先证明矿场无利可图才能上线）。owner：待定
4. ✅ **cap 余量处理**（2026-10-07 定）：销毁。cap 导致 Σ份额 < 预算时，未分配预算直接不增发（settle 时不写），发行量可预测。
5. 🔶 **绑定注册 UX + attestation 格式**：机制已定（2026-10-07：agent delegation-1 意向 + verifier assertion-1 确认，不改 signing policy）；UX 与 attestation 具体格式待设计。owner：待定
6. 🔶 **SBT tier**：阈值已定（2026-10-07）：铜/银/金 = 3+/6+/12+ epochs（与 Layer 0 对齐）；发放/吊销规则待定。owner：待定
7. ❌ **Layer 0 sunset 治理流程**（谁决议、默认动作、提前终止条件）。owner：待定
8. ✅ **Tenure-weighted TGE 姿态**（2026-10-07 定）：去中心化，无传统法务流程；机制公开记录，r 待定，不做具体经济承诺。
9. ✅ **Epoch 时长**（2026-10-07 定）：7 天。
10. ❌ **Settler 审查（omission）的 challenge / 强制纳入机制**（agent 可自证被遗漏，但如何强制记账方纳入）。owner：待定
11. 🔶 **TGE 转换公式框架公布**：公式结构 `tokens = X × r × (1+tenure_bonus)` 已定；r（兑换比例）待定——是经济参数未定，不是等谁审批。owner：待定
12. ❌ **Milestone SBT 类型设计**（S11 扩展："首个 10,000 分""连续 N epoch"等）。owner：待定
13. ❌ **P0 sink 落地**：Relay ID 注册 burn 的实现依赖（S10-0 namespace + 收费机制）。owner：待定
14. ❌ **Claim UX 详细设计**：CLI 命令 spec、gas 策略（自付/代付）、网站 Phase 2 范围（只读 dashboard + 发起页）。owner：待定

## 附录 B：Pure SBT 完整方案（已设计，待决策）

> 背景（2026-10-07）：用户要求在 pure SBT 约束下做最优设计（Node + Agent 全用 SBT，无 fungible points）。以下为该约束下的最佳方案及诚实评估。

### B.1 设计

- **EpochContribution SBT**（每身份每 epoch 一枚）：soulbound，metadata 含 `{work_score, node_tier, phase}`。实现用 **EAS off-chain attestation**（EIP-712 签名，免费，用户钱包持有，可验证），每 epoch 一个 Merkle root 上链锚定。不用 ERC-721 逐个 mint（gas 无解）。
- **Milestone SBT**（链上 ERC-721，稀有）："首个 10k""连续 12 epoch""金牌运营"——纯 trophy，无数值。
- **空投公式（day one 公布）**：`tokens_i = Σ_e [work_score(i,e) × W_phase(e) + node_value(tier(i,e)) × N_phase(e)] × decay^e`，phase 权重沿用 50:50→75:25→90:10。
- **Spend**：徽章不可 burn，改为"预支空投份额"——用户签名 spend 消息，settler 从其累计份额中扣除。命名空间/折扣/优先级照此实现。
- **反女巫**：work_score 沿用 work 函数 + 5% cap；node_tier 沿用 tenure tier；1.25× 绑定乘数作用于 work_score。机制复杂度与 points 方案相同。

### B.2 诚实账本

得到的：
- 看得见：每 epoch 一枚徽章，钱包里可展示（tangible UX）。
- 托管：attestation 用户自持签名，不只活在平台 DB。

付出的：
- **D1 重造**：RecordWork/Finalize/Merkle/idempotent settle 已建成；pure SBT 需重建 70% 相同逻辑 + EAS 集成。
- **work_score 就是 points 改名**：徽章里的数字承担全部记账职能——抽象没消失，搬进了 metadata。
- **Spend 别扭**："烧未来的空投份额"比"烧积分余额"难理解得多，且 settler 需维护 gross/net 两套账。
- **Milestone 徽章的 gas**：链上 trophy 仍需 mint 成本（低频，可接受）。

### B.3 关键判断

最好的 pure SBT 与 points+badges 双轨是**同构**的——数值部分一模一样，差别只在表现层（徽章可见）和托管（钱包持有）。而这两样**不用 pure SBT 也拿得到**：表现层徽章化已在 §10b 规划；托管方面回执本就自持签名 + root 上链（§"谁记账"）。

**值得偷的一个**：EAS off-chain attestation 作为徽章发行标准——比"每 epoch mint ERC-721"好得多，建议 milestone SBT 改用 EAS（S11 扩展时评估）。

**建议**：维持双轨（v0.10），不切换 pure SBT。Pure SBT 为 cosmetic gain 支付 rebuild 成本，且 spend 机制更差。若用户坚持要 pure SBT 的"看得见"，用 §10b 的 milestone 徽章 + 可视化实现，不动机制。

## 11. Changelog

- **v0.21**（2026-10-07）：4 项拍板落字——cap 余量定销毁；绑定机制定（agent delegation-1 意向 + verifier assertion-1 确认，不改 signing policy）；§8 姿态定（去中心化无传统法务流程，不以等法务为门槛，r 待定）；SBT tier 映射定（铜/银/金 = 3+/6+/12+ epochs，与 Layer 0 对齐）。
- **v0.20**（2026-10-07）：epoch 定 7 天 ✅；B₀ 定 7M/周、每 4 周 −15%（`B_n = 7M × 0.85^⌊n/4⌋`，首年约 1.64 亿），§6 算例同步；新增 P1 任务供给 proposed（官方池 + 自派任务，P2 转开放市场，待 Cursor 论证防刷）；Gas 归属落字（心跳不上链，每周 root 由记账方付，徽章走 EAS 无 gas）。
- **v0.19**（2026-10-07）：tenure 重置策略定（单 epoch 不达标降一档，连续两 epoch 不达标清零）；Claim UX 增加 Web 版设计（ticket 配对代替连接钱包；"签名 CLI 优先，展示 Web 优先"；CLI 必须可达的约束落字；gas 代付/多身份 UI/deferred claim 待定）；Points 安全补充 Merkle 完整性 vs 真实性说明。
- **v0.18**（2026-10-07）：tenure-tier 权重压缩至 1×/1.1×/1.25×（最大差距 25%）；新增"连续在线"定义（slot 心跳 + 95% 达标 + 降档/清零重置策略待定）；新增显示层需求（每分钟刷新待结算预估，读写分离）；Points 安全新增"伪造 proof"条（密码学不可伪造，真风险在 settler 与锚点）。
- **v0.17**（2026-10-07）：5 处机制批注落字——PoSR 绑定机制待重设计（ADR-0009 D7，给三选一）；"node key"残余术语修正为 verifier key；cap 余量 / B₀ / epoch 时长三处 ❌ 就位标注（标出它们是 settle 实现与 farm ROI 建模的前置依赖）。
- **v0.16**（2026-10-07）：反女巫补充（同机多节点自收敛；不用 IP 反女巫）；共 key 自我惩罚落字（协议按地址记账，无需禁止）。
- **v0.15**（2026-10-07）：新增"Claim UX"（两种 key 两笔 claim；`relayfirst claim --to`；CLI 优先不先做网站，Phase 2 只读 dashboard；gas 策略待定；SBT 不用 claim）。§10 新增 14。
- **v0.14**（2026-10-07）：新增"绑定与身份规则"（地址=身份无 rotation；one-binding-per-identity；多 node/agent 矩阵；key 分离安全；残余风险）。
- **v0.13**（2026-10-07）：明确 SBT 在 claim 中的角色（乘数非票据；无 points 领不到；SBT 已在三处变现：L0 加权/TGE 加成/未来 perks；直接定价=double counting）。
- **v0.12**（2026-10-07）：新增"Airdrop claim 机制"（快照→总 root→claim 合约；`tokens = X × r × (1+tenure_bonus)`；防重领；证明非花掉；TGE 后框架预留）。
- **v0.11**（2026-10-07）：新增附录 B：Pure SBT 完整方案（EpochContribution SBT 用 EAS off-chain attestation + milestone 链上徽章 + day one 公布空投公式；诚实账本：D1 重造、work_score 即 points 改名、spend 别扭；判断：与双轨同构，建议维持双轨；值得偷：EAS 做徽章标准）。
- **v0.10**（2026-10-07）：新增 §10b 表现层与叙事（诊断 points 抽象三真因；pure SBT+空投五硬伤不采用；最优解=机制双轨+表现层徽章化+TGE 公式公布+做实 sinks；§10 新增 11–13）。
- **v0.9**（2026-10-07）：新增资金流向图（epoch 预算 → 双池 → 积分账本 → burn/TGE；SBT/USDC 平行轨道）。

- **v0.8**（2026-10-07）：新增"Points 安全吗"（被盗/篡改/跑路/稀释/归零五问；settler 可替换；root 上链待定）；评估"统一成一个再空投"提案——不采用（gas 重现、flow/stock 不消失、一次性空投更易撸；TGE 转换即连续空投）。
- **v0.7**（2026-10-07）：新增"积分怎么花"（不可转让→花=烧；积分管赚和烧、钱管付和收；TGE/命名空间注册/手续费抵扣/优先级；反垃圾选项未定）。
- **v0.6**（2026-10-07）：新增"为什么不能统一为一种"（统一成 SBT 杀死经济飞轮/A2H；统一成积分杀死可验证履历/LinkedIn；flow vs stock；两种工具不是三种）。
- **v0.5**（2026-10-07）：新增"可交易性与三者关系"（积分不可交易→TGE 转 token 可交易；SBT 永不变成 token；token=你赚了多少，SBT=你是谁）。
- **v0.4**（2026-10-07）：新增"谁记账"（平台记账但可验证；P1→P2→P3 去中心化路径；D3 verifier 名单即 root co-signer 集；omission 未解 → §10.10）。
- **v0.3**（2026-10-07）：新增"积分的性质"（链下账本 + Merkle root，TGE 时转 token；上线无需为积分部署合约）。
- **v0.2**（2026-10-07）：预算改为分阶段动态比例（P1 50:50 / P2 75:25 / P3 90:10，预先锁定、按 epoch 自动切换）；Layer 0 分配改为 tenure-tier 加权（1×/2×/4×）；farm ROI 建模升级为 launch-blocking；新增"SBT ≠ 积分"概念澄清。
- **v0.1**（2026-10-07）：框架版。确立 Layer 0/1/2 + 市场层结构；Model B、份额公式、5% cap、1.25× 乘数数学已定；Layer 0 参数、X 最终值、B\_0、cap 余量等待定（§10）。
