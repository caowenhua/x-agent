package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/caowenhua/x-agent/xxx-code/internal/buildinfo"
	"github.com/caowenhua/x-agent/xxx-code/internal/stability"
)

func main() {
	os.Exit(runMain(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func runMain(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if stdin == nil {
		stdin = os.Stdin
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	var cfg stability.Config
	var summaryPath string
	var showVersion bool
	var helperPluginEcho bool

	fs := flag.NewFlagSet("xxx-code-stability", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.DurationVar(&cfg.Duration, "duration", 0, "Maximum wall-clock runtime for the soak run (for example 30m)")
	fs.IntVar(&cfg.Iterations, "iterations", 0, "Maximum number of completed rounds before stopping")
	fs.IntVar(&cfg.Concurrency, "concurrency", 2, "Number of workers that execute the scenario suite in parallel")
	fs.IntVar(&cfg.RestartEvery, "restart-every", 10, "Restart the in-process daemon after this many completed rounds")
	fs.DurationVar(&cfg.ScenarioTimeout, "scenario-timeout", 20*time.Second, "Timeout applied to each scenario execution")
	fs.DurationVar(&cfg.ProgressEvery, "progress-every", 20*time.Second, "Progress print interval")
	fs.StringVar(&cfg.WorkingDir, "workdir", "", "Optional working directory for daemon state and artifacts")
	fs.BoolVar(&cfg.KeepWorkDir, "keep-workdir", false, "Keep the working directory even after a successful run")
	fs.BoolVar(&cfg.Verbose, "verbose", false, "Print round and restart milestones")
	fs.StringVar(&cfg.HelperBinary, "helper-binary", "", "Optional helper binary used by the plugin echo scenario")
	fs.StringVar(&summaryPath, "summary-json", "", "Optional path to write a JSON summary, even when the run fails")
	fs.BoolVar(&showVersion, "version", false, "Print version information and exit")
	fs.BoolVar(&helperPluginEcho, "helper-plugin-echo", false, "Internal helper used by the plugin echo scenario")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: %s [flags]\n\n", fs.Name())
		fmt.Fprintln(fs.Output(), "Run the built-in stability and soak scenarios against an in-process xxx-code daemon.")
		fmt.Fprintln(fs.Output(), "No external model provider or API key is required.")
		fmt.Fprintln(fs.Output())
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintf(stderr, "config error: %v\n", err)
		return 1
	}
	if len(fs.Args()) > 0 {
		fmt.Fprintf(stderr, "config error: unexpected arguments: %s\n", strings.Join(fs.Args(), " "))
		return 1
	}
	if showVersion {
		fmt.Fprint(stdout, buildinfo.String())
		return 0
	}
	if helperPluginEcho {
		if _, err := io.Copy(stdout, stdin); err != nil {
			fmt.Fprintf(stderr, "helper error: %v\n", err)
			return 1
		}
		return 0
	}

	result, runErr := stability.Run(context.Background(), cfg, stdout, stderr)
	if summaryPath != "" {
		if err := writeSummary(summaryPath, result); err != nil {
			fmt.Fprintf(stderr, "summary write error: %v\n", err)
			if runErr == nil {
				runErr = err
			}
		}
	}
	if runErr != nil {
		return 1
	}
	return 0
}

func writeSummary(path string, result stability.Result) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}
