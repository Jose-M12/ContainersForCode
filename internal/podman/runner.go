package podman

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

type Result struct {
	Stdout string
	Stderr string
}

type CommandError struct {
	Operation string
	Stderr    string
	Err       error
}

func (e *CommandError) Error() string {
	detail := strings.TrimSpace(e.Stderr)
	if detail == "" {
		return fmt.Sprintf("podman %s: %v", e.Operation, e.Err)
	}
	return fmt.Sprintf("podman %s: %s", e.Operation, detail)
}

func (e *CommandError) Unwrap() error { return e.Err }

type Runner interface {
	Run(ctx context.Context, args ...string) (Result, error)
	Interactive(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, args ...string) error
	Available() error
}

type CLI struct {
	Binary string
}

func NewCLI() CLI { return CLI{Binary: "podman"} }

func (c CLI) Available() error {
	_, err := exec.LookPath(c.Binary)
	if err != nil {
		return fmt.Errorf("Podman CLI is not available in PATH")
	}
	return nil
}

func (c CLI) Run(ctx context.Context, args ...string) (Result, error) {
	if err := c.Available(); err != nil {
		return Result{}, err
	}
	command := exec.CommandContext(ctx, c.Binary, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	result := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		operation := "command"
		if len(args) > 0 {
			operation = args[0]
		}
		return result, &CommandError{Operation: operation, Stderr: result.Stderr, Err: err}
	}
	return result, nil
}

func (c CLI) Interactive(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, args ...string) error {
	if err := c.Available(); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, c.Binary, args...)
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return ctx.Err()
		}
		operation := "command"
		if len(args) > 0 {
			operation = args[0]
		}
		return &CommandError{Operation: operation, Err: err}
	}
	return nil
}
