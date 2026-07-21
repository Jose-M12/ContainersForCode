package app

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"containersagents.dev/v2/internal/fsutil"
	"containersagents.dev/v2/internal/podman"
)

func (a *Application) diskCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usage("disk requires report or cleanup")
	}
	switch args[0] {
	case "report":
		return a.diskReport(ctx, args[1:])
	case "cleanup":
		return a.diskCleanup(ctx, args[1:])
	default:
		return usage("unknown disk subcommand %q", args[0])
	}
}

func (a *Application) diskReport(ctx context.Context, args []string) error {
	flags := newFlagSet("disk report")
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
	images, err := a.podman.ListManagedImages(ctx)
	if err != nil {
		return err
	}
	containers, err := a.podman.ListManagedContainers(ctx, true)
	if err != nil {
		return err
	}
	networks, err := a.podman.ListManagedNetworks(ctx)
	if err != nil {
		return err
	}
	homesSize, _ := directorySize(filepath.Join(a.paths.Data, "environments"))
	cacheSize, _ := directorySize(a.paths.Cache)
	systemDF, dfErr := a.runner.Run(ctx, "system", "df", "--format", "json")
	value := map[string]any{
		"managedImages": images, "managedContainers": containers, "managedNetworks": networks,
		"environmentDataBytes": homesSize, "cacheBytes": cacheSize,
		"unrelatedPodmanInformational": strings.TrimSpace(systemDF.Stdout),
	}
	if dfErr != nil {
		value["podmanDiskInfoError"] = cleanErrorText(dfErr)
	}
	if *output == "json" {
		return writeJSON(a.stdout, value)
	}
	fmt.Fprintf(a.stdout, "V2 images: %d\nV2 containers: %d\nV2 networks: %d\nEnvironment data: %s\nV2 cache: %s\n",
		len(images), len(containers), len(networks), humanBytes(homesSize), humanBytes(cacheSize))
	fmt.Fprintln(a.stdout, "Unrelated Podman data is informational only and is never cleaned by cagent.")
	return nil
}

type cleanupCandidate struct {
	Type    string `json:"type"`
	ID      string `json:"id,omitempty"`
	Name    string `json:"name"`
	Reason  string `json:"reason"`
	OwnerID string `json:"-"`
}

func (a *Application) diskCleanup(ctx context.Context, args []string) error {
	flags := newFlagSet("disk cleanup")
	managed := flags.Bool("managed", false, "apply the V2-only cleanup plan")
	planOnly := flags.Bool("plan", false, "show the cleanup plan")
	confirm := flags.String("confirm", "", "must be managed to apply")
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
	if *managed && *planOnly {
		return usage("choose --plan or --managed, not both")
	}
	candidates, err := a.cleanupCandidates(ctx)
	if err != nil {
		return err
	}
	if *output == "json" {
		if err := writeJSON(a.stdout, map[string]any{"candidates": candidates, "applied": *managed}); err != nil {
			return err
		}
	} else {
		if len(candidates) == 0 {
			fmt.Fprintln(a.stdout, "no reclaimable V2-managed resources")
		}
		for _, candidate := range candidates {
			fmt.Fprintf(a.stdout, "%-10s %-24s %s\n", candidate.Type, candidate.Name, candidate.Reason)
		}
	}
	if !*managed {
		return nil
	}
	if err := confirmExact(*confirm, "managed", "managed cleanup"); err != nil {
		return err
	}
	for _, candidate := range candidates {
		switch candidate.Type {
		case "container":
			inspect, exists, inspectErr := a.podman.InspectContainer(ctx, candidate.ID)
			if inspectErr != nil {
				return inspectErr
			}
			if !exists {
				continue
			}
			if inspect.Labels[podman.ManagedLabel] != "true" || inspect.Labels[podman.EnvironmentIDLabel] != candidate.OwnerID {
				return policyError("container ownership changed during cleanup; refusing removal")
			}
			if !inspect.Running {
				if err := a.podman.RemoveContainer(ctx, candidate.ID); err != nil {
					return err
				}
			}
		case "network":
			network, exists, inspectErr := a.podman.InspectNetwork(ctx, candidate.Name)
			if inspectErr != nil {
				return inspectErr
			}
			if !exists || network.Labels[podman.ManagedLabel] != "true" || network.Labels[podman.EnvironmentIDLabel] != candidate.OwnerID {
				return policyError("network ownership changed during cleanup; refusing removal")
			}
			if err := a.podman.RemoveNetwork(ctx, candidate.Name); err != nil {
				return err
			}
		case "image":
			if err := a.podman.RemoveImage(ctx, candidate.ID); err != nil {
				return err
			}
		case "build-cache":
			root := filepath.Join(a.paths.Cache, "profile-builds")
			target := filepath.Join(root, candidate.Name)
			if !fsutil.IsWithin(target, root) {
				return policyError("cleanup target escaped V2 cache root")
			}
			if err := os.RemoveAll(target); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *Application) cleanupCandidates(ctx context.Context) ([]cleanupCandidate, error) {
	index, err := a.states.LoadIndex()
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, id := range index.Environments {
		ids[id] = true
	}
	var result []cleanupCandidate
	containers, err := a.podman.ListManagedContainers(ctx, true)
	if err != nil {
		return nil, err
	}
	for _, container := range containers {
		id := container.Labels[podman.EnvironmentIDLabel]
		if id != "" && !ids[id] {
			inspect, exists, inspectErr := a.podman.InspectContainer(ctx, container.ID)
			if inspectErr != nil {
				return nil, inspectErr
			}
			if exists && !inspect.Running {
				result = append(result, cleanupCandidate{Type: "container", ID: container.ID, Name: container.Name, Reason: "orphaned V2 container; no desired environment state", OwnerID: id})
			}
		}
	}
	networks, err := a.podman.ListManagedNetworks(ctx)
	if err != nil {
		return nil, err
	}
	for _, network := range networks {
		id := network.Labels[podman.EnvironmentIDLabel]
		if id != "" && !ids[id] {
			result = append(result, cleanupCandidate{Type: "network", ID: network.ID, Name: network.Name, Reason: "orphaned V2 network", OwnerID: id})
		}
	}
	profiles, err := a.profiles.List()
	if err != nil {
		return nil, err
	}
	hashes := map[string]bool{}
	for _, summary := range profiles {
		if summary.Valid {
			resolved, resolveErr := a.profiles.Resolve(summary.Name)
			if resolveErr == nil {
				hashes[resolved.Hash] = true
			}
		}
	}
	images, err := a.podman.ListManagedImages(ctx)
	if err != nil {
		return nil, err
	}
	for _, image := range images {
		hash := image.Labels[podman.ProfileHashLabel]
		if hash != "" && !hashes[hash] {
			result = append(result, cleanupCandidate{Type: "image", ID: image.ID, Name: image.Repository + ":" + image.Tag, Reason: "unused V2 profile image"})
		}
	}
	entries, _ := os.ReadDir(filepath.Join(a.paths.Cache, "profile-builds"))
	for _, entry := range entries {
		if entry.IsDir() && !hashes[entry.Name()] {
			result = append(result, cleanupCandidate{Type: "build-cache", Name: entry.Name(), Reason: "unused extracted built-in context"})
		}
	}
	return result, nil
}

func directorySize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	if os.IsNotExist(err) {
		return 0, nil
	}
	return total, err
}
