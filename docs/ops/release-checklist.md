# 发布 checklist（npm + 节点镜像）

> **用途**：判据 ③（镜像）+ `npx` 零安装入口的**一次性发布动作**。**需要凭据（你）。**
> **现状**：打包**已就绪**（PKG-1 分包 done）；**只差 `npm publish` 与 `docker push`**。
> **配套**：`planning.md §C1/C2`、`npm-packaging-plan.md`、`Dockerfile`、`scripts/ci.sh`（打包/launcher 门禁）。

---

## 0. 前置（一次性）

- [ ] **npm 帐号**，且是该 scope 的 owner（`@relayfirst`，或改 `package.json` 的 scope 为你所有）。
- [ ] `npm login` 成功。
- [ ] **镜像 registry** 选定（Docker Hub / GHCR），本地 `docker login` 成功。
- [ ] **CI 全绿**：`bash scripts/ci.sh`（含 npm 打包门禁 + 交叉编译 + launcher 解析）。

---

## 1. npm：**顺序不能反**

> ⚠️ **先发 5 个 `@relayfirst/<os>-<arch>` 子包，再发主包。** 主包的 `optionalDependencies`
> 指向的版本**必须先存在**，否则 `npx` 解析不到（`npm-packaging-plan.md §4`）。

```bash
# 0. 打包（prepublishOnly 也会跑，但先跑一次看输出）
bash scripts/build-npm-binaries.sh      # 交叉编译 5 平台 × 3 工具
bash scripts/pack-platform-packages.sh  # 生成 5 个平台子包

# 1. 先发子包（每个目录里 npm publish）
for d in dist/npm/@relayfirst/*/; do (cd "$d" && npm publish --access public); done

# 2. 再发主包（三个用户工具的主包）
npm publish --access public
```

**要发的物**（**节点刻意不发 npm** —— 长驻服务走 Docker）：

| 物 | `npx` 入口 |
|---|---|
| `relayfirst` CLI | `npx relayfirst …` |
| `relayfirst-mcp` | MCP stdio |
| `relayfirst-dashboard` | `npx relayfirst-dashboard …` |

**发后验证**（干净环境）：

```bash
cd $(mktemp -d)
env -i PATH=<只含 node> npx relayfirst@latest version   # 应打印版本，且 PATH 里没有 go
```

- [ ] 子包全部 `published`，主包随后 `published`
- [ ] 干净环境 `npx` 跑通（证明走预编译二进制，非 `go build`）
- [ ] `npm view relayfirst` 的 `optionalDependencies` 指向的版本**都已存在**

---

## 2. 节点镜像 → registry

`Dockerfile` 已就位（**构建阶段 golang:1.27-alpine，运行阶段 alpine:3.20，`VOLUME /data`，`EXPOSE 8080`，`ENTRYPOINT relayfirst-node`**）。

```bash
docker build -t relayfirst/node:latest .
docker tag  relayfirst/node:latest <registry>/relayfirst/node:latest
docker push <registry>/relayfirst/node:latest
```

**发后验证**（干净机器，判据 ③ 的载体）：

```bash
docker run -d --name relayfirst-node -p 8080:8080 \
  -v relayfirst-data:/data <registry>/relayfirst/node:latest
curl -sf http://localhost:8080/healthz   # 200
```

- [ ] 镜像已 push
- [ ] 另台机器 `docker run` 起来，`/healthz` = 200
- [ ] **无需注册 / 许可**（MVP 硬要求）

---

## 3. 发布后要更新的文档

- [ ] `status-map.md §1`：判据 ③ 的"镜像未发布" → 已发布
- [ ] `mvp2-launch-readiness.md`：§C 去掉"未 publish"
- [ ] `planning.md §C1`：标 done
- [ ] `docs/notes/mcp-setup.md`：删"本地二进制回退"段（打包发布后不再需要）

> **提示**：`npm publish` 与 `docker push` 各是**一条命令**，但需要凭据与一次干净环境验证。**没有凭据，这一步做不了。**
