# Sael 网关部署与开发

[项目介绍与截图](../README.md) · [CLI 与 SDK](../docs/cli.md)

## Docker Compose

```sh
cd gateway/deploy
cp .env.example .env
# 编辑 .env：填写 ADMIN_PASSWORD、POSTGRES_PASSWORD、REDIS_PASSWORD 和 UPSTREAM_URL
docker compose up --build -d
docker compose ps
```

首次构建需要下载 Node.js、Go、Alpine、PostgreSQL 和 Redis 镜像。历史数据保存在 PostgreSQL 卷；Redis 卷保存待入库统计、异常记录和会话冻结；Redis 不可用时写入独立的本地暂存卷。

| 默认地址 | 用途 |
| --- | --- |
| `http://localhost:8080` | 运维控制台与 `/admin/*` API |
| `http://localhost:8081` | 客户端业务进网 |

两个端口在启动时确定，控制台不能动态修改。管理端口不转发业务，进网端口不提供管理 API；两端均有 `/healthz`。Compose 默认绑定 `127.0.0.1`；对外提供服务时，通过域名反向代理，或在 `.env` 中调整对应绑定地址。

### `.env` 配置

无需编辑 `compose.yaml`。包含 `$`、`#` 或空格的值建议用单引号包住；不要提交 `.env`。

| 变量 | 默认值 | 用途 |
| --- | --- | --- |
| `COMPOSE_PROJECT_NAME` | `sael` | Compose 项目和数据卷名称 |
| `ADMIN_PASSWORD` | 必填 | 控制台登录密码 |
| `POSTGRES_PASSWORD` | 必填 | 内置数据库密码；网关自动编码连接串中的特殊字符 |
| `REDIS_PASSWORD` | 必填 | 内置 Redis 密码 |
| `REDIS_HOST` / `REDIS_PORT` | `redis` / `6379` | Redis 连接地址 |
| `REDIS_URL` | 空 | 可选外部 Redis 连接串，覆盖 Redis 连接字段 |
| `ADMIN_BIND_IP` / `INGRESS_BIND_IP` | `127.0.0.1` | 两个宿主机监听地址 |
| `ADMIN_PORT` / `INGRESS_PORT` | `8080` / `8081` | 两个宿主机端口 |
| `INGRESS_PUBLIC_URL` | `http://localhost:<INGRESS_PORT>` | 控制台展示的外部进网地址，不改变监听 |
| `UPSTREAM_URL` | 空 | 首次导入的出站根地址，如 `https://api.example.com` |
| `TRUSTED_PROXY_CIDRS` | 空 | 可信代理网段，逗号分隔；用于解析来源 IP |
| `JEV_MAX_INPUT_TOKENS` | `28800` | 首次导入的送审上限，使用本地 Token 估算 |
| `MAX_REQUEST_BODY_SIZE` | `268435456` | 请求体读取上限，256 MiB |
| `MAX_HEADER_BYTES` | `65536` | 请求头上限，64 KiB |
| `READ_HEADER_TIMEOUT` / `IDLE_TIMEOUT` | `10s` / `120s` | 读取请求头、空闲连接超时；不设置整段上传或 SSE 响应超时 |
| `STOP_GRACE_PERIOD` | `30s` | Docker 停止宽限期；网关等待后台审查最多 10 秒后取消剩余任务 |
| `ASYNC_REVIEW_CONCURRENCY` | `256` | 每实例非阻塞审查的在途任务上限；满时跳过本次审查并累计进网、输出限频运行告警，不排队阻塞转发 |
| `CLASSIFIER_TIMEOUT` | `5s` | 分类器默认超时；已保存的 Jev 设置优先 |
| `POSTGRES_USER` / `POSTGRES_DB` | `sael` / `sael` | 内置数据库用户和库名 |
| `DB_HOST` / `DB_PORT` / `DB_SSLMODE` | `db` / `5432` / `disable` | 网关的数据库连接参数 |
| `DATABASE_URL` | 空 | 可选完整连接串；填写后覆盖上面的连接参数 |

`UPSTREAM_URL` 与 `JEV_MAX_INPUT_TOKENS` 只初始化尚未保存的配置，重启不会覆盖管理员在控制台的修改。Jev 地址、模型和密钥在控制台设置，密钥不会明文回传给前端。使用已有数据库卷时，修改 `.env` 中的数据库密码不会修改库中已有用户的密码。外部数据库应按其要求设置 TLS；当前 Compose 仍会启动内置数据库和 Redis 服务。

### 统计写入与故障恢复

请求只向有界内存队列入队，后台合并统计后写 Redis Streams，再批量入库。队列满时背压，不按采样丢弃；正常退出先排空队列。配置读取使用进程内快照，保存后通知其他实例，并每秒核对一次数据库；会话冻结使用 Redis TTL。

Redis 不可用时，去敏后的批次先写本地暂存并刷盘，恢复后重放。数据库的数据写入和消费进度在同一事务中提交，重放不会重复累计。多个实例需要共享同一 PostgreSQL 和 Redis；各实例使用自己的暂存卷。

持久性边界：进程强杀可能丢失尚在内存队列的批次；Compose 的 Redis AOF 使用 `everysec`，Redis 主机崩溃可能再丢失最近约一秒数据。Redis 和本地磁盘同时不可写时，后台保留当前批次并重试，队列满后请求会等待。备份或恢复时应协调 PostgreSQL、Redis 与暂存卷。

[性能结果与复现方法](../docs/gateway-performance.md)。网关默认使用进程内 SDK；显式设置 `SAEL_CLI_PATH` 可继续使用旧 CLI 接入，该方式不属于 5,000 RPS 的验收配置。

### 接入流程

1. 使用 `ADMIN_PASSWORD` 登录。
2. **接入**：保存出站根地址。不含 `/v1`、其他路径或认证信息；网关保留客户端路径、查询参数、原始正文和上游凭据。
3. **设置**：保存 Jev 地址、模型、API Key、超时和送审上限。可以测试尚未保存的连接；留空密钥表示保留已有值。
4. **场景**：添加条件，填写原始分数阈值，选择端点、模型、满足任一/全部以及阻塞性／非阻塞性审查。拖拽排序，或从已有场景创建副本。
5. 用文本试算草稿，检查分数和匹配过程；试算不请求出站服务、不生成生产统计。
6. 保存并开启审查。首次启动默认关闭；未匹配场景的请求放行，不保存逐条事件。

客户端继续使用上游原有密钥，将 API 根地址换成网关的进网地址。例如 OpenAI 客户端通常使用 `http://localhost:8081/v1`。

## 监控端点

仅以下 POST 路径进入监控。其他方法和路径直接转发，不调用 Jev、不计入审核统计。

| 端点 | 路径 | 送审文本 |
| --- | --- | --- |
| Chat Completions | `/v1/chat/completions` | 最后的当前用户输入 |
| Responses | `/v1/responses` | 当前 `input` 文本 |
| Messages | `/v1/messages` | 最后的当前用户输入 |
| Images Generations | `/v1/images/generations` | JSON `prompt` |
| Images Edits | `/v1/images/edits` | multipart 或 JSON 的 `prompt` |
| Images Variations | `/v1/images/variations` | 无文本，跳过文本审查 |

图片、mask、音频、历史消息、工具结果全部忽略。multipart 原始字节和 boundary 原样转发，不重组文件。审查范围由启用场景中的端点和模型决定；没有适用场景的请求直接转发，不调用 Jev、不生成逐条记录。

### 登录与可信密钥

控制台登录按真实来源 IP 每分钟最多尝试 3 次，Redis 统一计数。超限返回 `429` 和 `Retry-After`，首次冷却 60 秒；重复触发按 120、240、480、900 秒退避，最高 15 分钟。冷却期间的请求不延长截止时间；一个小时没有尝试后重置退避。Redis 不可用时暂停登录。反向代理后部署时，设置准确的 `TRUSTED_PROXY_CIDRS`。

调用密钥第一次进入网关时直接透传，不读取审查正文、不调用 Jev。只有受监控 POST 端点返回 2xx 且响应类型为 JSON 或 SSE 时才自动建立信任；该首次请求及信任建立前已进入的请求不补审。公共路径和 HTML 页面不会建立信任。这个判断使用上游 HTTP 响应状态，不扫描响应正文。

可信列表在 Redis 中仅保存绑定出站目标的密钥指纹与最后请求时间，默认闲置 **30 天**自动清除。在 **设置 → 可信密钥** 修改闲置天数；有效请求续期，后台每 30 秒清理过期项。上游返回 401 时撤销信任，切换出站目标后需要重新学习。未知密钥不会创建缓存项，首次验证窗口内不进行内容拦截。

### 审查执行方式

- **阻塞性审查**：等待 Jev 结果，按场景优先级决定拦截或转发。
- **非阻塞性审查**：转发与审查并行；客户端响应结束后后台审查继续执行，命中或 Jev 失败时生成记录。未命中只累计统计。审查并发已满时跳过本次审查，不等待名额。
- 同一请求存在适用的阻塞场景时，先等待一次审查结果，再按场景优先级决定最终结果；不会为每条场景重复调用 Jev。

方法、路径、审查开关、端点场景范围和可信密钥检查在正文解析前完成；候选请求先读取模型字段，确定场景适用后再提取文本及请求参数。无需审查的请求保留流式透传。

### 规则与拒绝响应

9 项概率值使用 0–1，色情和血腥程度使用 0–3。条件为**严格大于**各自阈值。场景按顺序匹配，首个匹配决定结果，其他匹配保留用于分析。模型列表为空表示全部模型，非空时精确匹配模型名。

拦截遵循 sub2api 安全审核的端点分支，HTTP 状态统一为 **403**，消息统一为 `Request denied.`，错误码为 `prompt_guard_blocked`。具体风险、阈值、分数、原始文本和冻结信息不会发送给客户端。

| 端点 | 返回格式 |
| --- | --- |
| Chat、Images | `{"error":{"type":"permission_error","code":"prompt_guard_blocked","message":"Request denied."}}` |
| Responses | `{"error":{"type":"api_error","code":"prompt_guard_blocked","message":"Request denied."}}` |
| Messages | `{"type":"error","error":{"type":"permission_error","code":"prompt_guard_blocked","message":"Request denied."}}` |

这些是网关的拒绝响应，保留各端点的兼容格式；自定义错误码不冒充官方错误码。响应携带 `X-Request-Id`，Messages 还携带 `Request-Id`。即使请求为流式，转发前的拒绝也返回 JSON，不先开启 SSE。

### 会话冻结

在 **设置 → 会话冻结** 开启，默认关闭，默认时长 **60 分钟**。命中拦截场景后，后续同会话请求在调用 Jev 前直接拒绝；到期释放，冻结期间重试不会续期。

- 冻结键由**调用凭据 + 显式会话 ID** 计算 SHA-256，同名会话在不同凭据之间隔离。冻结表只保存哈希和到期时间，重启后仍有效。
- 凭据优先读取 `X-Api-Key`，否则读取 Bearer；会话优先读取 `session-id`、`session_id`、`X-Session-Id`、`X-Claude-Code-Session-Id`，Messages 还支持 `metadata.user_id` 中显式嵌入的 Claude Code 会话 ID。
- 缺少凭据或会话 ID 时，不建立冻结；不会猜测会话、按 IP 冻结或封禁整个密钥。
- 冻结在启用场景的端点、模型范围内生效；非监控请求、没有适用场景、总审查关闭时仍转发。冻结开关关闭后恢复正常逐次审核。
- 冻结重试生成 `session_blocked` 网关警告，单独计数，不计为新的场景命中或 Jev 故障。冻结存储不可用时继续普通审核，并在服务日志报告存储问题。

### Jev 输入上限与失败处理

[Jev 官方模型说明](https://docs.typesafe.ai/models)列出两个上限：整次请求 64k tokens（state 与全部问题），以及 state 加最长单题 32k tokens。Sael 默认把后者的 90%，即 **28,800**，用作文本送审上限，可在 **设置 → Jev 分类器** 调整。

Jev 没有公开分词器。Sael 使用本地 `cl100k_base` **估算**输入 Token，不是 Jev 精确计数或计费数据；预留 10% 也不保证所有语言都能精确贴合服务端边界。词表随程序打包，运行时不下载。

| 情况 | 请求处理 | 记录与统计 |
| --- | --- | --- |
| 估算输入超过上限 | 不截断、不调用 Jev，原样放行 | `classifier_input_too_long` 警告，记录估算量与上限；文本仅保留去敏预览 |
| Jev 不可用、超时或分数无效 | 放行 | Jev 失败记录，计入失败率 |
| 正常未命中 | 放行 | 仅计数和耗时聚合，不存分类分数 |
| 上游服务返回错误 | 转发原响应 | 不记录为审核故障 |

控制台的连接测试和场景文本试算使用相同的输入预检。网关不另外限制请求体大小，也不增加分类并发排队限制。

## 总览与记录

请求量、命中/拦截比例、Jev 失败率及耗时共用时间轴。支持端点/模型筛选、流量明细、场景排行、耗时热力图和下钻记录。完整日期逐桶显示，空间不足时图内横向滚动；快捷时间直接生效，自定义起止时间点“应用”后生效。

- **命中率** = 场景命中 / 完成审查。
- **拦截率** = 场景拦截 / 完成审查。会话冻结拦截单独统计。
- **Jev 失败率** = Jev 失败 /（完成审查 + Jev 失败）。输入超限和冻结重试不进入分母。
- 分母为零显示 `—`；P50/P95 通过耗时分桶估算上界，不平均各时间桶的分位数。

进网计数在转发前完成，不等待 SSE 结束。事件和计数暂时写库失败时，写入持久卷的 JSONL，恢复后重放并对计数去重。新增统计从升级后开始积累，不补造旧数据。

### 请求记录与去敏

记录分为**场景命中、Jev 失败、网关警告**。包含端点、模型、IP、客户端、显式会话 ID、客户端请求 ID、流式参数，以及适用的当前文本、分数和匹配过程。未达到条件的分数默认折叠。

请求参数采用白名单，包括输出 Token 上限、温度、推理强度、服务等级、工具数量、输出格式，及 Responses 的会话/前序响应 ID、Messages 的思考设置。不会保存任意请求头、全部 metadata 或历史消息。

来源 IP 默认取 TCP 对端；配置 `TRUSTED_PROXY_CIDRS` 后，才沿可信代理链解析转发头。请求文本、预览和参数在写库或暂存前去敏，覆盖常见邮箱、手机号、身份证、Bearer/Basic 凭据、API Key、JWT、私钥和敏感键值。JSON 递归处理，普通文本用正则；先去敏，再截预览。规则不保证识别所有个人信息，Jev 与出站服务仍接收原始文本。

## 本地开发与测试

网关需要 Go 1.26，前端使用 Node.js 22、React、shadcn/ui、ECharts、TanStack Query/Table 与 dnd-kit。

```sh
cd gateway
go vet ./...
go test -race ./...

cd web
npm ci
npm test
npm run build
npm run dev
```

本地 Go 进程需配置数据库、`REDIS_URL` 和 `ADMIN_PASSWORD`。Vite 将 `/admin` 代理到 `127.0.0.1:8080`，后端端口不同时用 `SAEL_ADMIN_URL=http://127.0.0.1:8090 npm run dev`。

数据库集成测试会清空相关表和 Redis 测试数据库，必须使用**独立测试库**；未设置连接串时，这些测试会跳过：

```sh
TEST_DATABASE_URL='postgres://user:password@localhost:5432/sael_test?sslmode=disable' \
TEST_REDIS_URL='redis://localhost:6379/15' \
  go test -race ./... -count=1
```

从仓库根目录执行 `make test` 可测试三个 Go 模块；需要 GNU Make 4。设计背景见 [网关重构设计](../docs/gateway-redesign.md)。
