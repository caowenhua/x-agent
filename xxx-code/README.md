# xxx-code

`xxx-code` 是一个用 Go 实现的终端 coding agent，设计目标不是逐行复刻 TypeScript 版 Claude Code 的全部产品面，而是先提供一个可运行、可扩展、方便继续演化 multi-agent 的内核。

当前版本已经包含：

- Anthropic Messages API 适配
- 多轮 agent loop
- 本地工具调用
- REPL 与单次执行模式
- in-process multi-agent 基础设施
- 子 agent 的 `spawn / wait / list`

## 目录结构

```text
xxx-code/
  cmd/xxx-code/              CLI 入口
  internal/config/           配置与参数
  internal/engine/           核心运行时、消息模型、主循环、agent 管理
  internal/provider/         模型提供方适配
  internal/tools/            内建工具
```

## 已实现的工具

- `bash`
- `read_file`
- `write_file`
- `edit_file`
- `glob`
- `grep`
- `agent_spawn`
- `agent_wait`
- `agent_list`

## 运行前准备

设置 Anthropic API Key：

```bash
export ANTHROPIC_API_KEY=...
```

## 交互模式

```bash
cd xxx-code
go run ./cmd/xxx-code
```

REPL 内支持：

- `:help`
- `:agents`
- `:wait <agent-id>`
- `:quit`

## 单次执行

```bash
go run ./cmd/xxx-code --print "分析当前目录的 Go 项目结构并给出修改建议"
```

## 常用参数

```bash
go run ./cmd/xxx-code \
  --model claude-sonnet-4-5 \
  --max-turns 12 \
  --tool-timeout 2m \
  --cwd /path/to/project \
  --print "实现一个功能"
```

## 设计重点

### 1. 统一执行内核

主线程和子 agent 复用同一个 `Runner` 循环：

- 发送 messages 给模型
- 解析 text / tool_use
- 执行工具
- 回写 tool_result
- 继续下一轮直到没有工具调用

### 2. Multi-agent 先做基础设施

`agent_spawn` 不是假的 prompt 分支，而是真的起一个独立 session：

- 独立消息历史
- 可选继承父会话历史
- 可同步等待，也可后台运行
- 可通过 `agent_wait` / `agent_list` 管理

这让后续继续扩展成更强的 Go 版多代理框架变得直接。

### 3. 依赖尽量轻

当前实现只使用 Go 标准库，方便你后续继续扩展、嵌入、裁剪。

## 测试

```bash
go test ./...
```

## 现在还没做的

这一版刻意先没有覆盖 TypeScript 版里特别重的产品层：

- 流式 UI / 增量 token 输出
- MCP 客户端
- hook 系统
- transcript 持久化与 resume
- context compaction / token budgeting
- 更细粒度的权限系统
- remote agent / bridge / daemon

但核心的 Go 版 agent runtime 和 multi-agent 支架已经在了，后面你要往这些方向继续加，会比从零写轻很多。
