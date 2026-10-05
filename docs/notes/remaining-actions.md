# 剩余动作清单（上线前）

> **代码侧自闭环的任务已清空。** 本文把"还没做完的"分成三类，每类写明**谁做**。
>
> 更新日期：2026-10-05。权威状态在 `TASKS.md`；本文是**行动索引**，不复制状态。

---

## A. 需要你【裁决】的（我做不了，因为它是业务决策）

| 项 | 决定 | 决策包 | 还缺什么 |
|---|---|---|---|
| **BLK-4 genesis** | **✅ A：genesis = 公开发布日 00:00 UTC** | [`blk-4-genesis-decision.md`](blk-4-genesis-decision.md) | **仅缺"具体哪天"** → 我改一行常量 + 跑回归 + 用 `-tags mainnet` 验防呆解除 |
| **BLK-3 指派策略** | **✅ A：确定性种子** | [`blk-3-assignment-policy.md`](blk-3-assignment-policy.md) | 部署配置（候选集 + seed 来源）；**机制零改动** |

> **两项方向都已裁决。** BLK-3 **机制上已关闭**（现参考实现即 A）；BLK-4 只等你给发布日。

### 已做的防呆（BLK-4）

`-tags mainnet` 构建在 genesis 仍为占位符时**启动即 panic**（`internal/epoch/release*.go`），
所以占位符**不可能被误带上线**。实测见 `blk-4-genesis-decision.md` §6。

---

## B. 需要【外部环境 / 人工】的（我无法代做）

| 项 | 为什么我做不了 | 入口 |
|---|---|---|
| **BLK-1** 订阅可程序化驱动？ | 需真实账号/订阅核实 | `TASKS.md` §1 |
| **BLK-2** 第一个真实消费方 | 需**人去接洽** | [`blk-2-first-consumer-plan.md`](blk-2-first-consumer-plan.md)（画像/首封信/8 周时间盒） |
| **判据 ①** 陌生人 10 分钟出分 | 定义就是**真人计时** | `TASKS.md` S6-8 / S8-1 |
| **判据 ③** 陌生人一条命令起节点 | 代码层已过（Dockerfile 实测）；**镜像未发 + 计时未做** | 见 C |
| **判据 ⑨** SBT 钱包可见 | 需**链上部署** + 真实钱包 | `contracts/` + `docs/stages/S7-report.md` |
| **S12-3** 真实 USDC | 需**选链 + 测试币 + 部署** | `TASKS.md` S12-3 |
| **BLK-3** 候选集"谁在池里" | 需**组织决定**谁当验证者 | 见 A |

---

## C. 需要【发布动作】的（一次性命令，但要有凭据）

| 物 | 到哪 | 现状 |
|---|---|---|
| `relayfirst` + `relayfirst-mcp` → npm | `npm publish` | `package.json` `bin` 就位，**未发布**（S6-7） |
| `relayfirst/node` → registry | `docker push` | `Dockerfile` 就位、实测 build/run 成功，**未发布**（P1 #6） |

> 一旦发布，`docs/notes/mcp-setup.md` 里"本地二进制回退"那段可删。

---

## D. 已闭环（本轮及近期完成，登记备查）

- **S13-3d** 委托签发者 + 消费者（`session grant` / `session verify`）
- **S12-7** MCP IDE 一行配置文档（[`mcp-setup.md`](mcp-setup.md)）
- **S10-6** 客户端独立复验（`assertion.Check`；此前 `TASKS.md` 有重复行，已修）
- **事件生产者** `relayfirst session open/close/show`

---

## 一句话

**代码写完了；卡住的是"业务决策 + 外部接洽 + 发布动作"。**
A 类给我输入即可当轮闭环；B/C 类需要你或运营去做，我会在你推进时同步文档。
