# BLK-2 外联工具包（首封信 中/英 + 名单模板 + 渠道）

> **用途**：把 [`blk-2-first-consumer-plan.md`](blk-2-first-consumer-plan.md) 的第 5 节**变成可直接发的版本**。
> **执行者：人**（这是上线关键路径）。**我无法代做**：认识谁、发信、对话。
> **配套**：`status-map.md`（三条基础线）、`blk-2-first-consumer-plan.md`（完整方案）。

---

## 0. 先填这三格（决定用哪个版本）

| 格 | 填 | 影响 |
|---|---|---|
| **圈层** | 中文 / 英文 / 都要 | 决定下面用哪封信 |
| **有渠道吗** | 认识量化/基础设施开发者？ | **有 → 跳过冷信，直接约 15 分钟**；无 → 走冷信（目标 10 人）|
| **买家名单** | 见 §3 模板 | 冷启动的落点 |

---

## 1. 冷信 · 中文（可直接发）

```text
标题：问一个关于你价格源 failover 的问题

你好，

我在做一个「可独立复核的观测记录」工具，想先确认它是不是个真问题，
不是来推销的。

你们的行情/价格数据现在接几个源？当几个源不一致时，
你怎么判断是「源错了」还是「你的解析错了」？

我在做的方向：多个独立 agent 各自观测同一个公开源，
每人签一份可离线验证的回执，然后交叉比对。
所以「这个价格哪来的」能变成一份**可复核的证据**，而不只是你自己的日志。
单个源挂掉，也不会让数据断。

如果你觉得有用，我可以**用一个你的公开源跑一次**给你看
（5 分钟，不需要你装任何东西，也不用给我密钥）。
如果你觉得没用，也请回我一句为什么 —— 我确实想知道。

[你的名字]
[联系方式]
```

## 2. 冷信 · 英文（可直接发）

```text
Subject: a question about your price-feed failover

Hi,

I'm building an "independently re-checkable observation record" and want to
confirm it's a real problem before going further -- this isn't a pitch.

How many sources does your price data come from, and when they disagree, how
do you tell whether the source is wrong or your parsing is?

The direction: several independent agents observe the same public source, each
signs an offline-verifiable receipt, and we cross-check them. So "where did
this price come from" becomes a re-checkable record instead of just your own
log, and a single source going down doesn't break the feed.

If it looks useful, I can run one pass against one of your public sources
-- 5 minutes, nothing to install, and I don't need any key. If it looks
useless, tell me why; I'd genuinely like to know.

[Your name]
[contact]
```

> **为什么这样写**（沿用计划 §5）：不问"要不要买"；问一个专业上愿意回答的问题；给退出方式；成本极低。

---

## 3. 买家名单模板（冷启动落点）

复制到你的工具里逐行填。**目标：10 行。**

| # | 角色 | 具体到人/组织 | 在哪找到 | 渠道 | 状态 | 下一步 |
|---|---|---|---|---|---|---|
| 1 | 量化开发者（个人/小团队）| | | | 未联系 | 发冷信 |
| 2 | 交易机器人作者 | | | | | |
| 3 | 行情聚合/数据服务 | | | | | |
| 4 | 做 RPC/节点基础设施 | | | | | |
| 5 | … | | | | | |

**状态取值**：`未联系 → 已发 → 已回 → 对话中 → 试用中 → 在用`（对应计划 §6 的信号分级）。

---

## 4. 从哪找人（无渠道时的渠道清单）

- **GitHub**：搜"把行情/价格源接到自己服务的仓库"（trade bot、arb、market-maker、price aggregator）。**看 issue / 作者**。
- **Discord/Telegram**：量化、交易、基础设施社群；**先潜水看谁在抱怨"源不一致/failover"**。
- **开发者论坛**：HN / r/algotrading / 相关中文社群 —— 发帖问"多源不一致怎么办"，**问题本身带来名单**。
- **已有关系**：同事、前同事、读者、客户里"碰过价格数据"的人 —— **先扫一遍，冷启前优先**。

**⚠️ 不要群发。** 计划 §6 的判据是"**用输出做决定**"，群发拿到的"有意思"正好是最弱信号。

---

## 5. 每次接触的固定动作（照计划 §3/§6）

```text
1. 发信（§1/§2）
2. 若他回"有意思" → 不是兴趣，追问一句："你现在的源不一致时具体怎么处理？"
3. 若他愿意见 → 跑一户 demo（下）
4. 记信号（§6 分级），别靠感觉
```

**demo（5 分钟，现场或 60 秒录屏，不做 PPT）**：

```bash
# 需要一份已发布到节点的回执，observations 才有内容（节点索引它收到的）
relayfirst observations --relay <node-url> --subject https://<他的公开源>
```

输出即"按声称 contentHash 分组 + 每组几个独立 agent"，附诚实边界（**一致 ≠ 正确**）。

---

## 6. 时间盒（防无限期"还在接洽"）

| 阶段 | 时长 | 目标 | 失败则 |
|---|---|---|---|
| A | 第 1 周 | 联系 10 人 | — |
| B | 第 2–3 周 | 2–3 次真对话 | 重写信（可能是措辞）|
| C | 第 4 周 | ≥1 人愿试用 | **回 `MVP.md` 重审场景** |
| D | 第 5–8 周 | 他在真实使用 | 换场景或重想整个 play |

> **8 周无人使用 = 真实信号**（计划 §8 + `MVP.md §10.3`：它可能永远解决不了）。
