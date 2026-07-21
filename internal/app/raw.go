package app

import (
	"context"
	"os"
	"strconv"
	"strings"
	"unicode"

	"containersagents.dev/v2/internal/manifest"
	"containersagents.dev/v2/internal/podman"
	"containersagents.dev/v2/internal/resources"
	"containersagents.dev/v2/internal/security"
	"containersagents.dev/v2/internal/state"
)

func (a *Application) rawRun(ctx context.Context, args []string) error {
	before, command := splitCommand(args)
	flags := newFlagSet("run")
	image := flags.String("image", "", "fully qualified OCI image reference")
	project := flags.String("project", "", "optional host project path")
	projectMode := flags.String("project-mode", "read-only", "read-only or read-write")
	network := flags.String("network", "outbound", "none or outbound")
	resourceClass := flags.String("resource", "battery", "battery, balanced, performance, or custom")
	tty := flags.Bool("tty", true, "allocate a TTY")
	dangerous := flags.Bool("allow-dangerous-mount", false, "allow project outside configured roots")
	confirm := flags.String("confirm", "", "must be raw-run for dangerous mounts")
	if err := parseFlags(flags, before); err != nil {
		return err
	}
	if err := requireArgs(flags, 0, 0); err != nil {
		return err
	}
	if *image == "" {
		return usage("run requires --image")
	}
	if strings.HasPrefix(*image, "-") || strings.IndexFunc(*image, func(r rune) bool { return unicode.IsSpace(r) || r < 0x20 || r == 0x7f }) >= 0 {
		return usage("run image reference must not begin with a hyphen or contain whitespace or control characters")
	}
	if *network != "none" && *network != "outbound" {
		return usage("raw run network must be none or outbound")
	}
	if *projectMode != "read-only" && *projectMode != "read-write" {
		return usage("project mode must be read-only or read-write")
	}
	runtime, err := a.requireRootless(ctx)
	if err != nil {
		return err
	}
	budget, err := resources.Calculate(*resourceClass, manifest.ResourceOverrides{}, manifest.MinimumResources{}, runtime.Host)
	if err != nil {
		return err
	}
	id, err := state.NewUUID()
	if err != nil {
		return err
	}
	name := "ca2-run-" + strings.ReplaceAll(id[:8], "-", "")
	runArgs := []string{
		"container", "run", "--rm", "--name", name,
		"--label", podman.ManagedLabel + "=true",
		"--label", "io.containersagents.v2.resource-type=raw-run",
		"--label", "io.containersagents.v2.session-id=" + id,
		"--memory", strconv.FormatInt(budget.MemoryBytes, 10),
		"--memory-swap", strconv.FormatInt(budget.SwapBytes, 10),
		"--cpus", strconv.FormatFloat(budget.CPUs, 'f', 2, 64),
		"--pids-limit", strconv.Itoa(budget.PIDs),
		"--shm-size", strconv.FormatInt(budget.SHMBytes, 10),
		"--cap-drop=all", "--security-opt=no-new-privileges", "--interactive",
	}
	if *tty {
		runArgs = append(runArgs, "--tty")
	}
	if *network == "none" {
		runArgs = append(runArgs, "--network", "none")
	}
	if *project != "" {
		if *dangerous {
			if err := confirmExact(*confirm, "raw-run", "dangerous raw-run mount"); err != nil {
				return err
			}
		}
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return homeErr
		}
		validator := security.MountValidator{Paths: a.paths, Home: home, AllowedRoots: runtime.Defaults.Spec.AllowedMountRoots, UID: currentUID()}
		validated, validateErr := validator.Validate(*project, *dangerous)
		if validateErr != nil {
			return validateErr
		}
		mode := "ro"
		if *projectMode == "read-write" {
			mode = "rw"
		}
		mount := "type=bind,src=" + validated.Source + ",dst=/workspace," + mode
		if runtime.Capabilities.SELinux {
			mount += ",relabel=private"
		}
		runArgs = append(runArgs, "--mount", mount, "--workdir", "/workspace")
	}
	runArgs = append(runArgs, *image)
	runArgs = append(runArgs, command...)
	return a.podman.RunInteractive(ctx, a.stdin, a.stdout, a.stderr, runArgs)
}
