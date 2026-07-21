package planner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"containersagents.dev/v2/internal/capability"
	"containersagents.dev/v2/internal/hashspec"
	"containersagents.dev/v2/internal/hostinfo"
	"containersagents.dev/v2/internal/manifest"
	"containersagents.dev/v2/internal/podman"
	"containersagents.dev/v2/internal/policy"
	"containersagents.dev/v2/internal/profile"
	"containersagents.dev/v2/internal/resources"
	"containersagents.dev/v2/internal/security"
	"containersagents.dev/v2/internal/state"
)

type Options struct {
	AllowDangerousMount bool
	AllowExternalPort   bool
	AllowOvercommit     bool
	ResourceClass       string
}

type Action struct {
	Type        string `json:"type"`
	Resource    string `json:"resource"`
	Description string `json:"description"`
	Destructive bool   `json:"destructive,omitempty"`
}

type Mount struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Mode   string `json:"mode"`
	Kind   string `json:"kind"`
}

type Plan struct {
	Environment       string               `json:"environment"`
	EnvironmentID     string               `json:"environmentId"`
	Profile           string               `json:"profile"`
	ProfileHash       string               `json:"profileHash"`
	SpecHash          string               `json:"specHash"`
	ProjectHash       string               `json:"projectHash,omitempty"`
	ImageReference    string               `json:"imageReference"`
	ContainerName     string               `json:"containerName"`
	NetworkName       string               `json:"networkName,omitempty"`
	CertificateBundle string               `json:"certificateBundle,omitempty"`
	CurrentState      string               `json:"currentState"`
	DesiredState      string               `json:"desiredState"`
	Actions           []Action             `json:"actions"`
	Warnings          []string             `json:"warnings,omitempty"`
	PolicyExceptions  []string             `json:"policyExceptions,omitempty"`
	Mounts            []Mount              `json:"mounts,omitempty"`
	Ports             []manifest.PortSpec  `json:"ports,omitempty"`
	Secrets           []string             `json:"secrets,omitempty"`
	Resources         resources.Budget     `json:"resources"`
	Security          policy.SecurityPlan  `json:"security"`
	BuildArgs         []string             `json:"-"`
	CreateArgs        []string             `json:"-"`
	EffectiveManifest manifest.Environment `json:"effectiveManifest"`
	Snapshot          json.RawMessage      `json:"-"`
	Registered        bool                 `json:"registered"`
	ContainerExists   bool                 `json:"containerExists"`
	ContainerRunning  bool                 `json:"containerRunning"`
	ImageExists       bool                 `json:"imageExists"`
	DriftReasons      []string             `json:"driftReasons,omitempty"`
}

type Builder struct {
	Paths        state.Paths
	Podman       podman.Adapter
	Capabilities capability.Report
	Host         hostinfo.Resources
	Defaults     manifest.Defaults
	UID          int
	Home         string
}

func (b Builder) Build(ctx context.Context, environment manifest.Environment, resolved profile.Resolved, environmentState state.EnvironmentState, registered bool, options Options) (Plan, error) {
	effective := environment
	manifest.ApplyEnvironmentDefaults(&effective, resolved.Manifest, b.Defaults)
	if options.ResourceClass != "" {
		effective.Spec.ResourceClass = options.ResourceClass
	}
	if err := manifest.ValidateEnvironment(effective); err != nil {
		return Plan{}, err
	}
	if !b.Capabilities.Rootless {
		return Plan{}, fmt.Errorf("V2 requires rootless Podman; current Podman reports rootless=false")
	}
	if b.Defaults.Spec.StrictResources && !b.Capabilities.ResourceLimits {
		return Plan{}, fmt.Errorf("strict resource policy requires enforceable rootless resource limits (normally cgroups v2)")
	}
	securityPlan, err := policy.CompileSecurity(effective, resolved.Manifest)
	if err != nil {
		return Plan{}, err
	}
	budget, err := resources.Calculate(effective.Spec.ResourceClass, effective.Spec.Resources, resolved.Manifest.Spec.MinimumResources, b.Host)
	if err != nil {
		return Plan{}, err
	}
	runningMemory, err := b.Podman.RunningManagedMemory(ctx)
	if err != nil {
		return Plan{}, fmt.Errorf("calculate aggregate V2 memory: %w", err)
	}
	if registered {
		currentName := containerName(effective.Metadata.Name, environmentState.ID)
		current, exists, inspectErr := b.Podman.InspectContainer(ctx, currentName)
		if inspectErr != nil {
			return Plan{}, inspectErr
		}
		if exists && current.Running && podman.VerifyEnvironment(current, environmentState.ID) == nil {
			runningMemory -= current.Memory
			if runningMemory < 0 {
				runningMemory = 0
			}
		}
	}
	if err := resources.CheckAggregate(budget.AggregatePool, runningMemory, budget.MemoryBytes); err != nil {
		if !options.AllowOvercommit {
			return Plan{}, err
		}
	}
	plan := Plan{Environment: effective.Metadata.Name, Profile: resolved.Manifest.Metadata.Name, ProfileHash: resolved.Hash, ImageReference: resolved.ImageReference, DesiredState: "RUNNING", EffectiveManifest: effective, Resources: budget, Security: securityPlan, Registered: registered, Ports: effective.Spec.Network.Ports}
	if resolved.Manifest.Spec.Runtime.IdentityMode == "rootless-container-root" {
		plan.Warnings = append(plan.Warnings, "profile runs as container root inside the rootless user namespace; this is not host root but has greater container-local authority")
	}
	if options.AllowOvercommit && runningMemory+budget.MemoryBytes > budget.AggregatePool {
		plan.Warnings = append(plan.Warnings, "aggregate memory overcommit explicitly allowed")
	}
	if registered {
		plan.EnvironmentID = environmentState.ID
	} else {
		plan.EnvironmentID = "<registration-required>"
		plan.Actions = append(plan.Actions, Action{Type: "register", Resource: "state", Description: "assign an environment UUID and create private state directories"})
	}
	if registered {
		plan.ContainerName = containerName(effective.Metadata.Name, environmentState.ID)
		if securityPlan.NetworkMode == "internal" {
			plan.NetworkName = "ca2-net-" + strings.ReplaceAll(environmentState.ID[:13], "-", "")
		}
	}
	if registered && securityPlan.HomeAllowed && effective.Spec.Home.Persistence == "per-environment" && strings.Contains(b.Paths.EnvironmentHome(environmentState.ID), ",") {
		return Plan{}, fmt.Errorf("generated environment home path contains a comma delimiter unsupported by Podman --mount")
	}
	validator := security.MountValidator{Paths: b.Paths, Home: b.Home, AllowedRoots: b.Defaults.Spec.AllowedMountRoots, UID: b.UID}
	targets := map[string]string{}
	if resolved.Manifest.Spec.Runtime.Home != "" {
		targets[filepath.Clean(resolved.Manifest.Spec.Runtime.Home)] = "environment home"
	}
	if effective.Spec.Project != nil {
		if targetErr := addContainerTarget(targets, effective.Spec.Project.Target, "project"); targetErr != nil {
			return Plan{}, targetErr
		}
		validated, validateErr := validator.Validate(effective.Spec.Project.Source, options.AllowDangerousMount)
		if validateErr != nil {
			return Plan{}, fmt.Errorf("project mount: %w", validateErr)
		}
		effective.Spec.Project.Source = validated.Source
		plan.EffectiveManifest.Spec.Project.Source = validated.Source
		plan.Warnings = append(plan.Warnings, validated.Warnings...)
		mode := effective.Spec.Project.Mode
		if securityPlan.ProjectReadOnly {
			mode = "read-only"
		}
		plan.Mounts = append(plan.Mounts, Mount{Source: validated.Source, Target: effective.Spec.Project.Target, Mode: mode, Kind: "project"})
		plan.ProjectHash = pathHash(validated.Source)
	}
	for index, declared := range effective.Spec.Mounts {
		if targetErr := addContainerTarget(targets, declared.Target, "data mount"); targetErr != nil {
			return Plan{}, targetErr
		}
		validated, validateErr := validator.Validate(declared.Source, options.AllowDangerousMount)
		if validateErr != nil {
			return Plan{}, fmt.Errorf("mount to %s: %w", declared.Target, validateErr)
		}
		effective.Spec.Mounts[index].Source = validated.Source
		plan.EffectiveManifest.Spec.Mounts[index].Source = validated.Source
		plan.Warnings = append(plan.Warnings, validated.Warnings...)
		plan.Mounts = append(plan.Mounts, Mount{Source: validated.Source, Target: declared.Target, Mode: declared.Mode, Kind: "data"})
	}
	certificateHashes := map[string]string{}
	for index, certificate := range effective.Spec.Certificates {
		validated, validateErr := validator.Validate(certificate.Source, options.AllowDangerousMount)
		if validateErr != nil {
			return Plan{}, fmt.Errorf("certificate %q: %w", certificate.Source, validateErr)
		}
		effective.Spec.Certificates[index].Source = validated.Source
		plan.EffectiveManifest.Spec.Certificates[index].Source = validated.Source
		info, statErr := os.Stat(validated.Source)
		if statErr != nil || !info.Mode().IsRegular() {
			return Plan{}, fmt.Errorf("certificate source %q must be a regular file", validated.Source)
		}
		plan.Warnings = append(plan.Warnings, validated.Warnings...)
		data, readErr := os.ReadFile(validated.Source)
		if readErr != nil {
			return Plan{}, fmt.Errorf("read certificate %q: %w", validated.Source, readErr)
		}
		digest := sha256.Sum256(data)
		certificateHashes[validated.Source] = hex.EncodeToString(digest[:])
	}
	if registered && len(effective.Spec.Certificates) > 0 {
		plan.CertificateBundle = filepath.Join(b.Paths.EnvironmentData(environmentState.ID), "certificates", "ca-bundle.pem")
		if strings.Contains(plan.CertificateBundle, ",") {
			return Plan{}, fmt.Errorf("generated certificate bundle path %q contains a comma delimiter unsupported by Podman --mount", plan.CertificateBundle)
		}
		bundleTarget := "/run/containersagents/ca-bundle.pem"
		if existing, duplicate := targets[bundleTarget]; duplicate {
			return Plan{}, fmt.Errorf("certificate bundle target %q collides with %s", bundleTarget, existing)
		}
		targets[bundleTarget] = "certificate bundle"
		plan.Mounts = append(plan.Mounts, Mount{Source: plan.CertificateBundle, Target: "/run/containersagents/ca-bundle.pem", Mode: "read-only", Kind: "certificate-bundle"})
	}
	for _, port := range effective.Spec.Network.Ports {
		if effective.Spec.Network.Mode != "integration" {
			return Plan{}, fmt.Errorf("published ports require integration network mode")
		}
		ip := net.ParseIP(port.HostIP)
		if ip == nil {
			return Plan{}, fmt.Errorf("invalid host IP %q", port.HostIP)
		}
		if !ip.IsLoopback() {
			if !options.AllowExternalPort {
				return Plan{}, fmt.Errorf("external port binding %s:%d requires --allow-external-port and exact-name confirmation", port.HostIP, port.HostPort)
			}
			plan.PolicyExceptions = append(plan.PolicyExceptions, fmt.Sprintf("external port %s:%d", port.HostIP, port.HostPort))
		}
	}
	for _, secret := range effective.Spec.Secrets {
		if existing, duplicate := targets[filepath.Clean(secret.Target)]; duplicate {
			return Plan{}, fmt.Errorf("secret target %q collides with %s", secret.Target, existing)
		}
		targets[filepath.Clean(secret.Target)] = "secret"
		plan.Secrets = append(plan.Secrets, secret.Name)
	}
	sort.Strings(plan.Secrets)
	plan.PolicyExceptions = append(plan.PolicyExceptions, securityPlan.PolicyExceptions...)
	if options.AllowDangerousMount {
		plan.PolicyExceptions = append(plan.PolicyExceptions, "dangerous mount override")
	}
	snapshotObject := map[string]any{"environment": plan.EffectiveManifest, "profileHash": resolved.Hash, "resources": budget, "security": securityPlan, "projectHash": plan.ProjectHash, "certificateHashes": certificateHashes}
	plan.SpecHash, err = hashspec.Value(snapshotObject)
	if err != nil {
		return Plan{}, err
	}
	plan.Snapshot, err = json.Marshal(snapshotObject)
	if err != nil {
		return Plan{}, err
	}
	imageExists, err := b.Podman.ImageExists(ctx, resolved.ImageReference)
	if err != nil {
		return Plan{}, err
	}
	plan.ImageExists = imageExists
	if !imageExists {
		actionType := "pull-image"
		if resolved.Manifest.Spec.Image.Mode == "build" {
			actionType = "build-image"
		}
		if resolved.Manifest.Spec.Image.Mode == "existing" {
			return Plan{}, fmt.Errorf("profile requires existing image %q, but it is unavailable", resolved.ImageReference)
		}
		plan.Actions = append(plan.Actions, Action{Type: actionType, Resource: resolved.ImageReference, Description: "make the profile image available"})
	}
	if !registered {
		plan.CurrentState = "DEFINED"
		plan.Actions = append(plan.Actions, Action{Type: "create-container", Resource: "pending UUID", Description: "create a V2-labeled environment container"}, Action{Type: "start-container", Resource: "pending UUID", Description: "start the environment container"})
		return plan, nil
	}
	inspect, exists, err := b.Podman.InspectContainer(ctx, plan.ContainerName)
	if err != nil {
		return Plan{}, err
	}
	plan.ContainerExists = exists
	if !exists {
		plan.CurrentState = "CONTAINER_ABSENT"
		if plan.NetworkName != "" {
			plan.Actions = append(plan.Actions, Action{Type: "create-network", Resource: plan.NetworkName, Description: "create an isolated V2 internal network"})
		}
		plan.Actions = append(plan.Actions, Action{Type: "create-container", Resource: plan.ContainerName, Description: "create a V2-labeled environment container"}, Action{Type: "start-container", Resource: plan.ContainerName, Description: "start the environment container"})
	} else {
		if err := podman.VerifyEnvironment(inspect, environmentState.ID); err != nil {
			return Plan{}, err
		}
		plan.ContainerRunning = inspect.Running
		plan.DriftReasons = driftReasons(inspect.Labels, plan, environmentState)
		if len(plan.DriftReasons) > 0 {
			plan.CurrentState = "DRIFTED"
			plan.Actions = append(plan.Actions, Action{Type: "recreate-required", Resource: plan.ContainerName, Description: "manifest drift requires explicit cagent env recreate", Destructive: true})
		} else if inspect.Running {
			plan.CurrentState = "RUNNING"
		} else {
			plan.CurrentState = "STOPPED"
			plan.Actions = append(plan.Actions, Action{Type: "start-container", Resource: plan.ContainerName, Description: "start the existing environment container"})
		}
	}
	plan.BuildArgs = buildArgs(resolved)
	plan.CreateArgs = createArgs(plan, resolved, b.Capabilities, b.Paths)
	sort.Strings(plan.Warnings)
	sort.Strings(plan.PolicyExceptions)
	return plan, nil
}

func buildArgs(resolved profile.Resolved) []string {
	if resolved.Manifest.Spec.Image.Mode != "build" {
		return nil
	}
	args := []string{"build", "--layers", "--pull=" + resolved.Manifest.Spec.Image.PullPolicy, "--tag", resolved.ImageReference, "--label", podman.ManagedLabel + "=true", "--label", "io.containersagents.v2.resource-type=profile-image", "--label", podman.ProfileHashLabel + "=" + resolved.Hash}
	if resolved.Manifest.Spec.Image.Target != "" {
		args = append(args, "--target", resolved.Manifest.Spec.Image.Target)
	}
	keys := make([]string, 0, len(resolved.Manifest.Spec.Image.BuildArgs))
	for key := range resolved.Manifest.Spec.Image.BuildArgs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "--build-arg", key+"="+resolved.Manifest.Spec.Image.BuildArgs[key])
	}
	return args
}

func createArgs(plan Plan, resolved profile.Resolved, capabilities capability.Report, paths state.Paths) []string {
	if plan.EnvironmentID == "<registration-required>" {
		return nil
	}
	args := []string{"container", "create", "--name", plan.ContainerName, "--workdir", resolved.Manifest.Spec.Runtime.Workdir, "--stop-timeout", "10"}
	labels := map[string]string{
		podman.ManagedLabel: "true", "io.containersagents.v2.schema": "1", podman.EnvironmentIDLabel: plan.EnvironmentID,
		"io.containersagents.v2.environment-name": plan.Environment, "io.containersagents.v2.profile": plan.Profile,
		podman.ProfileHashLabel: plan.ProfileHash, podman.SpecHashLabel: plan.SpecHash, podman.ProjectHashLabel: plan.ProjectHash,
		"io.containersagents.v2.security-class": plan.Security.Class, "io.containersagents.v2.resource-class": plan.Resources.Class,
		"io.containersagents.v2.memory-bytes": strconv.FormatInt(plan.Resources.MemoryBytes, 10),
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if labels[key] != "" {
			args = append(args, "--label", key+"="+labels[key])
		}
	}
	args = append(args, "--memory", strconv.FormatInt(plan.Resources.MemoryBytes, 10), "--memory-swap", strconv.FormatInt(plan.Resources.SwapBytes, 10), "--cpus", strconv.FormatFloat(plan.Resources.CPUs, 'f', 2, 64), "--pids-limit", strconv.Itoa(plan.Resources.PIDs), "--shm-size", strconv.FormatInt(plan.Resources.SHMBytes, 10))
	args = append(args, plan.Security.PodmanArgs...)
	switch resolved.Manifest.Spec.Runtime.IdentityMode {
	case "managed-user":
		args = append(args, "--userns", fmt.Sprintf("keep-id:uid=%d,gid=%d", resolved.Manifest.Spec.Runtime.UID, resolved.Manifest.Spec.Runtime.GID), "--user", resolved.Manifest.Spec.Runtime.User)
	case "explicit":
		args = append(args, "--user", resolved.Manifest.Spec.Runtime.User)
	case "rootless-container-root":
		args = append(args, "--user", "0")
	}
	switch plan.Security.NetworkMode {
	case "none":
		args = append(args, "--network", "none")
	case "internal":
		args = append(args, "--network", plan.NetworkName)
	}
	for _, port := range plan.Ports {
		hostIP := port.HostIP
		if strings.Contains(hostIP, ":") {
			hostIP = "[" + hostIP + "]"
		}
		args = append(args, "--publish", fmt.Sprintf("%s:%d:%d/%s", hostIP, port.HostPort, port.ContainerPort, port.Protocol))
	}
	for _, mount := range plan.Mounts {
		options := []string{"type=bind", "src=" + mount.Source, "dst=" + mount.Target}
		if mount.Mode == "read-only" {
			options = append(options, "ro")
		} else {
			options = append(options, "rw")
		}
		if capabilities.SELinux {
			options = append(options, "relabel=private")
		}
		args = append(args, "--mount", strings.Join(options, ","))
	}
	if plan.Security.HomeAllowed && plan.EffectiveManifest.Spec.Home.Persistence == "per-environment" {
		options := []string{"type=bind", "src=" + paths.EnvironmentHome(plan.EnvironmentID), "dst=" + resolved.Manifest.Spec.Runtime.Home, "rw"}
		if capabilities.SELinux {
			options = append(options, "relabel=private")
		}
		args = append(args, "--mount", strings.Join(options, ","))
	} else if resolved.Manifest.Spec.Runtime.Home != "" {
		args = append(args, "--tmpfs", resolved.Manifest.Spec.Runtime.Home+":rw,nosuid,nodev,size=512m")
	}
	for _, secret := range plan.EffectiveManifest.Spec.Secrets {
		value := fmt.Sprintf("%s,target=%s,uid=%d,gid=%d,mode=%04o", secret.Name, secret.Target, secret.UID, secret.GID, secret.Mode)
		args = append(args, "--secret", value)
	}
	environment := map[string]string{}
	for key, value := range plan.EffectiveManifest.Spec.Environment {
		environment[key] = value
	}
	if proxy := plan.EffectiveManifest.Spec.Proxy; proxy != nil {
		if proxy.HTTP != "" {
			environment["HTTP_PROXY"], environment["http_proxy"] = proxy.HTTP, proxy.HTTP
		}
		if proxy.HTTPS != "" {
			environment["HTTPS_PROXY"], environment["https_proxy"] = proxy.HTTPS, proxy.HTTPS
		}
		if len(proxy.NoProxy) > 0 {
			value := strings.Join(proxy.NoProxy, ",")
			environment["NO_PROXY"], environment["no_proxy"] = value, value
		}
	}
	if plan.CertificateBundle != "" {
		environment["SSL_CERT_FILE"] = "/run/containersagents/ca-bundle.pem"
		environment["REQUESTS_CA_BUNDLE"] = "/run/containersagents/ca-bundle.pem"
		environment["CURL_CA_BUNDLE"] = "/run/containersagents/ca-bundle.pem"
	}
	envKeys := make([]string, 0, len(environment))
	for key := range environment {
		envKeys = append(envKeys, key)
	}
	sort.Strings(envKeys)
	for _, key := range envKeys {
		args = append(args, "--env", key+"="+environment[key])
	}
	args = append(args, resolved.ImageReference)
	args = append(args, resolved.Manifest.Spec.Runtime.Keepalive...)
	return args
}

func networkCreateArgs(plan Plan) []string {
	return []string{"network", "create", "--internal", "--label", podman.ManagedLabel + "=true", "--label", podman.EnvironmentIDLabel + "=" + plan.EnvironmentID, plan.NetworkName}
}

func NetworkCreateArgs(plan Plan) []string { return networkCreateArgs(plan) }

func containerName(name, id string) string {
	return "ca2-" + name + "-" + strings.ReplaceAll(id[:8], "-", "")
}

func pathHash(path string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(path)))
	return hex.EncodeToString(sum[:])
}

func driftReasons(labels map[string]string, plan Plan, state state.EnvironmentState) []string {
	var reasons []string
	if labels[podman.ProfileHashLabel] != plan.ProfileHash {
		reasons = append(reasons, "profile image or build context changed")
	}
	if labels[podman.SpecHashLabel] != plan.SpecHash {
		reasons = append(reasons, "effective environment specification changed")
	}
	if labels[podman.ProjectHashLabel] != plan.ProjectHash {
		reasons = append(reasons, "project mount changed")
	}
	if state.SpecHash != "" && state.SpecHash != plan.SpecHash {
		reasons = append(reasons, "stored applied specification differs")
	}
	sort.Strings(reasons)
	return unique(reasons)
}

func unique(values []string) []string {
	result := values[:0]
	var previous string
	for _, value := range values {
		if value != previous {
			result = append(result, value)
			previous = value
		}
	}
	return result
}

func addContainerTarget(targets map[string]string, target, kind string) error {
	cleaned := filepath.Clean(target)
	if !filepath.IsAbs(cleaned) || cleaned == string(filepath.Separator) {
		return fmt.Errorf("%s target %q must be an absolute non-root container path", kind, target)
	}
	if strings.Contains(cleaned, ",") {
		return fmt.Errorf("%s target %q contains a comma delimiter unsupported by Podman --mount", kind, target)
	}
	for _, denied := range []string{"/boot", "/dev", "/etc", "/proc", "/sys", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/var", "/run"} {
		if cleaned == denied || strings.HasPrefix(cleaned, denied+"/") {
			return fmt.Errorf("%s target %q is inside protected container path %q", kind, target, denied)
		}
	}
	if existing, duplicate := targets[cleaned]; duplicate {
		return fmt.Errorf("%s target %q collides with %s", kind, target, existing)
	}
	targets[cleaned] = kind
	return nil
}
