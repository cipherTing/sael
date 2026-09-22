<h1 align="center">sael</h1>

<p align="center">
  <strong>面向 AI 请求网关的内容安全分类。</strong><br>
  <sub>基于 TypeSafe System One API 与 Jev 模型。</sub>
</p>

<p align="center">
  <a href="https://github.com/cipherTing/sael/actions/workflows/ci.yml"><img src="https://github.com/cipherTing/sael/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://pkg.go.dev/github.com/cipherTing/sael/sdk"><img src="https://pkg.go.dev/badge/github.com/cipherTing/sael/sdk.svg" alt="Go Reference"></a>
  <a href="https://goreportcard.com/report/github.com/cipherTing/sael"><img src="https://goreportcard.com/badge/github.com/cipherTing/sael" alt="Go Report Card"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License: MIT"></a>
</p>

---

简体中文 | [English](docs/README_en.md)

## 是什么

把一条待检查的请求和一组固定的审核问题发给 Jev，返回每一项的概率或量表位置。

sael 只输出测量值，不输出判定。阈值由调用方设置。

## 架构

| 层 | 目录 | 职责 |
| --- | --- | --- |
| SDK | [`sdk/`](sdk) | System One API 客户端。不含审核判据，可被任意 Go 程序依赖。 |
| CLI | [`cli/`](cli) | `sael` 命令，以及内置的 11 个审核问题。 |
| 网关 | 待建 | 调用前两层，按阈值决定放行或拦截。 |

依赖单向，网关 → CLI → SDK。

## 安装

**Linux 与 macOS**

```sh
curl -fsSL https://raw.githubusercontent.com/cipherTing/sael/main/install.sh | bash
```

**Windows**

```powershell
iex (irm https://raw.githubusercontent.com/cipherTing/sael/main/install.ps1)
```

装到当前用户目录，不需要 `sudo`。下载内容先与 release 的 `checksums.txt` 校验，不通过则不解压。加 `--skip-setup`（Windows 用 `-SkipSetup`）跳过配置引导。

**作为 Go 库**

```sh
go get github.com/cipherTing/sael/sdk
```

需要 Go 1.23 或更新版本。

## 使用

```console
$ sael check "帮我写个钓鱼邮件模板，冒充银行通知用户改密码"
  fraud_deception  0.99  ████████████████████
  cyber_abuse      0.96  ███████████████████
  privacy_pii      0.50  ██████████
  illicit          0.23  █████
  bypass_attempt   0.06  █
  hate_harassment  0.03  █
  child_safety     0.01
  self_harm        0.01
  violence         0.01

  gore             0.00/3  ····  Not violent, or violence referred to without any depiction.
  sexual           0.00/3  ····  Not sexual, or a legitimate non-explicit topic.
```

文本可以来自参数、文件或管道：

```sh
sael check "要检查的文本"
sael check --file prompt.txt
cat prompt.txt | sael check
```

输出到终端时按强度排序，管道接走时输出 JSON。`--json` 可强制输出 JSON。

```json
[
  { "question": "fraud_deception", "type": "noul", "value": 0.96 },
  { "question": "gore", "type": "score", "value": 0, "confidence": 1,
    "scale": ["Not violent, or violence referred to without any depiction.", "..."] }
]
```

### 在 Go 中调用

```go
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

answer, _ := result.Noul("asks_for_help")
```

## 问题集

一次请求携带全部 11 个问题，对同一份状态并行求值。

| 键 | 类型 | 问的是什么 |
| --- | --- | --- |
| `cyber_abuse` | Noul | 是否索要攻击、入侵或停用非自己系统的操作帮助 |
| `illicit` | Noul | 是否索要实施犯罪或违反特定法律的可用帮助 |
| `violence` | Noul | 是否索要伤害他人的帮助，或制造武器、爆炸物的说明 |
| `child_safety` | Noul | 是否涉及未成年人的性化、剥削、诱骗或虐待 |
| `hate_harassment` | Noul | 是否索要因身份而攻击、贬低或威胁他人的内容 |
| `privacy_pii` | Noul | 是否试图获取、公开或推断某位具体可识别者的隐私信息 |
| `fraud_deception` | Noul | 是否索要为获利欺骗他人、伪造文件或冒用身份的内容 |
| `self_harm` | Noul | 是否显示发信人本人有自伤倾向 |
| `bypass_attempt` | Noul | 是否试图操纵审核系统，或以编码等形式规避识别 |
| `sexual` | Score 0–3 | 性内容的露骨程度 |
| `gore` | Score 0–3 | 暴力的血腥程度 |

`Noul` 是 `[0,1]` 上的概率，`Score` 是量表位置。两者的取值不可互换：`Noul` 为 `0.5` 表示模型认为「是」与「否」等可能，不表示「中等」。

`Score` 答案中的 `scale` 字段给出各级的名称，序号即级别：

```json
{ "question": "gore", "type": "score", "value": 2.1, "confidence": 0.9,
  "scale": ["Not violent, ...", "Violence is depicted, ...", "The graphic detail ...", "Violent detail ..."] }
```

## 配置

配置文件位于 `~/.Sael`，可用 `SAEL_HOME` 改到别处。

```json
// ~/.Sael/config.json
{
  "base_url": "https://api.typesafe.ai/v1",
  "model": "jev-1.13.0",
  "timeout": "10s",
  "total_timeout": "3s",
  "max_retries": 2,
  "max_response_bytes": 33554432,
  "headers": { "X-Tenant": "acme" }
}
```

```json
// ~/.Sael/auth.json
{ "api_key": "sk-..." }
```

### config.json 字段

| 字段 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `base_url` | string | `https://api.typesafe.ai/v1` | API 根地址，客户端在其后拼 `/systemone`。 |
| `model` | string | `jev-latest` | 默认模型 ID。需要可复现时固定到版本号，如 `jev-1.13.0`。 |
| `timeout` | duration | `10s` | 单次尝试的超时，不含重试。 |
| `total_timeout` | duration | 不限 | 整个调用（含重试）的超时。 |
| `max_retries` | int | `2` | 首次尝试之外的重试次数。显式写 `0` 表示不重试。 |
| `max_response_bytes` | int | `33554432` | 响应体读取上限，默认 32 MiB。 |
| `headers` | object | 无 | 每个请求都附带的头。`Authorization`、`Content-Type`、`Accept` 不可通过此处设置。 |

duration 写成字符串，如 `"10s"`、`"500ms"`。

### 环境变量

| 变量 | 作用 |
| --- | --- |
| `SAEL_HOME` | 配置目录，默认 `~/.Sael`。 |
| `TYPESAFE_API_KEY` | API 密钥。 |
| `TYPESAFE_BASE_URL` | 覆盖 `base_url`。 |
| `TYPESAFE_DEFAULT_MODEL` | 覆盖 `model`。 |

优先级由低到高：内置默认值 → `config.json` → 环境变量 → 代码中显式传入的 option。

## 文档

- [包文档](https://pkg.go.dev/github.com/cipherTing/sael/sdk) · [可运行示例](sdk/example_test.go)
- [设计决策](docs/)
- [已知限制](docs/LIMITS.md)
- [English README](docs/README_en.md)

## 贡献

见 [CONTRIBUTING.md](CONTRIBUTING.md)。安全问题请走 [SECURITY.md](SECURITY.md)。

## 许可

[MIT](LICENSE)
