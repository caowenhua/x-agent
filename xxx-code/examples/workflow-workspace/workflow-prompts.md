# Workflow Workspace Prompts

下面这些 prompt 适合直接粘到 `xxx-code` 里，用来体验显式 workflow。

## 1. 先理解这个案例

```text
读取当前 workspace 的 brief.md 和 inputs/*.md，告诉我这个 workflow 例子想演示什么。
```

## 2. 显式启动一个 workflow

```text
显式使用 agent_fanout 做一个 release review workflow。

要求：
- wait=true
- max_parallel=2
- 任务名分别是 roadmap、incidents、metrics、draft
- roadmap 读取 inputs/roadmap.md 并给一句结论
- incidents 读取 inputs/incidents.md 并给一句结论
- metrics 读取 inputs/metrics.md 并给一句结论
- draft 依赖 roadmap、incidents、metrics
- draft 的 prompt 必须用 {{tasks.roadmap.result}}、{{tasks.incidents.result}}、{{tasks.metrics.result}} 来拼最终 digest
```

## 3. 查询 workflow 状态

```text
把刚才那个 workflow 的 task 状态查出来，告诉我哪些任务完成了，resolved_prompt 是什么。
```

## 4. 写出最终产物

```text
把 workflow 的最终 digest 写到 outputs/release-digest.md，并给我一个简短总结。
```

## 5. 演示 selective resume

```text
如果某个 workflow task 失败了，演示如何只恢复 failed 的任务，而不是整条 workflow 全部重跑。
```
