package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"containersagents.dev/v2/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	application, err := app.New(os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cagent: %v\n", err)
		os.Exit(1)
	}
	if err := application.Run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "cagent: %v\n", err)
		os.Exit(app.ExitCode(err))
	}
}
