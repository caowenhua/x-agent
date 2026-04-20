# xxx-code MCP Integration Guide

## MCP 在 xxx-code 里的角色

在 `xxx-code` 里，MCP 不是一个旁路能力，而是会被桥接进统一 tool registry：

- 远端 MCP server 暴露的 tool 会注册成 runtime tool
- MCP resource / resource template / prompt 会通过 support tools 暴露
- agent、daemon、remote client 看到的是同一套 MCP surface

这意味着你接入一个 MCP server 后，模型并不是“切换到另一套系统”，而是继续在同一个 runtime 里工作。

## 配置入口

默认情况下，`xxx-code` 会自动发现：

```text
.mcp.json
```

也可以通过：

- `--mcp-config`
- `XXX_CODE_MCP_CONFIG`
- `mcp_config`

显式指定。

## 配置结构

MCP 配置顶层结构是：

```json
{
  "mcpServers": {
    "server-name": {
      "...": "..."
    }
  }
}
```

每个 server 对应源码里的 `mcp.ServerConfig`，主要字段有：

| 字段 | 作用 |
| --- | --- |
| `transport` | transport 类型 |
| `type` | `transport` 的兼容别名 |
| `url` | `http / sse / ws` 端点 |
| `headers` | HTTP / SSE / WS 请求头 |
| `command` | `stdio` 模式下的命令 |
| `args` | `stdio` 模式参数 |
| `env` | `stdio` 模式额外环境变量 |
| `cwd` | `stdio` 模式工作目录 |

## 支持的 transport

`xxx-code` 当前支持四种 transport：

- `stdio`
- `http`
- `sse`
- `ws`

另外还有几个兼容写法会被自动归一化：

- `streamable-http`
- `streamable_http`
- `streamablehttp`

都会映射成：

```text
http
```

如果没有显式填 transport，默认是：

```text
stdio
```

## 配置示例

仓库里提供了几份最小示例：

- `examples/mcp/stdio.json`
- `examples/mcp/http.json`
- `examples/mcp/sse.json`
- `examples/mcp/ws.json`

### stdio

```json
{
  "mcpServers": {
    "docs": {
      "transport": "stdio",
      "command": "node",
      "args": ["./mcp-docs-server.js"],
      "cwd": ".",
      "env": {
        "NODE_ENV": "production"
      }
    }
  }
}
```

### HTTP

```json
{
  "mcpServers": {
    "docs": {
      "transport": "http",
      "url": "http://127.0.0.1:8080/mcp",
      "headers": {
        "Authorization": "Bearer replace-me"
      }
    }
  }
}
```

## transport 选择建议

### stdio

适合：

- 本机一起跑的小型 helper server
- 开发期快速试接
- 你已经有一个 CLI 形式的 MCP server

优点：

- 本地集成简单
- 不需要单独守护进程

代价：

- 每个 runtime 生命周期都要管理子进程
- 部署和排障更偏本机

### http

适合：

- 部署型 MCP server
- 共享给多个 `xxx-code` 实例
- 需要稳定网络边界与认证头

### sse

适合：

- 你已有 SSE 风格的 MCP server

### ws

适合：

- MCP server 本身就是 websocket 接口

## tool、resource、prompt 如何进入 runtime

### MCP tool

每个远端 MCP tool 会被桥接成本地 runtime tool，命名规则是：

```text
mcp__<server>__<tool>
```

例如：

```text
mcp__docs__search
```

命名时会做几件事：

- 归一化非法字符
- 限制长度
- 过长时对尾部做 hash 缩写

这样能避免不同 server 的长 tool name 把 registry 撑爆，也降低冲突概率。

### MCP resource / prompt

resource 和 prompt 不会被注册成动态工具名，而是通过 support tools 暴露：

- `list_mcp_resources`
- `list_mcp_resource_templates`
- `read_mcp_resource`
- `list_mcp_prompts`
- `get_mcp_prompt`
- `mcp_health`
- `mcp_reload`
- `mcp_validate`

这套设计的好处是：

- 对数据面和控制面做了清晰区分
- 模型既能直接调用远端 MCP tool，也能先读资源、读 prompt 再决定下一步

## workspace root 传递

`xxx-code` 在连接 MCP server 时，会把当前 `WorkingDir` 注册成一个 `workspace` root。

这意味着：

- 对支持 roots 的 MCP server，可以拿到 agent 当前工作区信息
- 本地 REPL、daemon session、remote session 都能把自己的工作目录上下文传给 MCP 侧

如果你的 MCP server 会读项目文件、索引代码库或做与仓库相关的操作，这一点非常关键。

## 校验、重载与健康检查

### 本地命令

- `:mcp`
- `:mcp-health [server]`
- `:mcp-reload`
- `:mcp-validate [path]`
- `:mcp-resources [server]`
- `:mcp-resource-templates [server]`
- `:mcp-prompts [server]`
- `:mcp-read <server> <uri>`
- `:mcp-prompt <server> <name> [k=v ...]`

### support tools

这些命令背后对应的是同名 support tools，模型也可以直接调用。

### daemon API

daemon 下同样提供了 MCP 控制面接口，例如：

- `GET /v1/sessions/{id}/mcp`
- `POST /v1/sessions/{id}/mcp/reload`
- `POST /v1/sessions/{id}/mcp/validate`
- `GET /v1/sessions/{id}/mcp/resources`
- `GET /v1/sessions/{id}/mcp/prompts`
- `POST /v1/sessions/{id}/mcp/read-resource`
- `POST /v1/sessions/{id}/mcp/get-prompt`

这让远程 daemon 也能做 MCP 生命周期管理。

## 常见接入方式

### 方式 1：本地 stdio server

适合你自己维护一个辅助程序，然后让 `xxx-code` 跟它一起启动。

### 方式 2：团队共享 HTTP MCP

适合多个开发者或多个 daemon 实例共用同一份外部能力。

### 方式 3：把 prompt catalog 暴露成 MCP prompt

如果你有稳定的领域 prompt 模板，MCP prompt 比把整段模板硬塞到系统 prompt 更可治理。

### 方式 4：把数据面暴露成 resource，而不是 tool

如果内容本质是“可读数据”，优先用 resource；只有需要动作执行时再用 tool。这样模型在推理时会更清楚“这是读取”还是“这是执行”。

## 排障建议

### 1. 先看 `mcp_validate`

它能先把：

- transport 填错
- URL 非绝对地址
- stdio command 为空
- header / env 名为空

这类静态问题提前拦住。

### 2. 再看 `mcp_reload`

如果配置文件刚改完，记得 reload；daemon 和本地 runtime 都不会自动监控 `.mcp.json` 文件变化。

### 3. 最后看 `mcp_health`

health 适合确认：

- server 能不能连通
- 认证头是否正确
- 当前连接是不是还活着

## 常见问题

### 为什么 tool 已经接进来了，但 resource / prompt 看不到？

说明 server 的 tools 面可用，但不一定实现了 resource 或 prompt catalog；这不是 `xxx-code` 的问题。

### 为什么我写的是 `streamable-http`，状态里显示成 `http`？

因为 runtime 内部会把这几种别名统一折叠成 `http`，便于后续 transport 分支处理。

### 什么时候该用 plugin，什么时候该用 MCP？

一个简单判断：

- 本地命令脚本：plugin
- 独立服务、远程能力、资源目录、prompt catalog：MCP
