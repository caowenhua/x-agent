# Claude Code 的 AGENT 设计精髓

## 一句话提炼

Claude Code 最厉害的地方，不是“支持子代理”，而是它把 Agent 设计成了一个可调度、可恢复、可隔离、可观测的运行时系统。

---

## 1. 它把 Agent 从“提示词技巧”提升成“运行时对象”

在很多 agent 框架里，agent 只是：

- 一段 system prompt
- 一个模型调用
- 若干工具定义

在 Claude Code 里，agent 是一整个运行时对象，至少包含：

- 自己的消息历史
- 自己的 tool pool
- 自己的 permission context
- 自己的 abort controller
- 自己的 transcript sidechain
- 自己的 task lifecycle
- 自己的 progress / notification / resume 能力

对应实现核心：

- `claude-code/src/tools/AgentTool/AgentTool.tsx`
- `claude-code/src/tools/AgentTool/runAgent.ts`
- `claude-code/src/utils/forkedAgent.ts`

这让 Agent 不再是“再问一次模型”，而是“再启动一个有边界的执行单元”。

---

## 2. 它把 Tool 和 Task 分开，这是整个系统成熟的标志

这是我认为最关键的设计点之一。

### Tool 解决的是

- 一次性动作
- 结构化输入输出
- 权限和 hook
- 可流式返回进度

### Task 解决的是

- 长生命周期
- 后台运行
- 可停止
- 可轮询
- 可恢复
- 可通知

对应实现核心：

- `claude-code/src/Tool.ts`
- `claude-code/src/Task.ts`
- `claude-code/src/tasks/LocalAgentTask/LocalAgentTask.tsx`
- `claude-code/src/tasks/RemoteAgentTask/RemoteAgentTask.tsx`

很多系统把“后台 agent”直接塞进工具层，结果最后无法恢复、无法可视化、无法停止。Claude Code 没这么做。

---

## 3. 它最强的不是 spawn agent，而是 context isolation

`createSubagentContext()` 体现了 Claude Code 的 Agent 设计哲学：

- 默认隔离
- 按需共享
- 明确哪些状态可以继承，哪些绝不能污染父线程

默认隔离的东西包括：

- 读文件缓存副本
- denial tracking
- nested memory / skill discovery 集合
- content replacement state 副本

显式共享的东西包括：

- `setAppState`
- `setResponseLength`
- `abortController`

这解决了多 agent 系统最难的矛盾：

- 共享太少，子 agent 没上下文、没缓存命中、没协作能力
- 共享太多，子 agent 会互相踩状态，最后不可控

Claude Code 的答案是：

> 默认隔离，显式共享，且共享项必须是有意识选择。

这比“简单复制父 prompt”高了不止一个层级。

---

## 4. 它把 prompt cache 稳定性当成 Agent 架构的一部分

大多数 agent 系统只在意“能不能跑起来”，Claude Code 在意“下一个 agent 能不能复用上一个 agent 的前缀缓存”。

具体体现：

- system prompt 里专门有 `SYSTEM_PROMPT_DYNAMIC_BOUNDARY`
- 工具数组要排序，避免 mid-session tool pool 抖动破坏缓存
- `toolToAPISchema()` 会缓存 session-stable schema
- fork agent 会保留 cache-safe params
- content replacement state 也会克隆而不是随意重建

对应实现核心：

- `claude-code/src/constants/prompts.ts`
- `claude-code/src/utils/api.ts`
- `claude-code/src/services/api/claude.ts`
- `claude-code/src/utils/forkedAgent.ts`
- `claude-code/src/tools.ts`

这意味着 Claude Code 的 agent 设计不是“逻辑正确就行”，而是“逻辑正确且长期成本可控”。

---

## 5. 它让所有 agent 都跑在同一套 query runtime 上

这点非常高级。

Claude Code 没有给主线程、子代理、fork agent、某类 specialist agent 各写一套循环。

它的做法是：

- 所有人最终都复用 `query.ts`
- 区别只在于传入的 `ToolUseContext`、messages、system prompt、tools、permission mode、MCP clients

这带来的好处非常大：

- fallback / compact / retry / hook / tool repair 这类能力只写一次
- 主 agent 和子 agent 的行为模型一致
- specialist agent 只需改“配置面”，而不必改“执行面”

这是一种非常强的“统一执行内核”设计。

---

## 6. 它把 specialist agent 做成“约束模板”，而不是新框架

`Explore`、`Plan`、`verification` 的实现思路很值得学：

- 不是给每类 agent 造一个新 runtime
- 而是用同一 runtime，叠加不同的 prompt、tool 白名单/黑名单、模型、background/isolation 策略

比如：

- `Explore` 是只读搜索专家
- `Plan` 是只读架构规划专家
- `verification` 是对抗式验证专家

这说明 Claude Code 对 agent specialization 的理解是：

> 专家 agent 的本质，是对统一执行引擎施加不同的能力约束和认知偏置。

这比“复制一个 prompt，换个名字”要高级得多。

---

## 7. 它把失败恢复当成 Agent 主路径

真正成熟的 agent 系统，不是只在 happy path 上漂亮，而是失败后还能继续工作。

Claude Code 在这件事上做得很彻底：

- tool_result 缺失时会补 pairing
- streaming fallback 会 tombstone 旧消息并重建执行器
- prompt too long 会尝试 context collapse / reactive compact
- max output tokens 会自动 recovery
- fallback model 可以切换后重试
- abort 时会补全被中断工具的 synthetic result

对应核心：

- `claude-code/src/query.ts`
- `claude-code/src/services/tools/StreamingToolExecutor.ts`
- `claude-code/src/utils/messages.ts`
- `claude-code/src/services/compact/*`

所以 Claude Code 的 agent 不是“容易成功”，而是“很难彻底死掉”。

---

## 8. 它把可观测性和恢复能力做进了设计，而不是调试补丁

Claude Code 的 agent 几乎处处留下结构化痕迹：

- transcript
- sidechain transcript
- task output file
- SDK init / result / task events
- usage / requestId / cost / tracing
- remote task metadata

这意味着它不仅能跑，还能：

- 被 UI 观察
- 被 SDK 消费
- 被 resume
- 被后台轮询
- 被产品分析

很多 agent 框架最大的短板是“只活在当前进程里”，Claude Code 明显不是。

---

## 9. 如果把它抽象成一个可复用公式

我会把 Claude Code 的 Agent 设计总结成这个公式：

> Agent = 统一 query 内核 + 受控上下文隔离 + 工具/任务分层 + 权限治理 + 压缩/恢复机制 + 可观测 transcript

少任何一个，系统都会退化：

- 没有上下文隔离，多 agent 会互相污染
- 没有任务层，后台 agent 只是悬空 Promise
- 没有权限治理，agent 很难真的上线
- 没有压缩恢复，长会话迟早崩
- 没有 transcript，可恢复性和可调试性都很差

---

## 10. 对 Agent 系统设计最有启发的三个点

### 第一，统一执行内核比“多做几个 agent”更重要

真正可维护的是一个内核，多个 profile，而不是多个平行 agent 框架。

### 第二，默认隔离、显式共享，是多 agent 上生产环境的关键

这是 `createSubagentContext()` 最值得学的地方。

### 第三，成本和缓存不是优化题，而是架构题

Claude Code 把 prompt cache 稳定性写进了 system prompt、tool schema、tool ordering、fork context 的设计里，这非常少见，也非常正确。

---

## 最后的判断

Claude Code 的 Agent 设计最精髓的部分，不是“它能起多少个子代理”，而是：

> 它把代理当成一个有边界的执行单元来设计，而不是一个会自动续写的 prompt。

这就是它和很多 demo 级 agent 的根本差别。

