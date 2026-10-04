# RelayFirst 作为通用去中心化消息引擎：定位、缺口与 WukongWeb / Wukong Agent 对接

日期：2026-10-04
背景：2026-10-04 讨论确认 RelayFirst 可发展为 Headless A2A/IM 通用消息引擎（Nostr 形），用户在其上 build 不同的 social app。本文件记录技术依据、缺口与对接方案。

## 1. 定位：Decentralized Headless A2A & IM

- RelayFirst = Decentralized（去中心化网络）+ Headless（无头协议）+ A2A & IM（agent 协调与人类消息同一套 envelope）。
- 三要素缺一不可：Decentralized 指 permissionless multi-relay 网络（Nostr-style node model，无官方 relay，NET-1 至 NET-4，见 ARCHITECTURE.md 第 15 节）；Headless 指无官方客户端与渲染层，协议真相只在签名与链下 relay 之中；A2A 与 IM 指同一套 envelope 同时承载 agent 任务协作与人类即时消息，挖矿、社交都是跑在上面的应用。
- v2.0 已定：本体 = A2A 协议 + 节点；挖矿 = 跑在协议上的引流层，即第一个应用。
- 新定位下：social app 是第二个、第三个应用。JimAIM 可作为 flagship 参考客户端。
- 明确不是 Matrix 形：无联邦、无全序（NET-3）。Twitter 式 social 是 proven 模型；WhatsApp 式大群 E2E 难。

## 2. 技术依据：envelope 已经是通用容器

- internal/protocol/envelope.go：Kind 是自由字符串（非枚举），Payload 完全不透明，节点只读 ID、AgentID、Kind。
- 设计原意：the node is payload-agnostic by design；字段叫 id 而不叫 receiptId，so the same format can carry non-receipt messages。
- 这就是 Nostr 的 kind 机制：任何应用可定义自己的 kind，relay 不解释、不拦截。
- Sequence + PreviousEventHash：per-actor 投递链（S9 预留，A9 第 6 条），给 social 的因果序打基础。
- MaxPayloadBytes = 1 MiB：文字消息够，媒体走 RFN-08 artifact 引用。

## 3. 缺口一：S10 索引必须支持 social 查询面（新增需求，记入 S10 定义）

现状：节点只按 addressee 索引（GET /messages/某 agentId）加 card 目录。作者身份在签名 payload 内，relay 建不了索引。

social engine 需要的查询：

- 按 author 查：给我 X 的最近 N 条。
- 按 kind 查：给我 kind=K 的最近 N 条（feed 流）。
- 按 author 加 kind 组合查。

要求：

- S10 的索引设计必须包含 author/kind 维度的索引，而不仅是 relay 自用的消息索引。
- 约束：节点不能验签（哑节点铁律），所以 author 索引只能来自 envelope 明文声明的 author 字段或可验证的派生值。当前 AgentID 是 addressee，author 在 payload 内。需要协议层先决定 author 在 envelope 的位置，这是协议设计项，先于索引实现。
- Broadcast 语义：envelope 是寻址的，public post 的 addressee 约定需要定义（寻址给自己，还是空约定）。与 author 字段设计一并裁定。

## 4. 缺口二：Social conventions spec（NIP 对应物）

- Nostr 靠 NIPs 定义 kind 语义。RelayFirst 需要自己的 conventions：profile、follow、post、like 的 Kind 命名与 payload 格式约定。
- 原则：relay 层不感知，全部客户端解释。conventions 只管怎么写，不管谁执行。
- 建议 Kind 命名用字符串自描述，如 social.profile.v1（对比 Nostr 数字 kind，字符串可读性更好，代价是字节稍长）。

## 5. WukongWeb 对接方案

WukongWeb 结构（/Users/cartermacbook/Desktop/Dev/WuKongIM/wukongim-web/）：

- src/client/：vanilla TS 聊天端（5173 端口），registry 架构，新传输可注册接入。
- src/server/ BFF（5001 端口）：WuKongIMProductClient 接口已把 WK 操作抽象，后面加 RelayFirstClient 实现即可。
- admin/：管理台前端，不受影响。

对接映射（按难度）：

- 容易：1:1 消息（envelope 装消息体，session 对会话，现有协议全支持）；会话列表与历史（拉取加 WS 推送，基础都在）。
- 中等：送达/已读回执（RelayFirst 只有存储 ack，需做成协议层签名事件）；presence/typing（RelayFirst 第 4.4 节定为 transport 层，需定 transport 扩展）。
- 难：群聊。WuKongIM 有 channel quorum 全序；RelayFirst 无全序靠客户端合并。小群可行，大群（10 万成员级）是已知难题，放最后。

渐进路径：BFF 双后端（interface 现成）先行，1:1 消息先切 RelayFirst 验证，再小群，大群最后。TS SDK 是前置（2-3 周，含 relay-set 逻辑）。

## 6. Wukong Agent 对接方案

现状（WukongWeb 内）：Agent = bot（system UID svc.assistant 加 wukongimjssdk 长连接），六层 L1-L6 harness，LlmClient 薄接口，Vercel AI SDK 在后面。Agent 在 WK 体系里是二等公民（外挂 bot）。

RelayFirst 里 agent 是一等身份：EVM 地址即身份，Agent Card 即名片，EIP-712 即签名。映射如下：

- 身份层：Wukong 的 bot UID 映射为 RelayFirst agent:eip155 地址加 Agent Card（含 EIP-712 proof）。原来谁在说话靠 UID 字符串，现在靠密码学身份。
- 传输层：wukongimjssdk 长连接收发改为 RelayFirst envelope 收发（需 TS SDK）。L1-L6 上层（上下文组装、MCP 工具、编排、Prompt）不动。harness 与传输解耦正是 LlmClient 薄接口的设计目的。
- 可靠层：Wukong 自建的队列/去重/幂等/对账，对应 RelayFirst 的 envelope ID 幂等加 per-actor hash chain 加多 relay 扇出。部分可复用，部分被协议原生替代。
- 四种身份模式映射：A 独立 Bot 对应独立 RelayFirst agent（有自己的 card）；B 影子旁听对应订阅 relay；C 客户端 Copilot 对应 E2EE 会话 agent（S13）；D BeforeSend 守门员对应节点侧 policy（RFN-04 声明）。
- 战略契合：JimAIM to-C 方向是 Crypto 加 AI。WK 里 AI 是外挂，RelayFirst 里 AI 是原住民。EVM 身份还天然带来打赏、付费 DM、token 门控（Nostr 要靠 Lightning 外挂）。

## 7. 实施顺序建议

1. S10 定义时吃下第 3 节的索引需求加 author/broadcast 协议设计（前置）。
2. TS SDK（2-3 周）。
3. BFF RelayFirstClient 加 1:1 消息验证。
4. Social conventions spec v0（profile/follow/post）。
5. Wukong Agent 身份迁移（bot UID 改为 EVM agent）。
6. 小群聊，最后大群聊（难题最后）。

## 8. 与现有路线图的关系

- 不冲突：S10-S13 照走。本文件是 engine 化视角的需求输入，主要落在 S10（索引/查询面）和 TS SDK（新增）。
- RFN-05（多 relay 客户端逻辑）、relay-set、TS SDK 三者是 engine 化的共同前置，已在 gap list。
- JimAIM 作为 flagship 应用，不改变 RelayFirst 本体中立性（relay 保持哑）。
