# xxx-code Workflow Workspace

这个目录是一个专门演示 `multi-agent + workflow` 的示例工作区。

和 `examples/demo-workspace/` 不同，这个例子不强调 plugin 或 MCP，而是聚焦：

- `agent_fanout`
- `depends_on`
- `{{tasks.<name>.result}}` prompt 引用
- `workflow_tasks / workflow_get / workflow_resume`
- 把 workflow 结果写成实际产物文件

## 目录结构

```text
workflow-workspace/
  .env.example
  config.yaml
  brief.md
  workflow-prompts.md
  inputs/
    roadmap.md
    incidents.md
    metrics.md
  outputs/
    .gitkeep
```

## 适合用它理解什么

这个 workspace 适合用来理解两件关键设计：

1. `agent_fanout` 不只是“并发跑几个 agent”，而是可以形成一个可查询、可恢复的 workflow。
2. workflow 中的下游任务可以显式依赖上游任务，并通过 `{{tasks.<name>.result}}` 引用上游结果。

## 启动方式

前提：

- 本机已安装 Go
- 已设置任意一个可用 provider 的 API key

在 `xxx-code/` 仓库根目录执行：

```bash
export ANTHROPIC_API_KEY=your-key
go run ./cmd/xxx-code --config ./examples/workflow-workspace/config.yaml
```

如果你想先看环境变量模板，可以打开：

- `examples/workflow-workspace/.env.example`

## 快速 smoke

如果你想先验证这个 workflow workspace 的配置和用户故事回归，而不是立刻连真实模型，可以直接运行：

```bash
bash ./scripts/workflow-workspace-smoke.sh
```

它会把 smoke 日志与摘要写到：

```text
.artifacts/workflow-workspace-smoke/
```

## 这个例子如何工作

推荐顺序：

1. 先读 `brief.md`
2. 再看 `inputs/*.md`
3. 然后看 `workflow-prompts.md`
4. 最后在交互里实际让它跑一次 workflow

这个例子里最值得注意的是 `draft` 任务。它不是自己重新读文件，而是依赖前三个任务，并把前三个任务的结果注入到自己的 prompt 里。

这类模式很适合：

- 研究型任务拆分
- 多模块代码审查
- 发布说明汇总
- 周报 / 日报 / digest 生成

## 调试建议

如果 workflow 行为和预期不一致，可以按这个顺序查：

1. `:workflows`
2. `:workflow <id>`
3. `:workflow-tasks <id>`
4. `:workflow-resume <id> failed`

如果最终文件没有写出来，再确认：

1. `allow_write` 是否包含当前 workspace
2. `write_file` 是否还在 `allow_tools` 中
3. `draft` 任务是否真的拿到了上游任务结果
