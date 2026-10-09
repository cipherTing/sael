# 网关 Docker 镜像发版

网关镜像发布到公开 Docker Hub 仓库 `sundayting/sael-gateway`，由 `.github/workflows/gateway-release.yml` 在 GitHub Actions 构建。部署端无需源码或构建工具。

## 一次性配置

1. 在 Docker Hub 的 `sundayting` 账号下创建公开仓库 `sael-gateway`。
2. 创建专用于 Actions、具有镜像推送权限的 Docker Hub Personal Access Token。
3. 在 GitHub 仓库 **Settings → Environments → 通用** 配置：环境变量 `DOCKERHUB_USERNAME=sundayting`，环境秘密 `DOCKERHUB_TOKEN` 为该 PAT。Token 直接填入 GitHub Secrets。

网关发布 job 显式使用 `environment.name: 通用`，因此可以读取该环境中的变量和秘密；`deployment: false` 避免为镜像发布创建服务器部署记录。CI 检查 job 无需 Docker Hub 凭据。

发布 job 使用 GitHub 自动提供的 `GITHUB_TOKEN` 创建 Release，权限为 `contents: write`；CI job 只读仓库。

## 独立版本

| 组件 | Git 标签 | 发布物 |
| --- | --- | --- |
| SDK | `sdk/vX.Y.Z` | Go 模块 |
| CLI | `cli/vX.Y.Z` | CLI 安装包与 Release |
| 网关 | `gateway/vX.Y.Z` | Docker 镜像与网关 Release |

同一个 commit 可以同时具有多个组件标签。各工作流读取触发自己的标签，不根据该提交上其他组件的标签推测版本。

网关标签支持 `gateway/v0.1.0`、`gateway/v0.1.0-rc.1` 等语义版本。Docker 标签不支持 `+`，因此网关发布标签不带构建元数据。版本由标签自动提取，部署配置无需版本环境变量。

## 发布

在准备发布的提交上创建并推送标签。例如：

```sh
git tag -a gateway/v0.1.0 -m 'Sael Gateway 0.1.0'
git push origin gateway/v0.1.0
```

Actions 先调用同一提交的完整 CI，检查成功后构建 `linux/amd64`、`linux/arm64` 镜像，并推送 Docker Hub。正式版本发布精确标签和 `latest`；预发布版本只发布精确标签。

镜像内包含同一源码提交构建的网关、CLI 和前端，保持内部协议一致；网关镜像版本不要求 CLI 或 SDK 同时发版。网关二进制、启动日志及 OCI 标签携带网关版本与源码提交。

发布流程验证两个目标平台及程序 `--version`，然后创建 `gateway/v*` GitHub Release，附带 `compose.yaml`、`env.example`、镜像 digest 和部署说明。Release 附件使用 `env.example`，因为 GitHub 会改写以点开头的附件名；源码中的配置模板仍为 `.env.example`。网关 Release 设置 `--latest=false`，保留仓库整体 Latest 给 CLI；CLI 安装脚本也只筛选 `cli/v*`。网关主页按 `gateway/v*` 筛选 Release 检测更新。

## 用户部署

从 Release 下载部署文件，复制 `env.example` 为 `.env` 并填写密码、加密密钥：

```sh
docker compose up -d --wait
```

默认使用 `sundayting/sael-gateway:latest`。更新网关：

```sh
docker compose pull gateway
docker compose up -d --no-deps --wait gateway
```

固定版本或测试 RC 时，手动修改 `compose.yaml` 的镜像标签。使用 RC Release 的附件部署时也需这样修改，因为公共 Compose 的默认值始终为 `latest`。

## 依据

- [Docker 官方 GitHub Actions 构建与推送示例](https://docs.docker.com/build/ci/github-actions/push-multi-registries/)
- [Compose 镜像拉取策略](https://docs.docker.com/reference/compose-file/services/#pull_policy)：`latest` 在默认策略下也会拉取。
- [Docker 多平台 Go 交叉编译](https://docs.docker.com/build/building/multi-platform/#cross-compiling-a-go-application)
- [GitHub Release API](https://docs.github.com/en/rest/releases/releases#create-a-release)：`make_latest` 为仓库范围设置。
- [Sub2API 镜像部署示例](https://github.com/Wei-Shaw/sub2api/blob/3a6fd1c9db07203ca308aaba69e502bc1f35b307/deploy/docker-compose.yml#L19)
- [File Browser 发版检查依赖](https://github.com/filebrowser/filebrowser/blob/833d908884d5c801f30f5c098d7977177eb3a36b/.github/workflows/ci.yaml#L92)
