package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"containersagents.dev/v2/internal/envstore"
	"containersagents.dev/v2/internal/podman"
	"containersagents.dev/v2/internal/profile"
	"containersagents.dev/v2/internal/state"
)

var (
	Version = "0.1.0-alpha.1"
	Commit  = "development"
	Date    = "unknown"
)

type Application struct {
	stdin    io.Reader
	stdout   io.Writer
	stderr   io.Writer
	paths    state.Paths
	states   state.Store
	envs     envstore.Store
	profiles profile.Store
	runner   podman.Runner
	podman   podman.Adapter
}

func New(stdin io.Reader, stdout, stderr io.Writer) (*Application, error) {
	paths, err := state.ResolvePaths()
	if err != nil {
		return nil, err
	}
	runner := podman.NewCLI()
	return &Application{
		stdin: stdin, stdout: stdout, stderr: stderr, paths: paths,
		states: state.Store{Paths: paths}, envs: envstore.Store{Paths: paths}, profiles: profile.Store{Paths: paths},
		runner: runner, podman: podman.Adapter{Runner: runner},
	}, nil
}

func (a *Application) Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		a.printHelp()
		return nil
	}
	switch args[0] {
	case "help", "--help", "-h":
		a.printHelp()
		return nil
	case "version", "--version":
		return a.version(args[1:])
	case "profile":
		return a.profileCommand(ctx, args[1:])
	case "env":
		return a.environmentCommand(ctx, args[1:])
	case "run":
		return a.rawRun(ctx, args[1:])
	case "host":
		return a.hostCommand(ctx, args[1:])
	case "disk":
		return a.diskCommand(ctx, args[1:])
	case "completion":
		return a.completion(args[1:])
	default:
		return usage("unknown command %q; run 'cagent help'", args[0])
	}
}

func (a *Application) printHelp() {
	fmt.Fprint(a.stdout, `cagent - secure, laptop-aware rootless Podman environments

Usage:
  cagent profile <list|show|validate|detect|build|remove>
  cagent env <init|list|show|plan|prepare|shell|start|exec|stop|recreate|delete|diff|doctor>
  cagent run --image IMAGE -- COMMAND [ARG...]
  cagent host <doctor|off-check>
  cagent disk <report|cleanup>
  cagent completion <bash|zsh|fish>
  cagent version [--output human|json]

Destructive commands always require an exact confirmation token. V2 never uses
global Podman prune/reset operations and only mutates V2-labeled resources.
`)
}

func (a *Application) version(args []string) error {
	flags := newFlagSet("version")
	output := flags.String("output", "human", "human or json")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	value := map[string]string{"version": Version, "commit": Commit, "buildDate": Date}
	if *output == "json" {
		return writeJSON(a.stdout, value)
	}
	if *output != "human" {
		return usage("--output must be human or json")
	}
	fmt.Fprintf(a.stdout, "cagent %s (%s, %s)\n", Version, Commit, Date)
	return nil
}

func newFlagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	return flags
}

func parseFlags(flags *flag.FlagSet, args []string) error {
	normalized, err := interspersedArgs(flags, args)
	if err != nil {
		return usage("%s: %v", flags.Name(), err)
	}
	if err := flags.Parse(normalized); err != nil {
		return usage("%s: %v", flags.Name(), err)
	}
	return nil
}

func interspersedArgs(flags *flag.FlagSet, args []string) ([]string, error) {
	var options, positionals []string
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--" {
			positionals = append(positionals, args[index:]...)
			break
		}
		if !strings.HasPrefix(argument, "-") || argument == "-" {
			positionals = append(positionals, argument)
			continue
		}
		name := strings.TrimLeft(argument, "-")
		if equals := strings.IndexByte(name, '='); equals >= 0 {
			name = name[:equals]
		}
		definition := flags.Lookup(name)
		if definition == nil {
			return nil, fmt.Errorf("flag provided but not defined: %s", argument)
		}
		options = append(options, argument)
		if strings.Contains(argument, "=") {
			continue
		}
		if boolean, ok := definition.Value.(interface{ IsBoolFlag() bool }); ok && boolean.IsBoolFlag() {
			continue
		}
		index++
		if index >= len(args) {
			return nil, fmt.Errorf("flag needs an argument: %s", argument)
		}
		options = append(options, args[index])
	}
	return append(options, positionals...), nil
}

func requireArgs(flags *flag.FlagSet, minimum, maximum int) error {
	count := flags.NArg()
	if count < minimum || (maximum >= 0 && count > maximum) {
		return usage("%s: expected %s positional argument(s), got %d", flags.Name(), expectedRange(minimum, maximum), count)
	}
	return nil
}

func expectedRange(minimum, maximum int) string {
	if maximum < 0 {
		return fmt.Sprintf("at least %d", minimum)
	}
	if minimum == maximum {
		return fmt.Sprintf("exactly %d", minimum)
	}
	return fmt.Sprintf("%d to %d", minimum, maximum)
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func confirmExact(provided, expected, operation string) error {
	if provided != expected {
		return policyError("%s requires --confirm %s", operation, expected)
	}
	return nil
}

func splitCommand(args []string) (before, command []string) {
	for i, arg := range args {
		if arg == "--" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}

func (a *Application) completion(args []string) error {
	if len(args) != 1 {
		return usage("completion requires bash, zsh, or fish")
	}
	switch args[0] {
	case "bash":
		fmt.Fprint(a.stdout, bashCompletion)
	case "zsh":
		fmt.Fprint(a.stdout, zshCompletion)
	case "fish":
		fmt.Fprint(a.stdout, fishCompletion)
	default:
		return usage("unsupported completion shell %q", args[0])
	}
	return nil
}

const bashCompletion = `complete -W "profile env run host disk completion version help" cagent
`
const zshCompletion = `#compdef cagent
_arguments '1:command:(profile env run host disk completion version help)'
`
const fishCompletion = `complete -c cagent -f -a 'profile env run host disk completion version help'
`

func cleanErrorText(err error) string {
	if err == nil {
		return ""
	}
	return strings.ReplaceAll(err.Error(), "\n", "; ")
}

func fileExists(path string) bool { _, err := os.Stat(path); return err == nil }
