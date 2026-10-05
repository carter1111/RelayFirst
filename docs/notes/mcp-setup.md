# RelayFirst MCP — 接入指南（S12-5 / S12-7）

> **一句话：** 把 `relayfirst-mcp` 接到你的 agent（Cursor / Claude Desktop），
> agent 就能**发现任务、认领任务、查询观测、转发已签字节** —— 而它**结构上不可能签任何东西**。
>
> 命令已在真实节点上实测（见文末）。

---

## 1. 这个 server 存在的唯一理由

`MVP.md` §9.2：**MCP server 绝不能持有主密钥。**

原因：MCP server 跑在 **agent 的进程里**，而 agent 可被 **prompt injection** 操控。
如果它能签名，一条注入的指令就能让它**以你的名义签一份回执**。

**这不是"我们记得不要签名"，而是"它够不到签名代码"。** 该保证由**导入图**强制：

```bash
CGO_ENABLED=0 go list -deps ./cmd/relayfirst-mcp \
  | grep -E 'internal/(eip712|receipt|publish|assertion|delegationsign|e2ee|mining)'
# → 输出为空
```

CI 会跑这条断言，所以这个性质**不会因为后来一次"顺手"的改动而丢失**。

---

## 2. 一行配置

### Cursor

编辑（项目级 `.cursor/mcp.json` 或全局 `~/.cursor/mcp.json`）：

```json
{
  "mcpServers": {
    "relayfirst": {
      "command": "npx",
      "args": ["-y", "relayfirst-mcp"],
      "env": { "RELAYFIRST_RELAY": "http://localhost:8080" }
    }
  }
}
```

### Claude Desktop

编辑 `claude_desktop_config.json`（macOS：`~/Library/Application Support/Claude/`）：

```json
{
  "mcpServers": {
    "relayfirst": {
      "command": "npx",
      "args": ["-y", "relayfirst-mcp"],
      "env": { "RELAYFIRST_RELAY": "http://localhost:8080" }
    }
  }
}
```

> **⚠️ 发布状态**：打包**已就绪**（跨平台二进制会随 tarball 发布，`npx` 无需 Go 工具链），
> 但**尚未 `npm publish`**（见 [`planning.md`](planning.md) C1）。
> 在发布之前，用**本地二进制**代替 `npx`：
>
> ```json
> {
>   "mcpServers": {
>     "relayfirst": {
>       "command": "/绝对路径/bin/relayfirst-mcp",
>       "env": { "RELAYFIRST_RELAY": "http://localhost:8080" }
>     }
>   }
> }
> ```
>
> 本地构建：`CGO_ENABLED=0 go build -o bin/relayfirst-mcp ./cmd/relayfirst-mcp`
> （或在仓库内直接跑 `scripts/build-npm-binaries.sh`，产物在 `bin/npm/`。）

### 唯一的环境变量

| 变量 | 默认 | 含义 |
|---|---|---|
| `RELAYFIRST_RELAY` | `http://localhost:8080` | 要连的 relay 节点。**这里没有地方放密钥** —— 和最小节点不收密钥是同一个理由。 |

**先起一个节点**（否则 MCP 连不上）：

```bash
relayfirst-node --listen :8080 --storage ./relayfirst-node.db
# 或
docker run -p 8080:8080 ghcr.io/relayfirst/node   # 镜像尚未发布，见判据 ③
```

---

## 3. 它提供什么

| 工具 | 作用 | 会签名吗 |
|---|---|---|
| `list_tasks` | 列任务板上的任务 | ❌ 只读 |
| `claim_task` | 认领任务 | ❌ 只读（claim 是"兴趣"，非"授予"） |
| `list_agents` | 列 Agent Card 目录 | ❌ 只读 |
| `query_observations` | 查观测索引 | ❌ 只读 |
| `relay_message` | 转发**已签**的字节（base64 原样） | ❌ **不解码、不重编码**（重编码会改变签名覆盖的字节） |

`initialize` 的 `instructions` 会**明说本服务不持密钥**；`tools/list` **不提供任何签名类工具**
（测试断言无 `sign` / `publish_card` / `submit_receipt` / `decrypt`）。

---

## 4. 诚实的边界

- **它不能验证它中继的东西。** 这不是缺口：relay 不是权威（`ARCHITECTURE.md` §5.7），
  且生产者 CLI 在签名前已验证。**要检查回执，用你自己的验证器**（判据 ②）。
- **签名永远留在本地 CLI。** MCP 只搬运已签字节；agent 想产出新签名，走
  `relayfirst` / `relayfirst session` 命令行，那里才是持密钥的地方。
- **它只连一个节点。** 多 relay 的 quorum / failover 在客户端逻辑里（`internal/publish`），
  不在 MCP。

---

## 5. 实测

```
$ CGO_ENABLED=0 go run ./cmd/relayfirst-mcp      # 起节点后，stdin 喂 initialize
→ initialize: serverInfo.name = "relayfirst-mcp"
→ tools/list: list_tasks, claim_task, list_agents, query_observations, relay_message
```

导入图断言（本文第 1 节那条）在 `scripts/ci.sh` 的 **Import-graph separation** 门禁里。

相关：`cmd/relayfirst-mcp/main.go`（包注释是权威说明）、`TASKS.md` S12-5/S12-6/S12-7。
