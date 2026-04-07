package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/caowenhua/x-agent/xxx-code/internal/config"
	"github.com/caowenhua/x-agent/xxx-code/internal/engine"
	"github.com/caowenhua/x-agent/xxx-code/internal/provider/anthropic"
	"github.com/caowenhua/x-agent/xxx-code/internal/tools"
)

type App struct {
	config  config.Config
	runner  *engine.Runner
	session *engine.Session
	out     io.Writer
	errOut  io.Writer
}

func New(cfg config.Config, out, errOut io.Writer) *App {
	registry := engine.NewRegistry(
		&tools.BashTool{},
		&tools.ReadFileTool{},
		&tools.WriteFileTool{},
		&tools.EditFileTool{},
		&tools.GlobTool{},
		&tools.GrepTool{},
		&tools.AgentSpawnTool{},
		&tools.AgentWaitTool{},
		&tools.AgentListTool{},
	)

	provider := anthropic.NewClient(cfg.APIKey, cfg.BaseURL, cfg.Version)
	runner := engine.NewRunner(provider, registry, engine.RunnerConfig{
		Model:         cfg.Model,
		SystemPrompt:  cfg.SystemPrompt,
		MaxTokens:     cfg.MaxTokens,
		MaxTurns:      cfg.MaxTurns,
		WorkingDir:    cfg.WorkingDir,
		ToolTimeout:   cfg.ToolTimeout,
		MaxAgentDepth: 3,
		EventHandler: func(event engine.Event) {
			printEvent(cfg.Verbose, out, errOut, event)
		},
	})

	return &App{
		config:  cfg,
		runner:  runner,
		session: engine.NewSession(),
		out:     out,
		errOut:  errOut,
	}
}

func (a *App) Run(ctx context.Context) error {
	if a.config.Print {
		_, err := a.runner.RunTurn(ctx, a.session, a.config.Prompt)
		return err
	}

	return a.runREPL(ctx)
}

func (a *App) runREPL(ctx context.Context) error {
	fmt.Fprintf(a.out, "xxx-code (%s)\n", a.config.Model)
	fmt.Fprintln(a.out, "Type :help for commands, :quit to exit.")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for {
		fmt.Fprint(a.out, ">>> ")
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return err
			}
			return nil
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, ":") {
			if done, err := a.handleCommand(ctx, line); err != nil {
				return err
			} else if done {
				return nil
			}
			continue
		}

		if _, err := a.runner.RunTurn(ctx, a.session, line); err != nil {
			fmt.Fprintf(a.errOut, "error: %v\n", err)
		}
	}
}

func (a *App) handleCommand(ctx context.Context, line string) (bool, error) {
	fields := strings.Fields(line)
	switch fields[0] {
	case ":quit", ":exit":
		return true, nil
	case ":help":
		fmt.Fprintln(a.out, ":help                show this help")
		fmt.Fprintln(a.out, ":quit                exit the REPL")
		fmt.Fprintln(a.out, ":agents              list spawned agents")
		fmt.Fprintln(a.out, ":wait <agent-id>     wait for an agent and print its snapshot")
		return false, nil
	case ":agents":
		snapshots := a.runner.ListAgents()
		data, _ := json.MarshalIndent(snapshots, "", "  ")
		fmt.Fprintln(a.out, string(data))
		return false, nil
	case ":wait":
		if len(fields) < 2 {
			fmt.Fprintln(a.errOut, "usage: :wait <agent-id>")
			return false, nil
		}
		waitCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
		snapshot, err := a.runner.WaitAgent(waitCtx, fields[1])
		if err != nil {
			fmt.Fprintf(a.errOut, "error: %v\n", err)
			return false, nil
		}
		data, _ := json.MarshalIndent(snapshot, "", "  ")
		fmt.Fprintln(a.out, string(data))
		return false, nil
	default:
		fmt.Fprintf(a.errOut, "unknown command: %s\n", fields[0])
		return false, nil
	}
}

func printEvent(verbose bool, out, errOut io.Writer, event engine.Event) {
	switch event.Kind {
	case engine.EventAssistantText:
		if strings.TrimSpace(event.Text) == "" {
			return
		}
		if event.AgentName != "" {
			fmt.Fprintf(out, "[%s] %s\n", event.AgentName, event.Text)
			return
		}
		fmt.Fprintln(out, event.Text)
	case engine.EventToolCall:
		if verbose {
			agentPrefix := ""
			if event.AgentName != "" {
				agentPrefix = "[" + event.AgentName + "] "
			}
			fmt.Fprintf(errOut, "%stool %s %s\n", agentPrefix, event.ToolName, event.Text)
		}
	case engine.EventToolResult:
		if verbose {
			agentPrefix := ""
			if event.AgentName != "" {
				agentPrefix = "[" + event.AgentName + "] "
			}
			fmt.Fprintf(errOut, "%stool-result %s %s\n", agentPrefix, event.ToolName, event.Text)
		}
	case engine.EventAgentSpawned:
		fmt.Fprintf(errOut, "spawned agent %s (%s)\n", event.AgentName, event.AgentID)
	case engine.EventAgentCompleted:
		fmt.Fprintf(errOut, "agent %s completed\n", event.AgentName)
	}
}
