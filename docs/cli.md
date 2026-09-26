# CLI 与 Go SDK

[项目介绍](../README.md) · [English](cli_en.md)

CLI 内置 11 个审核问题；Go SDK 可以自定义问题。它们返回分类分数，不执行网关的拦截策略。

## 安装

Linux / macOS：

```sh
curl -fsSL https://raw.githubusercontent.com/cipherTing/sael/main/install.sh | bash
```

Windows PowerShell：

```powershell
iex (irm https://raw.githubusercontent.com/cipherTing/sael/main/install.ps1)
```

安装到当前用户目录，无需管理员权限。安装器验证发布包校验和，随后引导配置；用 `--skip-setup` 或 `-SkipSetup` 跳过引导。

从源码构建：

```sh
cd cli
go build -o sael ./cmd/sael
./sael setup
```

## 命令行

```sh
sael check "需要审查的文本"
sael check --file prompt.txt
cat prompt.txt | sael check --json
```

终端默认显示按分数排序的结果；管道输出默认 JSON，`--json` 可强制使用 JSON。一次请求并行评估全部 11 项。

| 审核项 | 键名 | 返回值 |
| --- | --- | --- |
| 网络滥用 | `cyber_abuse` | 0–1 概率 |
| 违法活动 | `illicit` | 0–1 概率 |
| 暴力伤害 | `violence` | 0–1 概率 |
| 未成年人安全 | `child_safety` | 0–1 概率 |
| 仇恨与骚扰 | `hate_harassment` | 0–1 概率 |
| 隐私泄露 | `privacy_pii` | 0–1 概率 |
| 欺诈与欺骗 | `fraud_deception` | 0–1 概率 |
| 自伤风险 | `self_harm` | 0–1 概率 |
| 审查绕过 | `bypass_attempt` | 0–1 概率 |
| 色情程度 | `sexual` | 0–3 程度 |
| 血腥程度 | `gore` | 0–3 程度 |

概率与程度量表含义不同：概率 0.5 表示判断不确定，不表示严重程度中等。程度结果的 `scale` 字段包含各档说明。

## Go SDK

需要 Go 1.23 或更新版本。

```sh
go get github.com/cipherTing/sael/sdk
```

在你的 Go 程序中：

```go
import (
    "os"
    "github.com/cipherTing/sael/sdk"
)

// ctx 为请求上下文，userMessage 为需要分类的文本。
client, err := sdk.New(sdk.WithAPIKey(os.Getenv(sdk.EnvAPIKey)))
if err != nil {
    return err
}
result, err := client.Evaluate(ctx, userMessage, sdk.Questions{
    "asks_for_help": sdk.NoulQuestion{
        Instructions: "Is this request asking for help breaking the law?",
    },
})
if err != nil {
    return err
}
answer, ok := result.Noul("asks_for_help")
```

完整可运行程序见 [quickstart](../sdk/examples/quickstart/main.go)，更多问题类型见 [SDK 示例](../sdk/example_test.go)与[包文档](https://pkg.go.dev/github.com/cipherTing/sael/sdk)。

## 分类器配置

CLI 使用 `~/.Sael`，可通过 `SAEL_HOME` 修改目录。网关使用控制台保存的配置，与这里的 CLI 配置独立。

`config.json` 示例：

```json
{
  "base_url": "https://api.typesafe.ai/v1",
  "model": "jev-latest",
  "timeout": "10s",
  "max_retries": 2
}
```

`auth.json` 保存 `{"api_key":"你的密钥"}`，也可使用环境变量 `TYPESAFE_API_KEY`。

| 字段 | 默认 | 用途 |
| --- | --- | --- |
| `base_url` | `https://api.typesafe.ai/v1` | API 根地址，客户端追加 `/systemone` |
| `model` | `jev-latest` | Jev 模型 ID |
| `timeout` | `10s` | 单次尝试超时 |
| `total_timeout` | 无 | 包括重试的总超时 |
| `max_retries` | `2` | 首次请求之外的重试次数，0 禁用重试 |
| `max_response_bytes` | `33554432` | SDK 缓冲分类响应的上限，与网关进网请求大小无关 |
| `headers` | 无 | 附加请求头，不能覆盖认证、Content-Type 和 Accept |

`TYPESAFE_BASE_URL`、`TYPESAFE_DEFAULT_MODEL` 可覆盖地址和模型。优先级为默认值 → 配置文件 → 环境变量 → 代码选项。

[已知限制](LIMITS.md) · [贡献指南](../CONTRIBUTING.md)
