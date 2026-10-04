# WuKongIM 技术优势记录（供 RelayFirst 借鉴）

日期：2026-10-04
来源：WuKongIM 项目 _wk-src/（v3 beta 上游源码快照）
目的：把 WuKongIM 在架构、DB、Go 库上的优化手段记下来，供 RelayFirst S10+ 借鉴。

先说结论：WuKongIM 的性能工程在自托管 IM 品类里是第一梯队。不是因为某个单点黑科技，而是每一层都知道自己在为什么付费，且有 benchmark 纪律兜底（拿压测抓真 bug，不是当宣传稿）。实测数字目前只到单机三节点 acceptance gate（4500 QPS sustained，P99 低于 400ms），v3 beta，结论是 provisional 的。借鉴思路，不照搬数字。

## 1. 网络层：事件驱动 + 零分配热路径

- gnet event loop（pkg/gateway/transport/gnet/）：TCP/WS 共用一套 conn 状态机，epoll/kqueue 驱动，替代 net/http 的每连接 goroutine 模型，是 C10K+ 连接数的基石。
- WS 握手和帧解析手写（ws_handshake.go、ws_frame.go），热路径不用 gorilla/websocket；附带专门的 allocation 基准测试（conn_write_alloc_test.go、hotpath_benchmark_test.go），说明有人在数内存分配。
- 背压：inbound 缓冲超限直接 ErrPendingBytesExceeded，慢连接不拖死 event loop。
- RelayFirst 对照：我们现在是 net/http + coder/websocket。S10 若要做大连接数推送，gnet 是候选，但不是现在，当前瓶颈不在连接数。

## 2. 并发模型：决策与阻塞 I/O 严格分离

- Reactor 只做状态转移，永不执行阻塞的 store/transport I/O；阻塞工作交给 typed worker 池，fenced 结果返回。
- Channel-keyed reactor + mailbox 优先级队列（ReactorCount 哈希分区，MailboxSize 有界）。
- 六个命名 worker 池全部有界且基准调过：channelv2-store-append（128 workers，注释写明 qualified for sustained 2,000 SEND/s three-replica）、store-apply（8）、rpc（96）等。池大小是压测调出来的，不是拍脑袋的。
- pkg/goroutine 有进程级 goroutine 所有权注册表（fixed task catalog、panic 策略、pool 压力指标），防 goroutine 泄漏。
- RelayFirst 对照：S10 做任务中转/索引时，reactor 分离 + 有界池是直接可抄的形状。池大小必须 benchmark 定，写进 S10 验收。

## 3. 写入路径：分区串行 + 跨区合批 + Group commit

- 单 channel 串行：每个 ChannelLog 一把 appendMu，同 channel 追加串行保序列号连续，不同 channel 并发。经典分区串行设计。
- 跨 channel store-append 合批：64 条或 250 微秒窗口（storeAppendBatchMaxItems=64），摊薄 Pebble 写入。
- Group commit：500 微秒收集窗口 + 1024 队列，注释写明是 benchmark 调出来的（wider windows increased caller latency without enough additional batching）。
- Key 前缀预计算缓存（appendKeyCache）：immutable 前缀每行不再重复编码。
- RelayFirst 对照：我们的 SQLite 目前单连接串行 + 逐条写入，这是 2000/s 数字的主要来源，不是 SQLite 的技术天花板。短期 fix 是批量事务 + WAL 下放开读并发。

## 4. 存储：Pebble 的内行用法

调参（实名在代码里）：BlockCache 128 MiB；MemTable 32 MiB；L0CompactionThreshold/StopWrites 8/24；Compaction 自适应 1 到 4；Bloom FilterPolicy(10) 全 level（约 1% FPR）；Darwin BytesPerSync 16 MiB（绕开 macOS fsync 坑）；Group commit 500 微秒窗口。

- Key schema：Domain(1B) 到 PartitionKind(1B) 到 Space(1B) 到 big-endian 有序部分，另有 AppendInt64Desc 做最新优先排序。教科书级 LSM 友好设计：前缀分区、范围可扫、顺序即语义。
- 消息/元数据双引擎隔离（MessagePath/MetaPath）。
- 为什么 Pebble 而不是 SQLite/Postgres：负载是 append-heavy 日志 + 点查 + 范围扫描，几乎无 JOIN，这是 LSM 的甜点区；Postgres 杀掉零外部依赖一键部署的核心卖点；Pebble 是 pure Go 无 CGO，保住单二进制分发。选型与负载匹配，不是跟风。
- RelayFirst 对照：Pebble 是 RelayFirst Scaled tier 的首选候选（pure Go 保判据 7；LSM 对 append 日志最优）。但现在不动：SQLite + 批量写足够到千万每天，迁移是过早优化。

## 5. 协议：二进制优先

- WKProto 自研二进制编解码（pkg/protocol/codec/）：send/sendack/recv/recvack/ping/pong，热路径不用 JSON；自定义 Writer 接口。
- RelayFirst 对照：Receipt 的 canonical JSON 走 EIP-712 是正确性要求（A9 逐字签名），不能换；但传输信封在 S10 做多 relay 时可以评估二进制编码，先量后换。

## 6. Go 库选型（验证过实际用在哪）

- panjf2000/gnet/v2：pkg/gateway/transport/gnet/，epoll 事件驱动，C10K 基石。
- panjf2000/ants/v2：pkg/workqueue/bounded_pool.go（自研有界队列+观测包装），goroutine 复用，防 burst 爆炸。
- cockroachdb/pebble v1+v2：pkg/db/internal/engine/，LSM 存储。
- go.etcd.io/raft/v3：控制面/Slot，不自研共识。
- valyala/bytebufferpool：buffer 复用，降 GC。
- bwmarrin/snowflake：分布式唯一 ID。
- klauspost/compress：压缩。
- prometheus/client_golang：全链路低基数指标（FLOW.md 禁止 UID/ChannelID 进 label）。
- gorilla/websocket：在依赖里，但热路径被自研 WS 替代，只用于非热路径。
- bytedance/sonic：indirect only，未直接使用。

## 7. 最值得抄的三条工程纪律

1. Benchmark 当工程手段：internal/bench/ 16 个子包，黑盒、确定性、seed 定 plan、证据不足判 harness-invalid；压测抓出过真 defect（权限检查 head-of-line blocking，P99 从 2 秒修到 400ms）并写进报告；报告诚实限定为本地可复现结果。对应行动：第 14 道 CI 门禁 = ingest benchmark 回归（待立项）。
2. 池大小、窗口大小全部 benchmark 定，注释写明调参依据。对应行动：S10 验收要求参数附依据，不许拍脑袋。
3. 全链路有界 + 背压代替无界增长：队列、mailbox、batch、worker 池全部有界，慢消费者被 drop。对应行动：node hub 已有 bounded buffer（64），S10 扩展到所有新队列。

## 8. 诚实限定

- 所有实测数字都是单机三进程：无多机、无跨机房、无故障注入。抄思路不抄数字。
- v3 beta：API/配置/持久化格式可能变，结论 provisional。
- 4500/s 是三副本 quorum + 持久化 + 权限检查的全功能写入，别对标 dumb relay 的 fire-and-forget；连接数上限没公布。
- WuKongIM 是中心化集群架构（Raft + 256 槽），和 RelayFirst 的 Nostr 式 permissionless 是两条路。借鉴单节点内的性能手段，不借鉴集群模型。

## 附：RelayFirst 借鉴清单（按优先级）

1. Benchmark 回归门禁：第 14 道 CI gate，ingest benchmark，防性能回退。待立项。
2. 批量事务 + WAL 读并发：internal/sqlite 先改用法不动 DB（10-50x）。待立项。
3. 有界队列 + 背压纪律：S10 所有新队列有界 + 慢消费者 drop。S10 定义时写入。
4. 池/窗口参数 benchmark 定：S10 验收附调参依据。S10 定义时写入。
5. Pebble 作为 Scaled tier 候选：pure Go 保判据 7。路线图，不现在做。
6. 分区串行 + 跨区合批：未来多分区索引/任务中转时用。路线图。
7. gnet 评估：仅当连接数成瓶颈时。现在不是。
8. 二进制传输信封评估：S10 多 relay 时先量 JSON 开销再决定。

## 9. 对 RelayFirst 的借鉴价值裁定（2026-10-04 补充）

逐项裁定 WuKongIM 的 Go 库选型对 RelayFirst 是否有参考价值。

值得借的：

- Pebble：最强的一项。pure Go 保判据 7，LSM 对 append 日志最优，Scaled tier 首选（路线图）。
- prometheus 低基数纪律：S10 做节点可归因（RFN-12 信誉）时需要指标，禁止 UID/ChannelID 进 label 这条纪律直接抄，低成本高价值。
- ants 的思想：不是库本身（标准库 channel 工人池也能做），而是有界池加背压，永不无界起 goroutine。S10 的索引/中转 worker 需要这个纪律。

不适合的：

- etcd raft：和 NET-3（节点间无共识）直接冲突。WuKongIM 是中心化集群才需要它，Nostr 式不要碰。
- gnet：现在不是瓶颈，别提前换编程模型。连接数真成问题时再评估。
- 手搓 WS：WuKongIM 是在 4500 QPS 下数 allocation 才这么干。coder/websocket 够用，手搓属于过早优化。

最值得学的不是库，是选型纪律：sonic 在依赖里但没直接用、gorilla 在依赖里但热路径被绕过。每个依赖都要自己挣到位置，benchmark 说了算。RelayFirst 已有这个基因（为 advisory 禁 gorilla、为理由自研 merkle），保持住。

一句话：抄 Pebble 和纪律，不抄 raft 和 gnet。
