package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"containersagents.dev/v2/internal/audit"
	"containersagents.dev/v2/internal/fsutil"
	"containersagents.dev/v2/internal/locks"
	"containersagents.dev/v2/internal/manifest"
	"containersagents.dev/v2/internal/planner"
	"containersagents.dev/v2/internal/podman"
	profilepkg "containersagents.dev/v2/internal/profile"
	"containersagents.dev/v2/internal/state"
)

type activeSession struct {
	application *Application
	plan        planner.Plan
	profile     profilepkg.Resolved
	state       state.EnvironmentState
	environment manifest.Environment
	locks       []locks.Lock
	started     bool
	defaults    manifest.Defaults
}

func (s *activeSession) release() {
	for index := len(s.locks) - 1; index >= 0; index-- {
		_ = s.locks[index].Release()
	}
	s.locks = nil
}

func (a *Application) prepareActiveSession(ctx context.Context, name string, options planOptions, start bool, applying bool) (_ *activeSession, err error) {
	if applying && (options.planner.AllowDangerousMount || options.planner.AllowExternalPort || options.planner.AllowOvercommit) {
		if confirmErr := confirmExact(options.confirm, name, "dangerous policy apply"); confirmErr != nil {
			return nil, confirmErr
		}
	}
	if err := a.paths.Ensure(); err != nil {
		return nil, err
	}
	global, err := locks.Acquire(a.paths.GlobalLock())
	if err != nil {
		return nil, err
	}
	owned := []locks.Lock{global}
	releaseOnError := func() {
		for index := len(owned) - 1; index >= 0; index-- {
			_ = owned[index].Release()
		}
	}
	defer func() {
		if err != nil {
			releaseOnError()
		}
	}()
	environment, err := a.envs.Load(name)
	if err != nil {
		return nil, err
	}
	resolved, err := a.profiles.Resolve(environment.Spec.Profile)
	if err != nil {
		return nil, err
	}
	stored, registered, err := a.states.GetByName(name)
	if err != nil {
		return nil, err
	}
	if !registered {
		stored, err = a.states.Register(name)
		if err != nil {
			return nil, err
		}
	}
	environmentLock, err := locks.Acquire(a.paths.EnvironmentLock(stored.ID))
	if err != nil {
		return nil, err
	}
	owned = append(owned, environmentLock)
	plan, err := a.buildEnvironmentPlan(ctx, name, options.planner)
	if err != nil {
		return nil, err
	}
	effectiveEnvironment := plan.EffectiveManifest
	var projectLock locks.Lock
	if plan.ProjectHash != "" && effectiveEnvironment.Spec.Concurrency.ProjectMode != "allow" {
		acquiredProjectLock, lockErr := locks.Acquire(a.paths.ProjectLock(plan.ProjectHash))
		if lockErr != nil {
			return nil, lockErr
		}
		projectLock = acquiredProjectLock
		owned = append(owned, projectLock)
		if err := a.enforceProjectConcurrency(ctx, plan, effectiveEnvironment); err != nil {
			return nil, err
		}
	}
	wasRunning := plan.ContainerRunning
	if applying {
		stored, err = a.applyPlan(ctx, plan, resolved, stored, start)
		if err != nil {
			return nil, err
		}
	}
	// The global lock serializes the concurrency check and start operation. Once
	// Podman exposes the new running state, later sessions can enforce policy
	// without an exclusive project lock blocking read-only-secondary sessions.
	if projectLock != nil {
		_ = projectLock.Release()
		owned = owned[:len(owned)-1]
	}
	_ = global.Release()
	owned = owned[1:]
	runtime, runtimeErr := a.discoverRuntime(ctx, false)
	if runtimeErr != nil {
		return nil, runtimeErr
	}
	return &activeSession{application: a, plan: plan, profile: resolved, state: stored, environment: effectiveEnvironment, locks: owned, started: start && !wasRunning, defaults: runtime.Defaults}, nil
}

func (a *Application) enforceProjectConcurrency(ctx context.Context, plan planner.Plan, environment manifest.Environment) error {
	containers, err := a.podman.ListManagedContainers(ctx, false)
	if err != nil {
		return err
	}
	for _, container := range containers {
		if container.Labels[podman.EnvironmentIDLabel] == plan.EnvironmentID || container.Labels[podman.ProjectHashLabel] != plan.ProjectHash {
			continue
		}
		mode := environment.Spec.Concurrency.ProjectMode
		switch mode {
		case "exclusive":
			return policyError("project is already used by running V2 environment %q; exclusive mode blocks another writer", container.Name)
		case "read-only-secondary":
			if environment.Spec.Project != nil && environment.Spec.Project.Mode != "read-only" {
				return policyError("project already has a running environment; read-only-secondary requires this environment's project mount to be read-only")
			}
		case "prompt":
			return policyError("project is already in use; prompt mode requires an interactive authorization workflow, so this non-prompting operation is rejected")
		}
	}
	return nil
}

func (a *Application) applyPlan(ctx context.Context, plan planner.Plan, resolved profilepkg.Resolved, stored state.EnvironmentState, start bool) (state.EnvironmentState, error) {
	if plan.CurrentState == "DRIFTED" {
		return stored, policyError("environment %q is drifted; review 'cagent env diff %s' and use explicit recreate", plan.Environment, plan.Environment)
	}
	if !plan.ImageExists {
		switch resolved.Manifest.Spec.Image.Mode {
		case "build":
			if len(resolved.Manifest.Spec.Image.BuildSecrets) > 0 {
				return stored, policyError("buildSecrets require an approved external provider, which is not configured")
			}
			contextPath, containerfile, err := a.profiles.Materialize(resolved)
			if err != nil {
				return stored, err
			}
			report, err := profilepkg.InspectContext(contextPath)
			if err != nil {
				return stored, err
			}
			if err := validateBuildContext(contextPath, report); err != nil {
				return stored, err
			}
			if _, err := a.podman.Build(ctx, profileBuildArgs(resolved, containerfile, contextPath, plan.Resources.BuildJobs)); err != nil {
				return stored, err
			}
		case "pull":
			if err := a.podman.Pull(ctx, resolved.ImageReference, resolved.Manifest.Spec.Image.PullPolicy); err != nil {
				return stored, err
			}
		case "existing":
			return stored, notFound("required existing image %q is unavailable", resolved.ImageReference)
		}
	}
	for _, secret := range plan.EffectiveManifest.Spec.Secrets {
		exists, err := a.podman.SecretExists(ctx, secret.Name)
		if err != nil {
			return stored, err
		}
		if !exists {
			return stored, notFound("Podman secret %q does not exist", secret.Name)
		}
	}
	if plan.NetworkName != "" {
		exists, err := a.podman.NetworkExists(ctx, plan.NetworkName)
		if err != nil {
			return stored, err
		}
		if exists {
			network, _, inspectErr := a.podman.InspectNetwork(ctx, plan.NetworkName)
			if inspectErr != nil {
				return stored, inspectErr
			}
			if network.Labels[podman.ManagedLabel] != "true" || network.Labels[podman.EnvironmentIDLabel] != plan.EnvironmentID {
				return stored, policyError("network name %q is occupied by a resource not owned by this V2 environment", plan.NetworkName)
			}
		} else {
			if err := a.podman.CreateNetwork(ctx, planner.NetworkCreateArgs(plan)); err != nil {
				return stored, err
			}
		}
	}
	if plan.Security.HomeAllowed && plan.EffectiveManifest.Spec.Home.Persistence == "per-environment" {
		if err := fsutil.EnsureDir(a.paths.EnvironmentHome(plan.EnvironmentID), 0700); err != nil {
			return stored, err
		}
	}
	if plan.CertificateBundle != "" {
		if err := a.prepareCertificateBundle(ctx, plan, resolved); err != nil {
			return stored, err
		}
	}
	if !plan.ContainerExists {
		containerID, err := a.podman.Create(ctx, plan.CreateArgs)
		if err != nil {
			return stored, err
		}
		stored.ContainerID = containerID
		stored.ContainerName = plan.ContainerName
		stored.NetworkName = plan.NetworkName
	}
	if start && !plan.ContainerRunning {
		if err := a.podman.Start(ctx, plan.ContainerName); err != nil {
			return stored, err
		}
	}
	stored.ProfileHash = plan.ProfileHash
	stored.SpecHash = plan.SpecHash
	stored.ProjectHash = plan.ProjectHash
	stored.ResourceClass = plan.Resources.Class
	stored.SecurityClass = plan.Security.Class
	stored.MemoryBytes = plan.Resources.MemoryBytes
	stored.AppliedSnapshot = plan.Snapshot
	if err := a.states.Save(stored); err != nil {
		return stored, err
	}
	a.appendAudit(stored, plan, "apply", "success", "", "")
	return stored, nil
}

func (a *Application) prepareCertificateBundle(ctx context.Context, plan planner.Plan, resolved profilepkg.Resolved) error {
	if resolved.Manifest.Spec.Runtime.CABundle == "" {
		return policyError("profile %q does not declare runtime.caBundle, so custom certificate injection is unavailable", resolved.Manifest.Metadata.Name)
	}
	result, err := a.runner.Run(ctx, "run", "--rm", "--network", "none", "--read-only", "--cap-drop=all", "--security-opt=no-new-privileges", "--label", podman.ManagedLabel+"=true", "--label", podman.EnvironmentIDLabel+"="+plan.EnvironmentID, "--entrypoint", "cat", resolved.ImageReference, resolved.Manifest.Spec.Runtime.CABundle)
	if err != nil {
		return fmt.Errorf("extract profile CA bundle: %w", err)
	}
	var bundle bytes.Buffer
	bundle.WriteString(result.Stdout)
	if bundle.Len() > 0 && !strings.HasSuffix(bundle.String(), "\n") {
		bundle.WriteByte('\n')
	}
	for _, certificate := range plan.EffectiveManifest.Spec.Certificates {
		data, readErr := os.ReadFile(certificate.Source)
		if readErr != nil {
			return readErr
		}
		text := string(data)
		if strings.Contains(text, "PRIVATE KEY") {
			return policyError("certificate source %q contains a private key", certificate.Source)
		}
		if !strings.Contains(text, "BEGIN CERTIFICATE") {
			return policyError("certificate source %q is not a PEM certificate", certificate.Source)
		}
		bundle.Write(data)
		if !bytes.HasSuffix(data, []byte("\n")) {
			bundle.WriteByte('\n')
		}
	}
	return fsutil.AtomicWrite(plan.CertificateBundle, bundle.Bytes(), 0600)
}

func (a *Application) appendAudit(stored state.EnvironmentState, plan planner.Plan, command, result, errorText, confirmation string) {
	actions := make([]string, 0, len(plan.Actions))
	for _, action := range plan.Actions {
		actions = append(actions, action.Type)
	}
	max := int64(10 * 1024 * 1024)
	if defaults, err := manifest.LoadDefaults(a.paths.DefaultsFile()); err == nil {
		max = int64(defaults.Spec.AuditMaxMiB) * 1024 * 1024
	}
	logger := audit.Logger{MaxBytes: max}
	_ = logger.Append(a.paths.EnvironmentAuditFile(stored.ID), audit.Record{Command: command, EnvironmentID: stored.ID, EnvironmentName: stored.Name, PlannedActions: actions, Result: result, Error: errorText, ProfileHash: plan.ProfileHash, SpecHash: plan.SpecHash, ResourceClass: plan.Resources.Class, SecurityClass: plan.Security.Class, PodmanResourceIDs: nonEmpty(stored.ContainerID), DestructiveConfirm: confirmation, PolicyExceptions: plan.PolicyExceptions})
}

func nonEmpty(values ...string) []string {
	result := []string{}
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func (a *Application) environmentPrepare(ctx context.Context, args []string) error {
	name, options, err := a.parsePlanCommand("env prepare", args)
	if err != nil {
		return err
	}
	session, err := a.prepareActiveSession(ctx, name, options, false, true)
	if err != nil {
		return err
	}
	defer session.release()
	fmt.Fprintf(a.stdout, "environment prepared: %s\n", name)
	return nil
}
func (a *Application) environmentStart(ctx context.Context, args []string) error {
	name, options, err := a.parsePlanCommand("env start", args)
	if err != nil {
		return err
	}
	session, err := a.prepareActiveSession(ctx, name, options, true, true)
	if err != nil {
		return err
	}
	defer session.release()
	fmt.Fprintf(a.stdout, "environment running: %s (%s)\n", name, session.plan.ContainerName)
	return nil
}

func (a *Application) environmentShell(ctx context.Context, args []string) error {
	name, options, err := a.parsePlanCommand("env shell", args)
	if err != nil {
		return err
	}
	session, err := a.prepareActiveSession(ctx, name, options, true, true)
	if err != nil {
		return err
	}
	defer session.release()
	execErr := a.podman.ExecInteractive(ctx, a.stdin, a.stdout, a.stderr, session.plan.ContainerName, session.profile.Manifest.Spec.Runtime.Shell)
	cleanupErr := session.cleanupAfterOwnedSession()
	if execErr != nil {
		return execErr
	}
	return cleanupErr
}

func (a *Application) environmentExec(ctx context.Context, args []string) error {
	before, command := splitCommand(args)
	flags := newFlagSet("env exec")
	resource, dangerous, external, overcommit, confirm, _ := addPlanFlags(flags)
	tty := flags.Bool("tty", false, "allocate an interactive TTY")
	if err := parseFlags(flags, before); err != nil {
		return err
	}
	if err := requireArgs(flags, 1, 1); err != nil {
		return err
	}
	name := flags.Arg(0)
	options := planOptions{planner: planner.Options{ResourceClass: *resource, AllowDangerousMount: *dangerous, AllowExternalPort: *external, AllowOvercommit: *overcommit}, confirm: *confirm}
	session, err := a.prepareActiveSession(ctx, name, options, true, true)
	if err != nil {
		return err
	}
	defer session.release()
	if len(command) == 0 {
		command = session.profile.Manifest.Spec.Runtime.Shell
		*tty = true
	}
	if *tty {
		err = a.podman.ExecInteractive(ctx, a.stdin, a.stdout, a.stderr, session.plan.ContainerName, command)
	} else {
		var result podman.Result
		result, err = a.podman.Exec(ctx, session.plan.ContainerName, command)
		fmt.Fprint(a.stdout, result.Stdout)
		fmt.Fprint(a.stderr, result.Stderr)
	}
	cleanupErr := session.cleanupAfterOwnedSession()
	if err != nil {
		return err
	}
	return cleanupErr
}

func (s *activeSession) cleanupAfterOwnedSession() error {
	if !s.started {
		return nil
	}
	stopOnExit := s.defaults.Spec.StopOnShellExit == nil || *s.defaults.Spec.StopOnShellExit
	if !stopOnExit {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := s.application.podman.Stop(cleanupCtx, s.plan.ContainerName, 10); err != nil {
		return fmt.Errorf("session ended but container stop failed: %w", err)
	}
	if s.environment.Spec.RootFS.Persistence == "ephemeral" {
		if err := s.application.podman.RemoveContainer(cleanupCtx, s.plan.ContainerName); err != nil {
			return err
		}
		s.state.ContainerID = ""
		s.state.ContainerName = ""
		_ = s.application.states.Save(s.state)
	}
	return nil
}

func (a *Application) environmentStop(ctx context.Context, args []string) error {
	flags := newFlagSet("env stop")
	all := flags.Bool("all", false, "stop all running V2 containers")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if *all {
		if err := requireArgs(flags, 0, 0); err != nil {
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
		containers, err := a.podman.ListManagedContainers(ctx, false)
		if err != nil {
			return err
		}
		for _, container := range containers {
			inspect, exists, inspectErr := a.podman.InspectContainer(ctx, container.ID)
			if inspectErr != nil {
				return inspectErr
			}
			if !exists || inspect.Labels[podman.ManagedLabel] != "true" {
				continue
			}
			if err := a.podman.Stop(ctx, container.ID, 10); err != nil {
				return err
			}
			fmt.Fprintf(a.stdout, "stopped %s\n", container.Name)
		}
		return nil
	}
	if err := requireArgs(flags, 1, 1); err != nil {
		return err
	}
	name := flags.Arg(0)
	if err := a.paths.Ensure(); err != nil {
		return err
	}
	global, err := locks.Acquire(a.paths.GlobalLock())
	if err != nil {
		return err
	}
	defer global.Release()
	stored, registered, err := a.states.GetByName(name)
	if err != nil {
		return err
	}
	if !registered {
		return notFound("environment %q is not registered", name)
	}
	lock, err := locks.Acquire(a.paths.EnvironmentLock(stored.ID))
	if err != nil {
		return err
	}
	defer lock.Release()
	environment, err := a.envs.Load(name)
	if err != nil {
		return err
	}
	resolved, err := a.profiles.Resolve(environment.Spec.Profile)
	if err != nil {
		return err
	}
	defaults, err := manifest.LoadDefaults(a.paths.DefaultsFile())
	if err != nil {
		return err
	}
	manifest.ApplyEnvironmentDefaults(&environment, resolved.Manifest, defaults)
	if stored.ContainerName == "" {
		fmt.Fprintf(a.stdout, "environment %s has no container\n", name)
		return nil
	}
	inspect, exists, err := a.podman.InspectContainer(ctx, stored.ContainerName)
	if err != nil {
		return err
	}
	if !exists {
		fmt.Fprintf(a.stdout, "environment %s has no container\n", name)
		return nil
	}
	if err := podman.VerifyEnvironment(inspect, stored.ID); err != nil {
		return err
	}
	if inspect.Running {
		if err := a.podman.Stop(ctx, stored.ContainerName, 10); err != nil {
			return err
		}
	}
	if environment.Spec.RootFS.Persistence == "ephemeral" {
		if err := a.podman.RemoveContainer(ctx, stored.ContainerName); err != nil {
			return err
		}
		stored.ContainerID, stored.ContainerName = "", ""
		if err := a.states.Save(stored); err != nil {
			return err
		}
	}
	fmt.Fprintf(a.stdout, "environment stopped: %s\n", name)
	return nil
}

func (a *Application) environmentRecreate(ctx context.Context, args []string) error {
	name, options, err := a.parsePlanCommand("env recreate", args)
	if err != nil {
		return err
	}
	if err := confirmExact(options.confirm, name, "environment recreation"); err != nil {
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
	stored, registered, err := a.states.GetByName(name)
	if err != nil {
		return err
	}
	if !registered {
		return notFound("environment %q is not registered", name)
	}
	envLock, err := locks.Acquire(a.paths.EnvironmentLock(stored.ID))
	if err != nil {
		return err
	}
	defer envLock.Release()
	plan, err := a.buildEnvironmentPlan(ctx, name, options.planner)
	if err != nil {
		return err
	}
	printPlan(a.stdout, plan)
	wasRunning := plan.ContainerRunning
	if plan.ContainerExists {
		inspect, exists, inspectErr := a.podman.InspectContainer(ctx, plan.ContainerName)
		if inspectErr != nil {
			return inspectErr
		}
		if exists {
			if err := podman.VerifyEnvironment(inspect, stored.ID); err != nil {
				return err
			}
			if inspect.Running {
				if err := a.podman.Stop(ctx, plan.ContainerName, 10); err != nil {
					return err
				}
			}
			if err := a.podman.RemoveContainer(ctx, plan.ContainerName); err != nil {
				return err
			}
		}
	}
	stored.ContainerID = ""
	stored.ContainerName = ""
	if err := a.states.Save(stored); err != nil {
		return err
	}
	plan, err = a.buildEnvironmentPlan(ctx, name, options.planner)
	if err != nil {
		return err
	}
	resolved, err := a.profiles.Resolve(plan.Profile)
	if err != nil {
		return err
	}
	stored, err = a.applyPlan(ctx, plan, resolved, stored, wasRunning)
	if err != nil {
		return err
	}
	a.appendAudit(stored, plan, "recreate", "success", "", name)
	fmt.Fprintf(a.stdout, "recreated %s; persistent home retained at %s\n", name, a.paths.EnvironmentHome(stored.ID))
	return nil
}

func (a *Application) environmentDiff(ctx context.Context, args []string) error {
	name, options, err := a.parsePlanCommand("env diff", args)
	if err != nil {
		return err
	}
	plan, err := a.buildEnvironmentPlan(ctx, name, options.planner)
	if err != nil {
		return err
	}
	result := map[string]any{"environment": name, "state": plan.CurrentState, "drift": plan.DriftReasons, "desiredSpecHash": plan.SpecHash}
	if options.output == "json" {
		return writeJSON(a.stdout, result)
	}
	if len(plan.DriftReasons) == 0 {
		fmt.Fprintln(a.stdout, "no drift detected")
		return nil
	}
	for _, reason := range plan.DriftReasons {
		fmt.Fprintf(a.stdout, "- %s\n", reason)
	}
	return nil
}

func (a *Application) environmentDoctor(ctx context.Context, args []string) error {
	name, options, err := a.parsePlanCommand("env doctor", args)
	if err != nil {
		return err
	}
	session, err := a.prepareActiveSession(ctx, name, options, true, true)
	if err != nil {
		return err
	}
	defer session.release()
	defer session.cleanupAfterOwnedSession()
	type checkResult struct {
		Name    string `json:"name"`
		Success bool   `json:"success"`
		Output  string `json:"output,omitempty"`
		Error   string `json:"error,omitempty"`
	}
	results := []checkResult{}
	for _, check := range session.profile.Manifest.Spec.Checks {
		checkCtx, cancel := context.WithTimeout(ctx, time.Duration(check.TimeoutSeconds)*time.Second)
		result, checkErr := a.podman.Exec(checkCtx, session.plan.ContainerName, check.Command)
		cancel()
		item := checkResult{Name: check.Name, Success: checkErr == nil, Output: strings.TrimSpace(result.Stdout)}
		if checkErr != nil {
			item.Error = cleanErrorText(checkErr)
		}
		results = append(results, item)
	}
	if options.output == "json" {
		return writeJSON(a.stdout, results)
	}
	failed := false
	for _, item := range results {
		status := "PASS"
		if !item.Success {
			status = "FAIL"
			failed = true
		}
		fmt.Fprintf(a.stdout, "%-5s %s %s\n", status, item.Name, item.Output)
	}
	if failed {
		return &CodedError{Code: 1, Err: fmt.Errorf("one or more profile checks failed")}
	}
	return nil
}

func sortedActionNames(plan planner.Plan) []string {
	values := []string{}
	for _, action := range plan.Actions {
		values = append(values, action.Type)
	}
	sort.Strings(values)
	return values
}
func rawSnapshot(value any) json.RawMessage { data, _ := json.Marshal(value); return data }
