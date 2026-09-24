# Sael 网关

网关位于 `gateway/`，独立于现有 CLI 和 SDK。首次启动时审查关闭，所有受支持请求按原协议转发。管理员登录后，在设置页配置转发目标和场景规则，再开启审查。

## Docker Compose 部署

1. 复制 `deploy/.env.example` 为 `deploy/.env`，填写 PostgreSQL 密码和管理员密码。`DATABASE_URL` 中的密码要与 `POSTGRES_PASSWORD` 一致；转发目标可以启动后在设置页填写。
2. 在 `gateway/deploy/` 运行 `docker compose --env-file .env up --build -d`。
3. 打开 `http://localhost:8080`，用 `ADMIN_PASSWORD` 登录。在「设置」保存转发目标。在「Jev 连接与调试」保存分类器地址、模型、API Key 和超时，并用文本测试分类器。在「场景规则」中为每条场景配置审核项原始分数、任一/全部条件和处理动作，再开启审查。首次启动默认透传。

Compose 只把网关绑定到本机 `127.0.0.1:8080`。需要对外提供服务时，在前面配置 HTTPS 反向代理及访问控制。转发目标只能是 HTTP(S) 根地址，不能包含路径；请求路径、查询参数和客户端认证头会原样转到目标站。

支持 `POST /v1/chat/completions`、`POST /v1/responses`、`POST /v1/messages`，以及 Gemini `POST /v1beta/models/*:generateContent` 和 `*:streamGenerateContent`。只提取最后一个当前用户输入的文本；图片、音频等本身不会送往分类器。

## 本地开发与测试

```sh
cd gateway
go test ./...

cd web
npm ci
npm test
npm run build
```

数据库集成测试需设置 `TEST_DATABASE_URL`，指向独立的 PostgreSQL 测试库。开发前端运行 `npm run dev`，Vite 将 `/admin` 代理到本地 Go 网关。网关需要 `DATABASE_URL`、`UPSTREAM_URL` 和 `ADMIN_PASSWORD`；分类器每次调用使用数据库中已保存的 Jev 配置。管理 API 不回传密钥明文；数据库管理员仍可读取所保存的密钥，应限制数据库访问与备份权限。

本地构建网关需要 Go 1.25 或更新版本；Docker 镜像使用 Go 1.26 构建。前端使用 Node.js 22。

命中与审查失败事件写入 PostgreSQL；事件或分钟计数写库失败时落到卷中的 JSONL 文件，数据库恢复后由网关重放，计数重放会去重。运行中数据库暂时不可用时，网关使用最近一次成功读取的策略。正常未命中请求只计入分钟统计。文本预览默认不配置，也不会保存；启用预览后，网关会去除常见邮箱和密钥模式，但不能保证识别所有敏感内容，生产环境可设为 0。
