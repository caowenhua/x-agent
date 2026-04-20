# Workflow Workspace Brief

This example workspace demonstrates how to use `xxx-code` as a small multi-agent planning and reporting system.

The scenario is a weekly release review:

- read roadmap notes
- read incident notes
- read delivery metrics
- fan out sub-agents to summarize each source in parallel
- use a dependent workflow task to draft one release digest
- inspect workflow state
- write the final digest to `outputs/release-digest.md`

This workspace is meant to show explicit `agent_fanout`, `depends_on`, prompt references such as `{{tasks.roadmap.result}}`, and workflow persistence tools like `workflow_tasks`.
