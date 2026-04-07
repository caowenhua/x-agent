package main

import (
	"context"
	"fmt"
	"os"

	"github.com/caowenhua/x-agent/xxx-code/internal/cli"
	"github.com/caowenhua/x-agent/xxx-code/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	app := cli.New(cfg, os.Stdout, os.Stderr)
	if err := app.Run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "run error: %v\n", err)
		os.Exit(1)
	}
}
