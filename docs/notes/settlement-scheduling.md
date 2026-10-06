# 结算调度 —— 单机运维示例（D1 收尾）

> **⚠️ 这是示例，不是规范。** 生产部署自定 —— 用什么调度器、在哪台机器、失败怎么告警，都由运营决定。
>
> **本文只回答第一个 operator 一定会问的那句：**「我挖了矿，分在哪？」——
> 答：**epoch 结束后，有人跑一次 `relayfirst settle`。** 下面是怎么让它"有人"变成"自动有人"。
>
> 相关：[`settlement-trigger.md`](settlement-trigger.md)（为什么是显式命令）、`planning.md §A2`（D1）。

---

## 0. 为什么需要这一步（不是可选）

**模型 B 下，"结算"是一个独立动作。** 之前每个回执自动落账；现在回执记 **work**，
points 由 **`relayfirst settle`** 在 epoch 结束后一次算出来。**没有这一步，余额永远是 0** ——
不是 bug，是模型（`MVP.md §6.2`：固定预算按份额）。

所以：**挖矿的机器上，需要一个"每隔一个 epoch 跑一次 settle"的调度。**

---

## 1. 单机示例：crontab（最简单）

```cron
# 每天 00:05 UTC（epoch 长 = 24h，00:00 UTC 收口）结算「刚结束」的那个 epoch。
# 5 是给边界留的余量，避免在 epoch 尚未真正收口时结算。
#
# ⚠️ 不需要密钥：settle 是对 work 账本做算术并写 points,【不签名】。
# 所以它可以跑在一台【不持任何密钥】的调度机上,只需能读那个 db 文件。
5 0 * * *  /path/to/relayfirst/relayfirst settle --db /path/to/relayfirst.db \
             --epoch $(( $(date -u +%s) / 86400 - 1 )) >> /var/log/relayfirst-settle.log 2>&1
```

**要点：**

- **不需要密钥**（重要）：`settle` 只读 work、写 points，**不碰任何私钥**。
  这正是它可以被一个**无密钥调度器**安全放权的原因 —— 结算错不了签名，最多算错一个 epoch，而幂等让它可重来。
- **幂等**：同一 epoch 重复跑**不重复发分**（entry id = `settle:<epoch>:<agent>`），所以
  cron 抖动、手动补跑、崩溃重试都**安全**。
- **`--db` 指向矿工那个库**（默认 `./relayfirst.db`）；settle 作用于**本机单库**。
- **`--epoch` 显式传"上一个"**，而不是默认"当前" —— 默认是给人工测试用的；调度应结算
  **已经关闭**的那个 epoch。
- **`>> log`** 保留输出：settle 打印每个 agent 的分数与总量，**这是"分落在哪"的凭据**。

---

## 2. 单机示例：systemd timer（更可控）

`/etc/systemd/system/relayfirst-settle.service`：

```ini
[Unit]
Description=RelayFirst epoch settlement

[Service]
Type=oneshot
# No key is needed: settle does not sign. So this unit can run as a user that
# cannot read any key file -- the least privilege for a job whose input is a db.
ExecStart=/path/to/relayfirst/relayfirst settle --db /path/to/relayfirst.db --epoch %i
```

`/etc/systemd/system/relayfirst-settle.timer`：

```ini
[Unit]
Description=Run RelayFirst settlement after each epoch closes

[Timer]
# 每天 00:05 UTC; OnCalendar 用 UTC 时区,避免本地时区漂移
OnCalendar=*-*-* 00:05:00 UTC
Persistent=true

[Install]
WantedBy=timers.target
```

**为什么 systemd 更好（单机场景）：**

- **`Persistent=true`** —— 机器在结算点关机，开机后会**补跑一次**（幂等让补跑无害）。
- **失败可见** —— `systemctl status` / journal 就能看到；cron 靠日志文件，容易没人看。
- **`oneshot`** 语义正确：结算是一次性动作，不是常驻服务。

---

## 3. 检查"分到底落没落"

```bash
relayfirst status                 # 看余额与每 agent 明细
relayfirst settle --epoch <N>     # 手动补结算（幂等，随时可跑）
```

- **余额是 0 且 work 已累积** → **还没结算**。跑 `settle`。
- **余额在涨** → 结算在跑。

> **这是新增的运维负担，必须说清**：以前"挖了就有分"，现在"挖了有 work，结算后有分"。
> 第一个 operator 一定会问，所以本文存在。**让 `status` 和 `settle` 的输出都带上
> "work 已累积但未结算" 的线索，是把疑问堵在源头。**

---

## 4. 边界

| 项 | 说明 |
|---|---|
| **不做成规范** | 用什么调度器、跑在哪、告警怎么配 —— **运营自定** |
| **多机 / 多 operator** | 结算作用于 **本机的 work 账本**（单库）；多机场景各自的库各自结算，**跨机聚合属 Post-MVP** |
| **clock 一致性** | `--epoch` 是**显式数字**，不依赖调度器时钟；算错 epoch 只会结算错的那个（且幂等，可重来） |
| **未实现**：链上提交 | settle 只**算 + 落账**；**把 root 上链**是独立的人工动作（`contracts/RelayAnchor.sol`，见 S7 报告） |
