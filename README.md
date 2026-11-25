<h1 align="center">ClaudeRelay</h1>

<p align="center">
  <b>一个轻量的本机 Anthropic API 转发与模型映射网关</b><br>
  面向 Claude Desktop 与兼容 Anthropic Messages API 的客户端 · 单文件二进制 · 无 Web UI · 无内置守护 · NSSM 托管
</p>

<p align="center">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go&logoColor=white&style=flat-square">
  <img alt="Windows" src="https://img.shields.io/badge/Platform-Windows%20x64-0078D4?logo=windows&logoColor=white&style=flat-square">
  <img alt="Single Binary" src="https://img.shields.io/badge/Deploy-Single%20Binary-2ea44f?style=flat-square">
  <img alt="NSSM" src="https://img.shields.io/badge/Service-NSSM-6f42c1?style=flat-square">
  <img alt="API" src="https://img.shields.io/badge/API-Anthropic%20Compatible-412991?style=flat-square">
  <img alt="License" src="https://img.shields.io/badge/License-MIT-green?style=flat-square">
</p>

---

> [!IMPORTANT]
> 本项目只提供协议转发和模型名映射，**不提供 API Key、模型账号或模型额度**。
>
> `Fable 5.2` 是客户端中显示的本地模型别名，**不是 Anthropic 官方模型名**。真实请求会被映射到你配置的上游模型。
>
> 默认只监听 `127.0.0.1:17863`，不会主动暴露到公网。

> [!NOTE]
> ClaudeRelay 与 CC Switch 相互独立。它不读取、不修改、不启动或停止 CC Switch；CC Switch 中的 Claude 配置删除后，ClaudeRelay 仍可单独工作。
>
> 本项目是纯后台二进制网关，**不提供 Web UI、网页管理面板、账号池或 Docker 面板**。运行参数全部来自 `config.json`。

## 功能

- Anthropic Messages API 兼容转发
- `/v1/messages`、`/v1/messages/count_tokens` 请求中的模型名改写
- 模型名改写只替换 `model` 字段的字节区间，请求体其余部分逐字节原样转发
- `/v1/models` 返回自定义模型 ID 和显示名
- 流式响应边接收边返回，支持 SSE
- 不需要改写模型的请求体直接流式透传，不整体缓冲到内存
- 未知模型可回退到统一上游模型
- 其他 API 路径透明转发到上游
- 单文件 Go 二进制，不内置 Node.js 运行时
- 不包含自守护逻辑，由 NSSM 负责启动、停止和异常重启
- 无 Web UI，配置完全由 `config.json` 管理
- GitHub Actions 构建 Windows 单文件二进制和发布 ZIP
- 不写请求日志或日志文件

## 工作方式

```text
┌────────────────┐    Anthropic API    ┌──────────────────────────┐
│ Claude Desktop │ ──────────────────> │ ClaudeRelay              │
│ 或其他兼容客户端 │   127.0.0.1:17863   │ 模型名映射 + 请求转发      │
└────────────────┘                     └────────────┬─────────────┘
                                                    │
                                                    │ 改写 model 字段
                                                    ▼
                                       ┌──────────────────────────┐
                                       │ 第三方网关 / Anthropic API │
                                       │ 例如 127.0.0.1:7863       │
                                       └──────────────────────────┘
```

以默认示例为例：

```text
客户端请求 model = claude-fable-5
             │
             ▼
ClaudeRelay 改写 model = cn:deepseek-v4.1-flash
             │
             ▼
转发到 http://127.0.0.1:7863
```

同时，ClaudeRelay 的 `/v1/models` 会返回：

```json
{
  "id": "claude-fable-5",
  "display_name": "Fable 5.2",
  "type": "model"
}
```

客户端读取模型目录后即可显示 **Fable 5.2**。

## 快速开始

### 1. 准备配置

创建部署目录并复制示例配置：

```powershell
New-Item -ItemType Directory -Path .\dist -Force | Out-Null
Copy-Item .\config.example.json .\dist\config.json
```

编辑 `dist\config.json`，至少确认以下三个值：

```json
{
  "listenPort": 17863,
  "upstreamBaseUrl": "http://127.0.0.1:7863",
  "defaultUpstreamModel": "cn:deepseek-v4.1-flash"
}
```

### 2. 构建二进制

要求 Go 1.22 或更高版本：

```powershell
$env:CGO_ENABLED = '0'
$env:GOTOOLCHAIN = 'local'

go build -trimpath -buildvcs=false `
  -ldflags "-s -w -X main.version=1.0.0" `
  -o .\dist\ClaudeRelay.exe `
  .\cmd\server
```

构建结果：

```text
dist\
├─ ClaudeRelay.exe
└─ config.json
```

`config.json` 必须与 EXE 位于同一目录，或者通过 `ClaudeRelay.exe -config <path>` 指定。

### 3. 连接客户端

在 Claude Desktop 或兼容 Anthropic API 的第三方客户端中：

```text
Base URL: http://127.0.0.1:17863
Model ID: claude-fable-5
```

具体设置入口取决于 Claude Desktop 版本或你使用的客户端封装。ClaudeRelay 不修改 Claude Desktop 程序文件。

### 4. 由系统工具托管（可选）

ClaudeRelay 和参考项目一样，**二进制本身不自带守护，也不附带 NSSM 安装/卸载脚本**。开机自启和异常重启由系统工具负责：

- NSSM：推荐，进程退出后自动拉起
- Windows 任务计划程序：不额外安装软件，但对退出码 0 的退出不会自动重启

具体命令见下方 [Windows 常驻运行](#windows-常驻运行开机自启--崩溃重启)。

## 配置说明

```json
{
  "listenHost": "127.0.0.1",
  "listenPort": 17863,
  "upstreamBaseUrl": "http://127.0.0.1:7863",
  "maxRequestBytes": 209715200,
  "responseHeaderTimeoutSeconds": 60,
  "defaultUpstreamModel": "cn:deepseek-v4.1-flash",
  "modelAliases": {
    "claude-fable-5": "cn:deepseek-v4.1-flash"
  },
  "models": [
    {
      "id": "claude-fable-5",
      "displayName": "Fable 5.2",
      "createdAt": "2025-01-01T00:00:00Z"
    }
  ]
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `listenHost` | string | 监听地址，默认 `127.0.0.1` |
| `listenPort` | number | 监听端口，示例为 `17863` |
| `upstreamBaseUrl` | string | 上游网关根地址，只支持 `http` / `https` |
| `maxRequestBytes` | number | 最大请求体字节数，默认 200 MiB |
| `responseHeaderTimeoutSeconds` | number | 等待上游返回响应头的秒数，默认 60；`0` 表示不限 |
| `defaultUpstreamModel` | string | 未知客户端模型使用的回退上游模型 |
| `modelAliases` | object | 客户端模型 ID 到上游模型 ID 的映射 |
| `models` | array | `/v1/models` 返回给客户端的模型目录 |
| `models[].id` | string | 客户端使用的模型 ID |
| `models[].displayName` | string | 客户端界面显示名 |
| `models[].createdAt` | string | RFC 3339 时间字符串 |

### 配置读取优先级

1. 命令行参数 `-config <path>`
2. 环境变量 `CLAUDE_RELAY_CONFIG`
3. `CLAUDE_RELAY_HOME\config.json`
4. `ClaudeRelay.exe` 所在目录的 `config.json`

可选环境变量：

| 变量 | 作用 |
| --- | --- |
| `CLAUDE_RELAY_CONFIG` | 指定配置文件路径 |
| `CLAUDE_RELAY_HOME` | 指定配置文件的根目录 |
| `CLAUDE_RELAY_VERSION` | 覆盖 `/health` 中显示的版本号 |

## 模型映射

只改写匹配路径中的 JSON `model` 字段：

- `/v1/messages`
- `/v1/messages/count_tokens`

示例：

```json
{
  "model": "claude-fable-5",
  "max_tokens": 1024,
  "messages": [
    { "role": "user", "content": "Hello" }
  ]
}
```

转发前变为：

```json
{
  "model": "cn:deepseek-v4.1-flash",
  "max_tokens": 1024,
  "messages": [
    { "role": "user", "content": "Hello" }
  ]
}
```

如果 `modelAliases` 中找不到客户端模型，则使用 `defaultUpstreamModel`；如果它也为空，则保持原模型名。

## API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/health` | 健康检查、版本、PID、上游与映射信息 |
| `GET` | `/v1/models` | 返回配置中的模型目录和显示名 |
| `POST` | `/v1/messages` | 改写模型后转发，支持流式响应 |
| `POST` | `/v1/messages/count_tokens` | 改写模型后转发 token 计数请求 |
| `OPTIONS` | `*` | 返回本机开发所需的 CORS 预检响应 |
| 其他 | 其他路径 | 透明转发到 `upstreamBaseUrl` |

健康检查示例：

```powershell
Invoke-RestMethod http://127.0.0.1:17863/health
```

模型目录示例：

```powershell
Invoke-RestMethod http://127.0.0.1:17863/v1/models
```

## 内存与转发行为

- 只有 `POST /v1/messages` 和 `POST /v1/messages/count_tokens` 且 `Content-Type` 为 JSON 时，请求体才会被读入内存并改写模型名。
- 模型名改写通过定位顶层 `model` 字段的字节区间，直接拼接替换值实现，不会把整个请求解析成结构化对象。请求体中的其他字节（消息、图片、工具定义、字段顺序和数字格式）原样保留。
- 其他所有请求（如文件上传、其他 API 路径）的请求体直接流式转发到上游，不整体缓冲，`maxRequestBytes` 对它们不生效。
- 因此，`maxRequestBytes` 只限制需要改写模型的请求体大小；超限返回 413。
- `responseHeaderTimeoutSeconds` 只限制“等上游返回响应头”的时间，默认 60 秒。一旦上游开始响应，流式传输可以持续任意久，不会被这个值截断。上游卡死时，请求会在超时后返回 502，而不是无限等待。设为 `0` 关闭该限制（不建议）。

## 构建与测试

```powershell
go fmt ./...
go test ./...
go vet ./...
```

本机构建 Windows x64 单文件二进制：

```powershell
$env:CGO_ENABLED = '0'
$env:GOTOOLCHAIN = 'local'

go build -trimpath -buildvcs=false `
  -ldflags "-s -w -X main.version=1.0.0" `
  -o .\dist\ClaudeRelay.exe `
  .\cmd\server
```

构建参数：

```text
CGO_ENABLED=0
GOTOOLCHAIN=local
-trimpath
-buildvcs=false
-ldflags "-s -w -X main.version=<version>"
```

产物是静态 Go 二进制，运行时不需要 Go、Node.js、Web 服务或额外前端资源。

### GitHub Actions 自动构建

工作流位于：

```text
.github/workflows/binaries.yml
```

- Push 到 `main` 或提交 PR：执行测试并上传 Windows x64 构建产物
- 手动运行 workflow：构建并上传 artifact
- 推送 `v*` 标签：构建 ZIP 并自动创建 GitHub Release

发布包内容包括：

```text
ClaudeRelay.exe
config.example.json
README.md
LICENSE
```

## Windows 常驻运行（开机自启 + 崩溃重启）

二进制本身不自带守护：进程退出后不会自己回来，也没有内置 Windows 服务模式。需要常驻时，由系统工具管理。

先确认：

- `ClaudeRelay.exe` 的绝对路径
- `config.json` 与 EXE 位于同一目录，或通过 `-config` 指定
- 手动运行一次可以正常启动，再交给常驻项

### 方案 A：NSSM（推荐，真正的服务）

NSSM 按“进程退出”拉起，不看退出码。以下命令在管理员 PowerShell 中执行，请按实际路径修改：

```powershell
$exe = 'C:\ClaudeRelay\dist\ClaudeRelay.exe'
$appDir = Split-Path -Parent $exe

nssm install ClaudeRelay $exe
nssm set ClaudeRelay AppDirectory $appDir
nssm set ClaudeRelay Start SERVICE_AUTO_START
nssm set ClaudeRelay AppExit Default Restart
nssm start ClaudeRelay
```

检查：

```powershell
nssm status ClaudeRelay
Invoke-RestMethod http://127.0.0.1:17863/health
Invoke-RestMethod http://127.0.0.1:17863/v1/models
```

停止、重启和卸载：

```powershell
nssm stop ClaudeRelay
nssm restart ClaudeRelay
nssm remove ClaudeRelay confirm
```

`nssm remove` 只删除服务，不删除 EXE 和配置文件。

### 方案 B：Windows 任务计划程序

不安装 NSSM 时，可以用系统自带的任务计划程序。管理员 PowerShell 执行：

```powershell
$exe = 'C:\ClaudeRelay\dist\ClaudeRelay.exe'
$act = New-ScheduledTaskAction -Execute $exe -WorkingDirectory (Split-Path $exe)
$trg = New-ScheduledTaskTrigger -AtStartup
$set = New-ScheduledTaskSettingsSet -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) `
       -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit ([TimeSpan]::Zero)
Register-ScheduledTask -TaskName 'ClaudeRelay' -Action $act -Trigger $trg -Settings $set -RunLevel Highest
Start-ScheduledTask -TaskName 'ClaudeRelay'
```

注意：任务计划程序的“失败后重启”主要针对非零退出码；进程正常退出或被人手动结束后，不保证重新启动。需要“任何退出都拉起”时使用 NSSM。

### 更新程序

1. 停止 `ClaudeRelay` 服务或计划任务
2. 替换 `ClaudeRelay.exe`
3. 保留原 `config.json`
4. 重新启动服务或计划任务
5. 再次请求 `/health` 和 `/v1/models`

## 目录结构

```text
ClaudeRelay/
├─ .github/
│  └─ workflows/
│     └─ binaries.yml
├─ .gitattributes
├─ .gitignore
├─ LICENSE
├─ README.md
├─ go.mod
├─ config.example.json
├─ cmd/
│  └─ server/
│     └─ main.go
└─ internal/
   ├─ config/
   │  └─ config.go
   └─ relay/
      ├─ server.go
      └─ server_test.go
```

- `.github/workflows/binaries.yml`：测试、构建 Windows 二进制和 Release ZIP
- `.gitattributes`：统一源码行尾和二进制文件属性
- `.gitignore`：排除本地配置、EXE、ZIP 和构建目录
- `LICENSE`：MIT License
- `cmd/server`：程序入口、信号处理和服务启动
- `internal/config`：配置加载与校验
- `internal/relay/server.go`：HTTP 转发、模型改写（字节级替换）、流式透传与流式响应
- `internal/relay/server_test.go`：模型映射、字节级改写、端到端转发与流式透传测试
- `config.example.json`：可公开提交的示例配置
- `config.json`：本地实际配置，不提交到仓库

## 排错

### 端口 17863 被占用

```powershell
Get-NetTCPConnection -LocalPort 17863 -State Listen
```

根据 `OwningProcess` 查找占用程序后手动停止。ClaudeRelay 不会自动终止其他进程。

### 健康检查失败

程序不写内置日志。依次检查：

```powershell
nssm status ClaudeRelay
Get-NetTCPConnection -LocalPort 17863 -State Listen
Invoke-RestMethod http://127.0.0.1:17863/health
```

### Claude 中仍显示旧模型名

1. 检查 `Invoke-RestMethod http://127.0.0.1:17863/v1/models`
2. 确认 `displayName` 是 `Fable 5.2`
3. 重启 Claude Desktop，让它重新拉取模型目录
4. 确认客户端使用的是 ClaudeRelay 的 Base URL，而不是旧网关

### 上游返回 502 或连接失败

确认：

- `upstreamBaseUrl` 可以访问
- 上游网关已启动
- 端口、协议和路径前缀正确
- 上游需要的认证头由客户端正常传入

### 配置解析失败

确认 `config.json` 是合法 JSON，且 `listenPort` 位于 `1-65535`。

## 安全说明

- 默认只监听回环地址，避免局域网或公网直接访问。
- 如果改为 `0.0.0.0` 或其他网卡地址，请自行增加防火墙、反向代理和访问控制。
- ClaudeRelay 不内置 API Key。客户端传入的认证头会原样转发到上游。
- 程序不写请求日志或日志文件；请求体和认证头只在内存中转发。
- 不要把包含密钥、Token 或私有上游地址的 `config.json` 提交到 Git。