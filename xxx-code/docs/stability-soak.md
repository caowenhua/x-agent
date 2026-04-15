# xxx-code Stability Soak

`xxx-code-stability` 是一个独立的长稳/soak 测试程序，用来持续验证 `xxx-code` 运行时在长时间、多轮、多会话和重启条件下是否还能稳定工作。

它不是普通的 `go test` 替代品，而是补足单元测试、集成测试和 user story 测试之外的一层“长时间运行验证”。

## 为什么单独做成程序

`go test` 很适合做快速、可重复、边界明确的自动回归，但它不太适合承担下面这些任务：

- 连续运行几十分钟到几小时
- 周期性重启 daemon 验证恢复能力
- 在一个进程里反复创建 session、workflow、agent 和动态工具
- 失败时保留完整工作目录，便于人工排障
- 产出一份独立的 summary JSON 给 CI 或人工分析

所以这里专门做了一个独立入口：

```bash
go run ./cmd/xxx-code-stability --iterations 1
```

## 它具体验证什么

这套程序会在本地临时目录里搭建一套完整但可控的运行环境：

- 一个进程内的 `xxx-code` daemon
- 一个 deterministic provider，不走真实模型网络请求
- 一个 HTTP MCP server
- 一个命令型插件
- 一个 remote client，通过 daemon HTTP API 驱动所有场景

这样做的重点不是“验证某个真实模型是否能回复”，而是验证 `xxx-code` 自己的 runtime 主链路是否稳定。

当前内建场景包括：

- `basic_turn`
  - 普通 turn 是否能完成并返回正确结果
- `stream_turn`
  - 流式输出事件是否和最终结果一致
- `plugin_lifecycle`
  - 插件 validate / install / invoke / remove 是否完整可用
- `mcp_lifecycle`
  - MCP reload / resources / templates / prompts / tool bridge 是否正常
- `agent_lifecycle`
  - 子 agent spawn / wait / send / cancel 是否稳定
- `workflow_lifecycle`
  - workflow fanout、任务查询和 selective resume 是否可恢复
- `session_save`
  - session 持久化是否生成可检查的落盘文件
- `stream_timeout`
  - turn timeout 是否能正确返回结构化重试错误

## 重启验证

除了上述场景外，程序还会按 `--restart-every` 指定的轮次周期重启 daemon，并验证：

- 既有 session 是否还能重新读取
- transcript 是否没有丢
- 重启后是否还能继续跑新 turn
- save 后的 session 文件是否仍可恢复

这部分很适合发现：

- 生命周期关闭不彻底
- 恢复路径状态不完整
- 持久化与内存状态不一致
- daemon restart 后远程 API 行为漂移

## 常用命令

快速 smoke：

```bash
go run ./cmd/xxx-code-stability --iterations 1
```

更像发布前 soak：

```bash
go run ./cmd/xxx-code-stability \
  --duration 30m \
  --concurrency 4 \
  --restart-every 20 \
  --progress-every 15s
```

失败时保留现场：

```bash
go run ./cmd/xxx-code-stability \
  --iterations 10 \
  --keep-workdir \
  --workdir /tmp/xxx-code-soak
```

输出机器可读摘要：

```bash
go run ./cmd/xxx-code-stability \
  --duration 10m \
  --summary-json ./artifacts/stability-summary.json
```

## 重要特性

### 不依赖外部模型 key

因为内部使用的是 deterministic provider，这个程序不需要 `ANTHROPIC_API_KEY`、`OPENAI_API_KEY` 之类的配置。

这意味着：

- 本地开发可以直接跑
- CI 可以直接跑
- 结果更可重复
- 出问题时更容易定位到 runtime，而不是外部 provider 波动

### 可以保留完整工件

如果开启 `--keep-workdir`，工作目录里通常会包含：

- daemon 状态目录
- session 持久化文件
- `.mcp.json`
- 动态插件源目录
- 运行时生成的 `.xxx-code` 目录

这对排查“长时间后才出现的问题”非常重要。

### 提供 JSON 总结

`--summary-json` 会输出本次运行的概览，包括：

- 起止时间
- 完成轮次
- restart 次数
- 总操作数和失败数
- 每个场景的执行次数、失败次数、平均耗时和最大耗时

这样可以很方便地接到：

- CI artifact
- 简单 dashboard
- 发布前回归记录

## 什么时候优先跑它

下面这些情况，建议除了 `go test` 以外，也额外跑一轮 `xxx-code-stability`：

- 改了 daemon 生命周期
- 改了 session 持久化 / resume
- 改了 remote client / SSE / timeout
- 改了 workflow / agent orchestration
- 改了 MCP 或 plugin 的动态装载
- 发布前想看一次更像真实运行时的长稳验证

## 建议的使用方式

一个比较实用的组合是：

1. 开发时跑 `go test ./...`
2. 合并前跑 `go test -race ./...`
3. 发布前跑 `go run ./cmd/xxx-code-stability --duration 30m`

这样三层分别覆盖：

- 快速回归
- 并发数据竞争
- 长时间稳定性与恢复能力
