package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"containersagents.dev/v2/internal/fsutil"
	"containersagents.dev/v2/internal/locks"
	"containersagents.dev/v2/internal/manifest"
	"containersagents.dev/v2/internal/planner"
	"containersagents.dev/v2/internal/podman"
)

func (a *Application) environmentCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usage("env requires a subcommand")
	}
	switch args[0] {
	case "init":
		return a.environmentInit(args[1:])
	case "list":
		return a.environmentList(ctx, args[1:])
	case "show":
		return a.environmentShow(ctx, args[1:])
	case "plan":
		return a.environmentPlan(ctx, args[1:])
	case "prepare":
		return a.environmentPrepare(ctx, args[1:])
	case "start":
		return a.environmentStart(ctx, args[1:])
	case "shell":
		return a.environmentShell(ctx, args[1:])
	case "exec":
		return a.environmentExec(ctx, args[1:])
	case "stop":
		return a.environmentStop(ctx, args[1:])
	case "recreate":
		return a.environmentRecreate(ctx, args[1:])
	case "delete":
		return a.environmentDelete(ctx, args[1:])
	case "diff":
		return a.environmentDiff(ctx, args[1:])
	case "doctor":
		return a.environmentDoctor(ctx, args[1:])
	default:
		return usage("unknown env subcommand %q", args[0])
	}
}

func (a *Application) environmentInit(args []string) error {
	flags := newFlagSet("env init")
	profileName := flags.String("profile", "", "profile name")
	project := flags.String("project", "", "host project directory")
	projectMode := flags.String("project-mode", "read-write", "read-only or read-write")
	containerfile := flags.String("containerfile", "", "custom Containerfile")
	contextPath := flags.String("context", "", "explicit custom build context")
	shell := flags.String("shell", "/bin/sh", "custom profile shell executable")
	keepalive := flags.String("keepalive", "sleep", "custom profile keepalive executable")
	user := flags.String("user", "", "custom profile runtime user")
	homePath := flags.String("home", "", "custom profile container home")
	workdir := flags.String("workdir", "/workspace", "custom profile workdir")
	securityClass := flags.String("security", "", "security class")
	resourceClass := flags.String("resource", "", "resource class")
	rootfs := flags.String("rootfs", "persistent", "root filesystem persistence")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if err := requireArgs(flags, 1, 1); err != nil {
		return err
	}
	name := flags.Arg(0)
	if err := manifest.ValidateName(name); err != nil {
		return usage("invalid environment name %q: %v", name, err)
	}
	if (*profileName == "") == (*containerfile == "") {
		return usage("choose exactly one of --profile or --containerfile")
	}
	if *containerfile != "" && *contextPath == "" {
		return usage("--containerfile requires --context")
	}
	if *project == "" {
		return usage("--project is required for managed environments")
	}
	projectResolved, err := fsutil.ResolveExisting(*project)
	if err != nil {
		return err
	}
	info, err := os.Stat(projectResolved)
	if err != nil || !info.IsDir() {
		return usage("project must be an existing directory")
	}
	if err := a.paths.Ensure(); err != nil {
		return err
	}
	global, err := locks.Acquire(a.paths.GlobalLock())
	if err != nil {
		return err
	}
	defer global.Release()
	if fileExists(a.paths.EnvironmentManifest(name)) {
		return policyError("environment %q already exists", name)
	}
	selectedProfile := *profileName
	if *containerfile != "" {
		selectedProfile = name + "-local"
		containerfileResolved, resolveErr := fsutil.ResolveExisting(*containerfile)
		if resolveErr != nil {
			return resolveErr
		}
		contextResolved, resolveErr := fsutil.ResolveExisting(*contextPath)
		if resolveErr != nil {
			return resolveErr
		}
		if !fsutil.IsWithin(containerfileResolved, contextResolved) {
			return policyError("Containerfile must be inside the explicit build context")
		}
		hostHome, _ := os.UserHomeDir()
		hostHome, _ = fsutil.ResolveExisting(hostHome)
		if contextResolved == hostHome {
			return policyError("the full host home cannot be used as a build context")
		}
		if !fileExists(filepath.Join(contextResolved, ".containerignore")) {
			return policyError("custom build context must contain .containerignore")
		}
		identity := "image-user"
		containerHome := *homePath
		if *user != "" {
			identity = "explicit"
		}
		if containerHome == "" {
			if *user == "" || *user == "root" || *user == "0" {
				containerHome = "/root"
			} else {
				containerHome = "/home/" + *user
			}
		}
		custom := manifest.Profile{APIVersion: manifest.APIVersion, Kind: manifest.ProfileKind, Metadata: manifest.Metadata{Name: selectedProfile}, Spec: manifest.ProfileSpec{
			Image:    manifest.ImageSpec{Mode: "build", Repository: "localhost/containersagents-v2/" + selectedProfile, Containerfile: containerfileResolved, Context: contextResolved, PullPolicy: "newer"},
			Runtime:  manifest.RuntimeSpec{User: *user, Home: containerHome, Workdir: *workdir, Shell: []string{*shell}, Keepalive: []string{*keepalive, "infinity"}, IdentityMode: identity},
			Defaults: manifest.ProfileDefaults{SecurityClass: "development", ResourceClass: "balanced", RootFSPersistence: "persistent"},
		}}
		if _, err := a.profiles.WriteCustom(custom, nil); err != nil {
			return err
		}
	} else {
		if _, err := a.profiles.Resolve(selectedProfile); err != nil {
			return err
		}
	}
	selected, err := a.profiles.Resolve(selectedProfile)
	if err != nil {
		return err
	}
	environment := manifest.Environment{APIVersion: manifest.APIVersion, Kind: manifest.EnvironmentKind, Metadata: manifest.Metadata{Name: name}, Spec: manifest.EnvironmentSpec{
		Profile: selectedProfile,
		Project: &manifest.ProjectSpec{Source: projectResolved, Target: selected.Manifest.Spec.Runtime.Workdir, Mode: *projectMode},
		Home:    manifest.HomeSpec{Persistence: "per-environment"}, RootFS: manifest.RootFSSpec{Persistence: *rootfs},
		SecurityClass: *securityClass, ResourceClass: *resourceClass,
		Concurrency: manifest.ConcurrencySpec{ProjectMode: "exclusive"}, Network: manifest.NetworkSpec{Mode: "outbound", Ports: []manifest.PortSpec{}},
	}}
	if err := a.envs.Save(environment); err != nil {
		return err
	}
	registered, err := a.states.Register(name)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "created environment %s\nmanifest: %s\nUUID: %s\nnext: cagent env plan %s\n", name, a.paths.EnvironmentManifest(name), registered.ID, name)
	return nil
}

type environmentListItem struct {
	Name      string `json:"name"`
	Profile   string `json:"profile,omitempty"`
	ID        string `json:"id,omitempty"`
	State     string `json:"state"`
	Container string `json:"container,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (a *Application) environmentList(ctx context.Context, args []string) error {
	flags := newFlagSet("env list")
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
	names, err := a.envs.List()
	if err != nil {
		return err
	}
	items := make([]environmentListItem, 0, len(names))
	podmanAvailable := a.runner.Available() == nil
	for _, name := range names {
		item := environmentListItem{Name: name, State: "DEFINED"}
		environment, loadErr := a.envs.Load(name)
		if loadErr != nil {
			item.Error = cleanErrorText(loadErr)
			items = append(items, item)
			continue
		}
		item.Profile = environment.Spec.Profile
		stored, registered, stateErr := a.states.GetByName(name)
		if stateErr != nil {
			item.Error = cleanErrorText(stateErr)
			items = append(items, item)
			continue
		}
		if !registered {
			items = append(items, item)
			continue
		}
		item.ID, item.Container = stored.ID, stored.ContainerName
		if !podmanAvailable {
			item.State = "UNKNOWN"
			items = append(items, item)
			continue
		}
		containerName := stored.ContainerName
		if containerName == "" {
			containerName = "ca2-" + name + "-" + strings.ReplaceAll(stored.ID[:8], "-", "")
		}
		inspect, exists, inspectErr := a.podman.InspectContainer(ctx, containerName)
		if inspectErr != nil {
			item.State, item.Error = "UNKNOWN", cleanErrorText(inspectErr)
		} else if !exists {
			item.State = "CONTAINER_ABSENT"
		} else if podman.VerifyEnvironment(inspect, stored.ID) != nil {
			item.State, item.Error = "BROKEN", "ownership labels do not match"
		} else if inspect.Running {
			item.State = "RUNNING"
		} else {
			item.State = "STOPPED"
		}
		items = append(items, item)
	}
	if *output == "json" {
		return writeJSON(a.stdout, items)
	}
	for _, item := range items {
		fmt.Fprintf(a.stdout, "%-28s %-22s %-18s %s\n", item.Name, item.Profile, item.State, item.Error)
	}
	return nil
}

func (a *Application) environmentShow(ctx context.Context, args []string) error {
	flags := newFlagSet("env show")
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
	environment, err := a.envs.Load(name)
	if err != nil {
		return err
	}
	resolved, err := a.profiles.Resolve(environment.Spec.Profile)
	if err != nil {
		return err
	}
	stored, registered, err := a.states.GetByName(name)
	if err != nil {
		return err
	}
	var actual any
	if registered && stored.ContainerName != "" && a.runner.Available() == nil {
		inspect, exists, inspectErr := a.podman.InspectContainer(ctx, stored.ContainerName)
		if inspectErr == nil && exists {
			actual = inspect
		}
	}
	value := map[string]any{"manifest": environment, "profile": map[string]any{"name": resolved.Manifest.Metadata.Name, "hash": resolved.Hash, "image": resolved.ImageReference}, "registered": registered, "state": stored, "actual": actual}
	if *output == "json" {
		return writeJSON(a.stdout, value)
	}
	fmt.Fprintf(a.stdout, "Environment: %s\nProfile: %s\nProfile hash: %s\nImage: %s\nRegistered: %t\n", name, resolved.Manifest.Metadata.Name, resolved.Hash, resolved.ImageReference, registered)
	if registered {
		fmt.Fprintf(a.stdout, "UUID: %s\nContainer: %s\nSecurity: %s\nResources: %s\n", stored.ID, stored.ContainerName, stored.SecurityClass, stored.ResourceClass)
	}
	if environment.Spec.Project != nil {
		fmt.Fprintf(a.stdout, "Project: %s -> %s (%s)\n", environment.Spec.Project.Source, environment.Spec.Project.Target, environment.Spec.Project.Mode)
	}
	fmt.Fprintf(a.stdout, "Secrets: %d named file mount(s); values are never stored in manifests or output\n", len(environment.Spec.Secrets))
	return nil
}

type planOptions struct {
	planner planner.Options
	confirm string
	output  string
}

func addPlanFlags(flags interface {
	String(string, string, string) *string
	Bool(string, bool, string) *bool
}) (*string, *bool, *bool, *bool, *string, *string) {
	resourceClass := flags.String("resource", "", "resource class override")
	dangerous := flags.Bool("allow-dangerous-mount", false, "allow paths outside allowed roots; hard denials remain")
	external := flags.Bool("allow-external-port", false, "allow non-loopback port publication")
	overcommit := flags.Bool("allow-overcommit", false, "allow aggregate memory overcommit")
	confirm := flags.String("confirm", "", "exact environment name for dangerous apply")
	output := flags.String("output", "human", "human or json")
	return resourceClass, dangerous, external, overcommit, confirm, output
}

func (a *Application) parsePlanCommand(name string, args []string) (string, planOptions, error) {
	flags := newFlagSet(name)
	resourceClass, dangerous, external, overcommit, confirm, output := addPlanFlags(flags)
	if err := parseFlags(flags, args); err != nil {
		return "", planOptions{}, err
	}
	if err := requireArgs(flags, 1, 1); err != nil {
		return "", planOptions{}, err
	}
	if err := ensureOutput(*output); err != nil {
		return "", planOptions{}, err
	}
	return flags.Arg(0), planOptions{planner: planner.Options{ResourceClass: *resourceClass, AllowDangerousMount: *dangerous, AllowExternalPort: *external, AllowOvercommit: *overcommit}, confirm: *confirm, output: *output}, nil
}

func (a *Application) buildEnvironmentPlan(ctx context.Context, name string, options planner.Options) (planner.Plan, error) {
	environment, err := a.envs.Load(name)
	if err != nil {
		return planner.Plan{}, err
	}
	resolved, err := a.profiles.Resolve(environment.Spec.Profile)
	if err != nil {
		return planner.Plan{}, err
	}
	runtime, err := a.requireRootless(ctx)
	if err != nil {
		return planner.Plan{}, err
	}
	stored, registered, err := a.states.GetByName(name)
	if err != nil {
		return planner.Plan{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return planner.Plan{}, err
	}
	builder := planner.Builder{Paths: a.paths, Podman: a.podman, Capabilities: runtime.Capabilities, Host: runtime.Host, Defaults: runtime.Defaults, UID: currentUID(), Home: home}
	return builder.Build(ctx, environment, resolved, stored, registered, options)
}

func (a *Application) environmentPlan(ctx context.Context, args []string) error {
	name, options, err := a.parsePlanCommand("env plan", args)
	if err != nil {
		return err
	}
	plan, err := a.buildEnvironmentPlan(ctx, name, options.planner)
	if err != nil {
		return err
	}
	if options.output == "json" {
		return writeJSON(a.stdout, plan)
	}
	printPlan(a.stdout, plan)
	return nil
}

func printPlan(writer interface{ Write([]byte) (int, error) }, plan planner.Plan) {
	fmt.Fprintf(writer, "Environment: %s (%s)\nCurrent state: %s\nDesired state: %s\nProfile: %s\nImage: %s\nSpec hash: %s\nResources: %s memory, %s memory+swap, %.2f CPUs, %d PIDs\n", plan.Environment, plan.EnvironmentID, plan.CurrentState, plan.DesiredState, plan.Profile, plan.ImageReference, plan.SpecHash, humanBytes(plan.Resources.MemoryBytes), humanBytes(plan.Resources.SwapBytes), plan.Resources.CPUs, plan.Resources.PIDs)
	if len(plan.Actions) == 0 {
		fmt.Fprintln(writer, "Actions: none; environment is current")
	} else {
		fmt.Fprintln(writer, "Actions:")
		for _, action := range plan.Actions {
			marker := ""
			if action.Destructive {
				marker = " [DESTRUCTIVE]"
			}
			fmt.Fprintf(writer, "  - %s: %s%s\n", action.Type, action.Description, marker)
		}
	}
	if len(plan.Mounts) > 0 {
		fmt.Fprintln(writer, "Mounts:")
		for _, mount := range plan.Mounts {
			fmt.Fprintf(writer, "  - %s -> %s (%s, %s)\n", mount.Source, mount.Target, mount.Mode, mount.Kind)
		}
	}
	for _, warning := range plan.Warnings {
		fmt.Fprintf(writer, "WARNING: %s\n", warning)
	}
	for _, exception := range plan.PolicyExceptions {
		fmt.Fprintf(writer, "POLICY EXCEPTION: %s\n", exception)
	}
}

func snapshotPretty(raw json.RawMessage) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return string(raw)
	}
	data, _ := json.MarshalIndent(value, "", "  ")
	return string(data)
}
