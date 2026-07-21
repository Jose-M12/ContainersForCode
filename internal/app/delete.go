package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"containersagents.dev/v2/internal/fsutil"
	"containersagents.dev/v2/internal/locks"
	"containersagents.dev/v2/internal/planner"
	"containersagents.dev/v2/internal/podman"
	"containersagents.dev/v2/internal/policy"
	"containersagents.dev/v2/internal/resources"
)

func (a *Application) environmentDelete(ctx context.Context, args []string) error {
	flags := newFlagSet("env delete")
	confirm := flags.String("confirm", "", "exact environment name")
	deleteHome := flags.Bool("delete-home", false, "also permanently remove the per-environment data directory")
	output := flags.String("output", "human", "human or json")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if err := requireArgs(flags, 1, 1); err != nil {
		return err
	}
	if err := ensureOutput(*output); err != nil {
		return err
	}
	name := flags.Arg(0)
	if err := confirmExact(*confirm, name, "environment deletion"); err != nil {
		return err
	}
	environment, err := a.envs.Load(name)
	if err != nil {
		return err
	}
	stored, registered, err := a.states.GetByName(name)
	if err != nil {
		return err
	}
	actions := []planner.Action{{Type: "remove-manifest", Resource: a.paths.EnvironmentManifest(name), Description: "remove the desired environment manifest", Destructive: true}}
	if registered && stored.ContainerName != "" {
		actions = append(actions, planner.Action{Type: "remove-container", Resource: stored.ContainerName, Description: "stop and remove the environment container; mutable root changes will be lost", Destructive: true})
	}
	if registered && stored.NetworkName != "" {
		actions = append(actions, planner.Action{Type: "remove-network", Resource: stored.NetworkName, Description: "remove the environment internal network", Destructive: true})
	}
	if *deleteHome && registered {
		actions = append(actions, planner.Action{Type: "remove-environment-data", Resource: a.paths.EnvironmentData(stored.ID), Description: "permanently remove the persistent home, data, and certificates", Destructive: true})
	}
	result := map[string]any{"environment": name, "actions": actions, "persistentHomeRetained": registered && !*deleteHome, "retainedPath": ""}
	if registered && !*deleteHome {
		result["retainedPath"] = a.paths.EnvironmentData(stored.ID)
	}
	if *output == "json" {
		if err := writeJSON(a.stdout, result); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(a.stdout, "Delete plan for %s:\n", name)
		for _, action := range actions {
			fmt.Fprintf(a.stdout, "  - %s: %s [DESTRUCTIVE]\n", action.Type, action.Description)
		}
		if registered && !*deleteHome {
			fmt.Fprintf(a.stdout, "Persistent data retained: %s\n", a.paths.EnvironmentData(stored.ID))
		}
	}
	if err := a.paths.Ensure(); err != nil {
		return err
	}
	global, err := locks.Acquire(a.paths.GlobalLock())
	if err != nil {
		return err
	}
	defer global.Release()
	if registered {
		environmentLock, lockErr := locks.Acquire(a.paths.EnvironmentLock(stored.ID))
		if lockErr != nil {
			return lockErr
		}
		defer environmentLock.Release()
	}
	if registered && stored.ContainerName != "" {
		inspect, exists, inspectErr := a.podman.InspectContainer(ctx, stored.ContainerName)
		if inspectErr != nil {
			return inspectErr
		}
		if exists {
			if err := podman.VerifyEnvironment(inspect, stored.ID); err != nil {
				return err
			}
			if inspect.Running {
				if err := a.podman.Stop(ctx, stored.ContainerName, 10); err != nil {
					return err
				}
			}
			if err := a.podman.RemoveContainer(ctx, stored.ContainerName); err != nil {
				return err
			}
		}
	}
	if registered && stored.NetworkName != "" {
		networks, networkErr := a.podman.ListManagedNetworks(ctx)
		if networkErr != nil {
			return networkErr
		}
		owned := false
		for _, network := range networks {
			if network.Name == stored.NetworkName && network.Labels[podman.EnvironmentIDLabel] == stored.ID {
				owned = true
				break
			}
		}
		if owned {
			network, exists, inspectErr := a.podman.InspectNetwork(ctx, stored.NetworkName)
			if inspectErr != nil {
				return inspectErr
			}
			if !exists || network.Labels[podman.ManagedLabel] != "true" || network.Labels[podman.EnvironmentIDLabel] != stored.ID {
				return policyError("network ownership changed during delete; refusing removal")
			}
			if err := a.podman.RemoveNetwork(ctx, stored.NetworkName); err != nil {
				return err
			}
		}
	}
	if err := os.Remove(a.paths.EnvironmentManifest(name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	if registered {
		emptyPlan := planner.Plan{Environment: name, EnvironmentID: stored.ID, Profile: environment.Spec.Profile, ProfileHash: stored.ProfileHash, SpecHash: stored.SpecHash, ProjectHash: stored.ProjectHash, Security: policy.SecurityPlan{Class: stored.SecurityClass}, Resources: resources.Budget{Class: stored.ResourceClass, MemoryBytes: stored.MemoryBytes}, Actions: actions}
		a.appendAudit(stored, emptyPlan, "delete", "success", "", name)
		if *deleteHome {
			target := a.paths.EnvironmentData(stored.ID)
			root := filepath.Join(a.paths.Data, "environments")
			if !fsutil.IsWithin(target, root) {
				return policyError("refuse to delete data outside the V2 environment data root")
			}
			if err := os.RemoveAll(target); err != nil {
				return err
			}
		}
		if err := a.states.Forget(name); err != nil {
			return err
		}
	}
	fmt.Fprintf(a.stdout, "deleted environment %s\n", name)
	return nil
}
