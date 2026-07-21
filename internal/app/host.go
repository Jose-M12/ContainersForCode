package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"runtime"
	"strings"
)

func (a *Application) hostCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usage("host requires doctor or off-check")
	}
	switch args[0] {
	case "doctor":
		return a.hostDoctor(ctx, args[1:])
	case "off-check":
		return a.hostOffCheck(ctx, args[1:])
	default:
		return usage("unknown host subcommand %q", args[0])
	}
}

func (a *Application) hostDoctor(ctx context.Context, args []string) error {
	flags := newFlagSet("host doctor")
	output := flags.String("output", "human", "human or json")
	refresh := flags.Bool("refresh", false, "refresh capability discovery")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if err := requireArgs(flags, 0, 0); err != nil {
		return err
	}
	if err := ensureOutput(*output); err != nil {
		return err
	}
	report := map[string]any{"podmanAvailable": false, "paths": a.paths}
	var risks []string
	var detected runtimeContext
	if a.runner.Available() != nil {
		risks = append(risks, "Podman CLI is not installed or unavailable")
	} else {
		report["podmanAvailable"] = true
		var err error
		detected, err = a.discoverRuntime(ctx, *refresh)
		if err != nil {
			return err
		}
		report["capabilities"] = detected.Capabilities
		report["host"] = detected.Host
		containers, listErr := a.podman.ListManagedContainers(ctx, false)
		if listErr == nil {
			report["runningV2"] = len(containers)
		}
		if !detected.Capabilities.Rootless {
			risks = append(risks, "Podman is not rootless")
		}
		if !detected.Capabilities.ResourceLimits {
			risks = append(risks, "strict resource limits are not enforceable")
		}
		if !detected.Capabilities.Secrets {
			risks = append(risks, "Podman secrets support was not detected")
		}
	}
	if runtime.GOOS == "linux" {
		current, _ := user.Current()
		subUID := fileContainsUser("/etc/subuid", current.Username)
		subGID := fileContainsUser("/etc/subgid", current.Username)
		report["subuidConfigured"], report["subgidConfigured"] = subUID, subGID
		if !subUID || !subGID {
			risks = append(risks, "subuid/subgid mappings are incomplete")
		}
	}
	report["risks"] = risks
	if *output == "json" {
		if err := writeJSON(a.stdout, report); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(a.stdout, "Podman available: %v\nRootless: %t\nPodman version: %s\nCgroups: %s\nOCI runtime: %s\nStorage driver: %s\nArchitecture: %s\nPodman Machine: %t\nCPUs: %d\nMemory: %s\nPower: %s\nConfig: %s\n",
			report["podmanAvailable"], detected.Capabilities.Rootless, detected.Capabilities.PodmanVersion,
			detected.Capabilities.CgroupVersion, detected.Capabilities.OCIRuntime, detected.Capabilities.StorageDriver,
			detected.Capabilities.Architecture, detected.Capabilities.PodmanMachine, detected.Host.LogicalCPUs,
			humanBytes(detected.Host.TotalMemoryBytes), detected.Host.PowerSource, a.paths.Config)
		for _, risk := range risks {
			fmt.Fprintf(a.stdout, "RISK: %s\n", risk)
		}
	}
	if len(risks) > 0 {
		return &CodedError{Code: 1, Err: fmt.Errorf("host doctor found %d risk(s)", len(risks))}
	}
	return nil
}

func fileContainsUser(path, name string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	prefix := name + ":"
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func (a *Application) hostOffCheck(ctx context.Context, args []string) error {
	flags := newFlagSet("host off-check")
	allPodman := flags.Bool("all-podman", false, "inspect all rootless Podman workloads")
	output := flags.String("output", "human", "human or json")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if err := requireArgs(flags, 0, 0); err != nil {
		return err
	}
	if err := ensureOutput(*output); err != nil {
		return err
	}
	var names []string
	if *allPodman {
		result, err := a.runner.Run(ctx, "container", "ps", "--format", "json")
		if err != nil {
			return err
		}
		var raw []map[string]any
		if strings.TrimSpace(result.Stdout) != "" {
			if err := json.Unmarshal([]byte(result.Stdout), &raw); err != nil {
				return err
			}
		}
		for _, item := range raw {
			names = append(names, fmt.Sprint(item["Names"]))
		}
	} else {
		containers, err := a.podman.ListManagedContainers(ctx, false)
		if err != nil {
			return err
		}
		for _, container := range containers {
			names = append(names, container.Name)
		}
	}
	scope := "v2-only"
	if *allPodman {
		scope = "all-podman"
	}
	result := map[string]any{"scope": scope, "running": names, "off": len(names) == 0}
	if *output == "json" {
		_ = writeJSON(a.stdout, result)
	} else if len(names) == 0 {
		fmt.Fprintln(a.stdout, "off: no matching containers are running")
	} else {
		fmt.Fprintf(a.stdout, "not off: %d matching container(s) are running: %s\n", len(names), strings.Join(names, ", "))
	}
	if len(names) > 0 {
		return &CodedError{Code: 1, Err: fmt.Errorf("matching workloads are still running")}
	}
	return nil
}
