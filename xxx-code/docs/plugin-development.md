# xxx-code Plugin Development Guide

## 适用场景

`xxx-code` 的 plugin 机制适合这类扩展：

- 你已经有一个命令行脚本或小工具，希望最小成本接入 agent runtime
- 你希望扩展逻辑跟主进程隔离，避免把一次性工具直接编译进 `xxx-code`
- 你希望扩展可以被安装、移除、重载，而不是每次都重新构建主程序

如果你的扩展天然就是一个远程能力系统，或者已经有 MCP server，优先考虑 MCP；如果你的扩展只是本地命令或脚本，plugin 往往是更轻的入口。

## 运行模型

`xxx-code` 的 plugin 本质上是“命令行工具桥接成 runtime tool”：

1. runtime 在 plugin 目录里发现 manifest
2. 每个 manifest 里的 `tools[]` 会被桥接成一个 `Tool`
3. 模型调用这个 tool 时，`xxx-code` 启动对应命令
4. tool input 以 JSON 形式写到子进程 `stdin`
5. 子进程 `stdout` 被当作 tool result 返回给模型

这意味着 plugin 的最小实现门槛其实很低：能读 `stdin`、能往 `stdout` 写结果即可。

## 默认目录与发现规则

如果没有显式配置 `plugin_dir`，runtime 默认会读取：

```text
.xxx-code/plugins
```

扫描规则是递归的，以下两类文件都会被当作 manifest：

- `plugin.json`
- `*.plugin.json`

也就是说，下面两种布局都可以：

```text
.xxx-code/plugins/echoer/plugin.json
```

```text
.xxx-code/plugins/echoer/echoer.plugin.json
```

## Manifest 结构

plugin manifest 对应源码里的：

- `internal/plugins.Manifest`
- `internal/plugins.CommandToolSpec`

一个最小例子：

```json
{
  "name": "echoer",
  "version": "0.1.0",
  "tools": [
    {
      "name": "echo",
      "description": "Echo the provided payload back to xxx-code.",
      "input_schema": {
        "type": "object",
        "properties": {
          "value": {
            "type": "string",
            "description": "Value to echo back."
          }
        },
        "required": ["value"]
      },
      "command": "./tool.sh",
      "timeout": "5s"
    }
  ]
}
```

关键字段说明：

| 字段 | 作用 |
| --- | --- |
| `name` | plugin 名称；不能为空 |
| `version` | 可选，仅用于状态展示 |
| `tools` | 该 plugin 暴露的命令工具列表；至少一个 |
| `tools[].name` | tool 名称；不能为空 |
| `tools[].description` | tool 描述；建议填写，模型会直接看到 |
| `tools[].input_schema` | JSON Schema 风格的输入定义 |
| `tools[].command` | 要执行的命令 |
| `tools[].args` | 命令参数 |
| `tools[].env` | 额外环境变量 |
| `tools[].cwd` | 命令工作目录；相对路径按 manifest 所在目录解析 |
| `tools[].timeout` | 单个 tool 超时，例如 `5s`、`1m` |

## 名称规范

plugin 真正注册到 runtime 里的 tool name 不是 manifest 里的原始 `name`，而是：

```text
plugin__<normalized-plugin-name>__<normalized-tool-name>
```

例如：

- plugin `echoer`
- tool `echo`

最终会被注册成：

```text
plugin__echoer__echo
```

这里的名称会做归一化：

- 转小写
- 非字母数字字符会变成 `_`
- 空名称会回退成安全占位名

这样做的目标是让 runtime 里所有 tool naming 风格一致，也便于模型在 prompt 里稳定引用。

## 命令执行约定

### 输入

plugin tool 调用时，`xxx-code` 会把 JSON 参数写到命令的 `stdin`。

如果 tool 没有参数，传入的是：

```json
{}
```

所以最简单的 shell tool 可以直接：

```sh
cat
```

### 输出

plugin 命令有两种输出模式。

普通文本模式：

- `stdout` 的全部内容会直接作为 tool result

结构化模式：

```json
{
  "content": "...",
  "is_error": false
}
```

或者：

```json
{
  "content": {
    "summary": "done",
    "files": ["a.go", "b.go"]
  },
  "is_error": false
}
```

当 `stdout` 能解析成上面的结构时，`xxx-code` 会把：

- `content` 作为结果内容
- `is_error` 作为错误标记

这很适合需要显式区分“命令成功执行了，但业务结果是失败”的场景。

### 失败语义

如果命令进程非零退出：

- 优先返回 `stderr`
- 其次返回 `stdout`
- 最后回退到进程错误文本

同时 tool result 会自动标记为错误。

## 运行时环境变量

plugin 执行时，runtime 会额外注入几个环境变量：

| 变量 | 含义 |
| --- | --- |
| `XXX_CODE_PLUGIN_NAME` | plugin 名称 |
| `XXX_CODE_PLUGIN_TOOL` | 完整 tool name |
| `XXX_CODE_WORKING_DIR` | 当前执行上下文工作目录 |

此外：

- 会继承宿主进程环境变量
- `tools[].env` 会覆盖同名变量

## 相对路径解析规则

这点非常重要，因为它直接影响 plugin 是否可移植。

### `command`

如果 `command` 包含路径分隔符，例如：

- `./tool.sh`
- `bin/runner`

它会按 manifest 所在目录解析。

如果只是：

```json
"command": "node"
```

则交给系统 `PATH` 解析。

### `cwd`

如果设置了 `cwd`：

- 绝对路径按绝对路径使用
- 相对路径按 manifest 所在目录解析

如果未设置 `cwd`：

- 优先使用当前 execution context 的 `WorkingDir`

这让 plugin 既能“跟着 agent 当前工作目录走”，也能“固定在自己目录下执行”。

## 最小可运行示例

仓库已经提供了一个最小 plugin 示例：

- `examples/plugins/echoer/plugin.json`
- `examples/plugins/echoer/tool.sh`

它的 `tool.sh` 会把输入 JSON 包装成结构化结果再返回：

```sh
#!/bin/sh
set -eu

payload="$(cat)"
if [ -z "$payload" ]; then
  payload='{}'
fi

printf '{"content": %s}\n' "$payload"
```

你可以直接把这个目录复制到自己的 `.xxx-code/plugins/` 下面，再根据需要改 `input_schema`、命令逻辑和 timeout。

## 安装、校验与重载

### 本地 runtime

plugin support tools 是内建注册的：

- `list_plugins`
- `validate_plugin`
- `install_plugin`
- `remove_plugin`
- `reload_plugins`

你可以在 agent 里直接让模型调用，也可以在 REPL / TUI 里用命令：

- `:plugins`
- `:plugins-validate <path>`
- `:plugins-install <path>`
- `:plugins-remove <name>`
- `:plugins-reload`

### daemon / remote

daemon 还提供了对等 API：

- `GET /v1/sessions/{id}/plugins`
- `POST /v1/sessions/{id}/plugins/validate`
- `POST /v1/sessions/{id}/plugins/install`
- `POST /v1/sessions/{id}/plugins/remove`
- `POST /v1/sessions/{id}/plugins/reload`

这意味着你可以把 plugin lifecycle 放到远程 daemon 上管理，而不是必须登录宿主机手工操作。

## 开发建议

### 1. description 要像给模型写，而不是像给人写

模型会直接读取 tool description，所以描述应说明：

- 这个工具做什么
- 需要什么输入
- 输出大概是什么
- 有什么限制

### 2. input_schema 越清晰，tool call 越稳定

如果 schema 太宽泛，模型更容易造出不规范输入。建议至少明确：

- `type`
- `properties`
- `required`

### 3. 尽量让 stdout 保持“可直接给模型看”

如果输出是一大段 debug 噪音，模型后续推理质量会明显下降。推荐：

- 成功时输出清晰结果
- 调试信息写到 `stderr`

### 4. 不要把长期状态隐式藏在 plugin 自己目录里

如果 plugin 需要读写项目文件，优先使用 `XXX_CODE_WORKING_DIR` 或显式参数，而不是偷偷依赖当前 shell 的随机 cwd。

### 5. 超时要保守设置

如果 plugin 只是一个轻量命令，最好给 `timeout`，避免某个脚本卡死把整个 turn 拖住。

## 常见问题

### 为什么 plugin 校验通过了，但运行失败？

`validate_plugin` 会检查 manifest、路径、timeout 等静态问题，但不会替你执行命令本体，所以：

- 运行时依赖没装
- 脚本权限不对
- 外部服务不可达

这些仍然需要在真实运行里暴露。

### 为什么 tool 名和 manifest 里的不一样？

因为 runtime 会统一转换为：

```text
plugin__<plugin>__<tool>
```

这是设计上的稳定命名约定，不是 bug。

### plugin 适合做复杂编排吗？

不太适合。plugin 更像“单个工具单元”；如果你需要：

- 远程数据面
- 多资源目录
- prompt catalog
- 长连接服务

通常 MCP 更合适。
