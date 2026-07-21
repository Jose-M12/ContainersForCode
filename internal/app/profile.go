package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"containersagents.dev/v2/internal/fsutil"
	"containersagents.dev/v2/internal/locks"
	"containersagents.dev/v2/internal/manifest"
	"containersagents.dev/v2/internal/podman"
	profilepkg "containersagents.dev/v2/internal/profile"
	"containersagents.dev/v2/internal/resources"
)

func (a *Application) profileCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usage("profile requires a subcommand")
	}
	switch args[0] {
	case "list":
		return a.profileList(args[1:])
	case "show":
		return a.profileShow(args[1:])
	case "validate":
		return a.profileValidate(args[1:])
	case "detect":
		return a.profileDetect(args[1:])
	case "build":
		return a.profileBuild(ctx, args[1:])
	case "remove":
		return a.profileRemove(args[1:])
	default:
		return usage("unknown profile subcommand %q", args[0])
	}
}

func (a *Application) profileList(args []string) error {
	flags := newFlagSet("profile list")
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
	profiles, err := a.profiles.List()
	if err != nil {
		return err
	}
	if *output == "json" {
		return writeJSON(a.stdout, profiles)
	}
	for _, item := range profiles {
		status := "valid"
		if !item.Valid {
			status = "invalid: " + item.Error
		}
		fmt.Fprintf(a.stdout, "%-24s %-8s %-8s %s\n", item.Name, item.Source, item.Mode, status)
	}
	return nil
}

func (a *Application) profileShow(args []string) error {
	flags := newFlagSet("profile show")
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
	resolved, err := a.profiles.Resolve(flags.Arg(0))
	if err != nil {
		return err
	}
	if *output == "json" {
		return writeJSON(a.stdout, resolved)
	}
	fmt.Fprintf(a.stdout, "Profile: %s\nSource: %s\nImage: %s\nHash: %s\nShell: %s\nIdentity: %s\n", resolved.Manifest.Metadata.Name, resolved.Source, resolved.ImageReference, resolved.Hash, strings.Join(resolved.Manifest.Spec.Runtime.Shell, " "), resolved.Manifest.Spec.Runtime.IdentityMode)
	return nil
}

func (a *Application) profileValidate(args []string) error {
	flags := newFlagSet("profile validate")
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
	resolved, err := a.profiles.Resolve(flags.Arg(0))
	if err != nil {
		return err
	}
	result := map[string]any{"name": resolved.Manifest.Metadata.Name, "valid": true, "hash": resolved.Hash, "imageReference": resolved.ImageReference, "source": resolved.Source}
	if *output == "json" {
		return writeJSON(a.stdout, result)
	}
	fmt.Fprintf(a.stdout, "%s is valid\nprofile hash: %s\nimage: %s\n", resolved.Manifest.Metadata.Name, resolved.Hash, resolved.ImageReference)
	return nil
}

func (a *Application) profileBuild(ctx context.Context, args []string) error {
	flags := newFlagSet("profile build")
	force := flags.Bool("force", false, "build even if the content-tagged image exists")
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
	if err := a.paths.Ensure(); err != nil {
		return err
	}
	global, err := locks.Acquire(a.paths.GlobalLock())
	if err != nil {
		return err
	}
	defer global.Release()
	buildLock, err := locks.Acquire(a.paths.ProfileBuildLock(flags.Arg(0)))
	if err != nil {
		return err
	}
	defer buildLock.Release()
	resolved, err := a.profiles.Resolve(flags.Arg(0))
	if err != nil {
		return err
	}
	runtime, err := a.requireRootless(ctx)
	if err != nil {
		return err
	}
	budget, err := resources.Calculate("balanced", manifest.ResourceOverrides{}, resolved.Manifest.Spec.MinimumResources, runtime.Host)
	if err != nil {
		return err
	}
	exists, err := a.podman.ImageExists(ctx, resolved.ImageReference)
	if err != nil {
		return err
	}
	result := map[string]any{"profile": resolved.Manifest.Metadata.Name, "image": resolved.ImageReference, "profileHash": resolved.Hash, "alreadyExists": exists}
	if exists && !*force {
		if *output == "json" {
			return writeJSON(a.stdout, result)
		}
		fmt.Fprintf(a.stdout, "image is current: %s\n", resolved.ImageReference)
		return nil
	}
	if resolved.Manifest.Spec.Image.Mode == "existing" {
		return notFound("required existing image %q is unavailable", resolved.ImageReference)
	}
	if resolved.Manifest.Spec.Image.Mode == "pull" {
		if err := a.podman.Pull(ctx, resolved.ImageReference, resolved.Manifest.Spec.Image.PullPolicy); err != nil {
			return err
		}
	} else {
		if len(resolved.Manifest.Spec.Image.BuildSecrets) > 0 {
			return policyError("buildSecrets require an approved external provider, which is not configured")
		}
		contextPath, containerfile, err := a.profiles.Materialize(resolved)
		if err != nil {
			return err
		}
		report, err := profilepkg.InspectContext(contextPath)
		if err != nil {
			return err
		}
		if err := validateBuildContext(contextPath, report); err != nil {
			return err
		}
		buildArgs := profileBuildArgs(resolved, containerfile, contextPath, budget.BuildJobs)
		if _, err := a.podman.Build(ctx, buildArgs); err != nil {
			return err
		}
		result["context"] = report
	}
	result["built"] = true
	if *output == "json" {
		return writeJSON(a.stdout, result)
	}
	fmt.Fprintf(a.stdout, "profile image ready: %s\n", resolved.ImageReference)
	return nil
}

func validateBuildContext(contextPath string, report profilepkg.ContextReport) error {
	if !report.Containerignore {
		return policyError("build context %q must contain .containerignore", contextPath)
	}
	if len(report.SuspiciousPaths) > 0 {
		return policyError("build context contains likely secret files not excluded by .containerignore: %s", strings.Join(report.SuspiciousPaths, ", "))
	}
	if report.SizeBytes > 2*resources.GiB {
		return policyError("build context is %s; contexts above 2 GiB require reduction", humanBytes(report.SizeBytes))
	}
	return nil
}

func profileBuildArgs(resolved profilepkg.Resolved, containerfile, contextPath string, jobs int) []string {
	image := resolved.Manifest.Spec.Image
	args := []string{"build", "--layers", "--pull=" + image.PullPolicy, "--jobs", strconv.Itoa(jobs), "--file", containerfile, "--tag", resolved.ImageReference, "--label", podman.ManagedLabel + "=true", "--label", "io.containersagents.v2.resource-type=profile-image", "--label", podman.ProfileHashLabel + "=" + resolved.Hash}
	if image.Target != "" {
		args = append(args, "--target", image.Target)
	}
	keys := make([]string, 0, len(image.BuildArgs))
	for key := range image.BuildArgs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "--build-arg", key+"="+image.BuildArgs[key])
	}
	return append(args, contextPath)
}

func (a *Application) profileDetect(args []string) error {
	flags := newFlagSet("profile detect")
	containerfile := flags.String("containerfile", "", "Containerfile path")
	contextPath := flags.String("context", "", "explicit build context")
	name := flags.String("name", "detected-profile", "draft profile name")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if err := requireArgs(flags, 0, 0); err != nil {
		return err
	}
	if *containerfile == "" || *contextPath == "" {
		return usage("profile detect requires --containerfile and --context")
	}
	containerfileResolved, err := fsutil.ResolveExisting(*containerfile)
	if err != nil {
		return err
	}
	contextResolved, err := fsutil.ResolveExisting(*contextPath)
	if err != nil {
		return err
	}
	if !fsutil.IsWithin(containerfileResolved, contextResolved) {
		return policyError("Containerfile must be inside the explicit context")
	}
	file, err := os.Open(containerfileResolved)
	if err != nil {
		return err
	}
	defer file.Close()
	user, workdir := "", "/workspace"
	shell := []string{"/bin/sh"}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch strings.ToUpper(fields[0]) {
		case "USER":
			user = fields[1]
		case "WORKDIR":
			workdir = fields[1]
		case "SHELL":
			var detected []string
			if json.Unmarshal([]byte(strings.TrimSpace(line[len(fields[0]):])), &detected) == nil && len(detected) > 0 {
				shell = detected
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	identity, home := "image-user", "/root"
	if user != "" && user != "root" && user != "0" {
		identity, home = "explicit", "/home/"+user
	}
	draft := manifest.Profile{APIVersion: manifest.APIVersion, Kind: manifest.ProfileKind, Metadata: manifest.Metadata{Name: *name}, Spec: manifest.ProfileSpec{
		Image:    manifest.ImageSpec{Mode: "build", Repository: "localhost/containersagents-v2/" + *name, Containerfile: containerfileResolved, Context: contextResolved, PullPolicy: "newer"},
		Runtime:  manifest.RuntimeSpec{User: user, Home: home, Workdir: workdir, Shell: shell, Keepalive: []string{"sleep", "infinity"}, IdentityMode: identity},
		Defaults: manifest.ProfileDefaults{SecurityClass: "development", ResourceClass: "balanced", RootFSPersistence: "persistent"},
	}}
	return writeJSON(a.stdout, map[string]any{"trusted": false, "message": "draft only; verify user, home, shell, keepalive, and image behavior", "profile": draft})
}

func (a *Application) profileRemove(args []string) error {
	flags := newFlagSet("profile remove")
	confirm := flags.String("confirm", "", "exact profile name")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if err := requireArgs(flags, 1, 1); err != nil {
		return err
	}
	name := flags.Arg(0)
	if err := confirmExact(*confirm, name, "profile removal"); err != nil {
		return err
	}
	if err := a.paths.Ensure(); err != nil {
		return err
	}
	global, err := locks.Acquire(a.paths.GlobalLock())
	if err != nil {
		return err
	}
	defer global.Release()
	profileLock, err := locks.Acquire(a.paths.ProfileBuildLock(name))
	if err != nil {
		return err
	}
	defer profileLock.Release()
	resolved, err := a.profiles.Resolve(name)
	if err != nil {
		return err
	}
	if resolved.Source != "custom" {
		return policyError("built-in profiles cannot be removed")
	}
	environments, err := a.envs.List()
	if err != nil {
		return err
	}
	for _, environmentName := range environments {
		environment, loadErr := a.envs.Load(environmentName)
		if loadErr == nil && environment.Spec.Profile == name {
			return policyError("profile %q is referenced by environment %q", name, environmentName)
		}
	}
	dir := filepath.Dir(resolved.SourcePath)
	if !fsutil.IsWithin(dir, a.paths.ProfilesDir()) {
		return policyError("refuse to remove profile outside the V2 profile directory")
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "removed custom profile %s\n", name)
	return nil
}
