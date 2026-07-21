package manifest

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

var (
	namePattern       = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
	envKeyPattern     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	featurePattern    = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
	capabilityPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]+$`)
	secretNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,252}$`)
)

type ValidationErrors struct {
	Items []string
}

func (e *ValidationErrors) Add(path, message string) {
	e.Items = append(e.Items, path+": "+message)
}

func (e *ValidationErrors) Err() error {
	if len(e.Items) == 0 {
		return nil
	}
	sort.Strings(e.Items)
	return fmt.Errorf("validation failed:\n  - %s", strings.Join(e.Items, "\n  - "))
}

func ValidateName(name string) error {
	if len(name) == 0 || len(name) > 63 || !namePattern.MatchString(name) {
		return fmt.Errorf("must be 1-63 characters, start with a letter, and contain only lowercase letters, digits, and single hyphen-separated segments")
	}
	return nil
}

func ValidateProfile(profile Profile) error {
	var errs ValidationErrors
	validateHeader(profile.APIVersion, profile.Kind, ProfileKind, profile.Metadata.Name, &errs)
	image := profile.Spec.Image
	if !oneOf(image.Mode, "build", "pull", "existing") {
		errs.Add("spec.image.mode", "must be build, pull, or existing")
	}
	if !oneOf(image.PullPolicy, "always", "missing", "newer", "never") {
		errs.Add("spec.image.pullPolicy", "must be always, missing, newer, or never")
	}
	if image.Mode == "build" {
		if image.Repository == "" {
			errs.Add("spec.image.repository", "is required in build mode")
		}
		if image.Containerfile == "" {
			errs.Add("spec.image.containerfile", "must be a non-empty path")
		}
		if image.Context == "" {
			errs.Add("spec.image.context", "must be a non-empty explicit path")
		}
		if image.Reference != "" {
			errs.Add("spec.image.reference", "must not be set in build mode")
		}
	} else if image.Mode == "pull" || image.Mode == "existing" {
		if image.Reference == "" {
			errs.Add("spec.image.reference", "is required in pull and existing modes")
		}
		if image.Repository != "" || image.Containerfile != "" || image.Context != "" {
			errs.Add("spec.image", "repository, containerfile, and context are build-mode fields")
		}
	}
	if image.Repository != "" && unsafeCLIValue(image.Repository) {
		errs.Add("spec.image.repository", "must not begin with a hyphen or contain whitespace or control characters")
	}
	if image.Reference != "" && unsafeCLIValue(image.Reference) {
		errs.Add("spec.image.reference", "must not begin with a hyphen or contain whitespace or control characters")
	}
	for key := range image.BuildArgs {
		if !envKeyPattern.MatchString(key) {
			errs.Add("spec.image.buildArgs."+key, "invalid build argument name")
		}
		if looksSecret(key) {
			errs.Add("spec.image.buildArgs."+key, "secret-like values must use buildSecrets, never build arguments")
		}
	}
	seenSecrets := map[string]bool{}
	for i, secret := range image.BuildSecrets {
		path := fmt.Sprintf("spec.image.buildSecrets[%d]", i)
		if secret.Name == "" || secret.ID == "" {
			errs.Add(path, "name and id are required")
		}
		if seenSecrets[secret.ID] {
			errs.Add(path+".id", "must be unique")
		}
		seenSecrets[secret.ID] = true
	}

	runtime := profile.Spec.Runtime
	if !oneOf(runtime.IdentityMode, "managed-user", "image-user", "explicit", "rootless-container-root") {
		errs.Add("spec.runtime.identityMode", "must be managed-user, image-user, explicit, or rootless-container-root")
	}
	if runtime.IdentityMode == "managed-user" {
		if runtime.User == "" || runtime.UID <= 0 || runtime.GID <= 0 {
			errs.Add("spec.runtime", "managed-user requires user and positive uid/gid")
		}
	}
	if runtime.IdentityMode == "explicit" && runtime.User == "" {
		errs.Add("spec.runtime.user", "is required in explicit identity mode")
	}
	if runtime.Home == "" || !filepath.IsAbs(runtime.Home) {
		errs.Add("spec.runtime.home", "must be an absolute container path")
	} else if filepath.Clean(runtime.Home) == string(filepath.Separator) {
		errs.Add("spec.runtime.home", "must not be the container root")
	} else if strings.ContainsAny(runtime.Home, ",:") {
		errs.Add("spec.runtime.home", "must not contain comma or colon delimiters")
	}
	if runtime.Workdir == "" || !filepath.IsAbs(runtime.Workdir) {
		errs.Add("spec.runtime.workdir", "must be an absolute container path")
	}
	if runtime.CABundle != "" && !filepath.IsAbs(runtime.CABundle) {
		errs.Add("spec.runtime.caBundle", "must be an absolute container path")
	}
	if len(runtime.Shell) == 0 || runtime.Shell[0] == "" {
		errs.Add("spec.runtime.shell", "must declare an executable and optional arguments")
	}
	if len(runtime.Keepalive) == 0 || runtime.Keepalive[0] == "" {
		errs.Add("spec.runtime.keepalive", "must declare an executable and optional arguments")
	}
	seenFeatures := map[string]bool{}
	for i, feature := range profile.Spec.Features {
		if !featurePattern.MatchString(feature) {
			errs.Add(fmt.Sprintf("spec.features[%d]", i), "invalid feature name")
		}
		if seenFeatures[feature] {
			errs.Add(fmt.Sprintf("spec.features[%d]", i), "must be unique")
		}
		seenFeatures[feature] = true
	}
	if !validSecurityClass(profile.Spec.Defaults.SecurityClass) {
		errs.Add("spec.defaults.securityClass", "invalid security class")
	}
	if !validResourceClass(profile.Spec.Defaults.ResourceClass) {
		errs.Add("spec.defaults.resourceClass", "invalid resource class")
	}
	if !validRootFS(profile.Spec.Defaults.RootFSPersistence) {
		errs.Add("spec.defaults.rootfsPersistence", "invalid root filesystem persistence")
	}
	if profile.Spec.MinimumResources.MemoryMiB < 0 || profile.Spec.MinimumResources.CPUs < 0 || profile.Spec.MinimumResources.PIDs < 0 || profile.Spec.MinimumResources.SHMMiB < 0 {
		errs.Add("spec.minimumResources", "resource minimums cannot be negative")
	}
	seenChecks := map[string]bool{}
	for i, check := range profile.Spec.Checks {
		path := fmt.Sprintf("spec.checks[%d]", i)
		if check.Name == "" || len(check.Command) == 0 || check.Command[0] == "" {
			errs.Add(path, "name and command are required")
		}
		if seenChecks[check.Name] {
			errs.Add(path+".name", "must be unique")
		}
		seenChecks[check.Name] = true
		if check.TimeoutSeconds < 1 || check.TimeoutSeconds > 3600 {
			errs.Add(path+".timeoutSeconds", "must be between 1 and 3600")
		}
	}
	return errs.Err()
}

func ValidateEnvironment(environment Environment) error {
	var errs ValidationErrors
	validateHeader(environment.APIVersion, environment.Kind, EnvironmentKind, environment.Metadata.Name, &errs)
	if err := ValidateName(environment.Spec.Profile); err != nil {
		errs.Add("spec.profile", err.Error())
	}
	if environment.Spec.Project != nil {
		project := environment.Spec.Project
		if project.Source == "" {
			errs.Add("spec.project.source", "is required")
		}
		if project.Target != "" && !filepath.IsAbs(project.Target) {
			errs.Add("spec.project.target", "must be an absolute container path")
		}
		if project.Mode != "" && !oneOf(project.Mode, "read-only", "read-write") {
			errs.Add("spec.project.mode", "must be read-only or read-write")
		}
	}
	if environment.Spec.Home.Persistence != "" && !oneOf(environment.Spec.Home.Persistence, "per-environment", "ephemeral", "none") {
		errs.Add("spec.home.persistence", "must be per-environment, ephemeral, or none")
	}
	if environment.Spec.RootFS.Persistence != "" && !validRootFS(environment.Spec.RootFS.Persistence) {
		errs.Add("spec.rootfs.persistence", "must be persistent, recreate, ephemeral, or read-only")
	}
	if environment.Spec.SecurityClass != "" && !validSecurityClass(environment.Spec.SecurityClass) {
		errs.Add("spec.securityClass", "invalid security class")
	}
	if environment.Spec.ResourceClass != "" && !validResourceClass(environment.Spec.ResourceClass) {
		errs.Add("spec.resourceClass", "invalid resource class")
	}
	seenCapabilities := map[string]bool{}
	for i, capability := range environment.Spec.Security.Capabilities {
		if !capabilityPattern.MatchString(capability) || capability == "ALL" || capability == "SYS_ADMIN" {
			errs.Add(fmt.Sprintf("spec.security.capabilities[%d]", i), "must be a specific Linux capability and cannot be ALL or SYS_ADMIN")
		}
		if seenCapabilities[capability] {
			errs.Add(fmt.Sprintf("spec.security.capabilities[%d]", i), "must be unique")
		}
		seenCapabilities[capability] = true
	}
	res := environment.Spec.Resources
	if res.MemoryMiB < 0 || res.SwapMiB < 0 || res.CPUs < 0 || res.PIDs < 0 || res.SHMMiB < 0 || res.BuildJobs < 0 {
		errs.Add("spec.resources", "resource values cannot be negative")
	}
	if environment.Spec.Concurrency.ProjectMode != "" && !oneOf(environment.Spec.Concurrency.ProjectMode, "exclusive", "allow", "read-only-secondary", "prompt") {
		errs.Add("spec.concurrency.projectMode", "invalid project concurrency mode")
	}
	if environment.Spec.Network.Mode != "" && !oneOf(environment.Spec.Network.Mode, "none", "outbound", "internal", "integration") {
		errs.Add("spec.network.mode", "invalid network mode")
	}
	seenPorts := map[string]bool{}
	for i, port := range environment.Spec.Network.Ports {
		path := fmt.Sprintf("spec.network.ports[%d]", i)
		if port.HostIP != "" && net.ParseIP(port.HostIP) == nil {
			errs.Add(path+".hostIP", "must be a literal IP address")
		}
		if port.HostPort < 1 || port.HostPort > 65535 || port.ContainerPort < 1 || port.ContainerPort > 65535 {
			errs.Add(path, "hostPort and containerPort must be between 1 and 65535")
		}
		if port.Protocol != "" && !oneOf(port.Protocol, "tcp", "udp") {
			errs.Add(path+".protocol", "must be tcp or udp")
		}
		key := fmt.Sprintf("%s:%d/%s", port.HostIP, port.HostPort, port.Protocol)
		if seenPorts[key] {
			errs.Add(path, "duplicates another host binding")
		}
		seenPorts[key] = true
	}
	seenTargets := map[string]bool{}
	for i, secret := range environment.Spec.Secrets {
		path := fmt.Sprintf("spec.secrets[%d]", i)
		if !secretNamePattern.MatchString(secret.Name) {
			errs.Add(path+".name", "must use only letters, digits, underscore, period, and hyphen, and must start with a letter or digit")
		}
		if strings.Contains(secret.Target, ",") {
			errs.Add(path+".target", "must not contain a comma delimiter")
		} else if !filepath.IsAbs(secret.Target) || (secret.Target != "/run/secrets" && !strings.HasPrefix(filepath.Clean(secret.Target), "/run/secrets/")) {
			errs.Add(path+".target", "must be an absolute path below /run/secrets")
		}
		if secret.UID < 0 || secret.GID < 0 {
			errs.Add(path, "uid and gid cannot be negative")
		}
		if secret.Mode != 0 && (secret.Mode < 0400 || secret.Mode > 0770) {
			errs.Add(path+".mode", "must be between 0400 and 0770")
		}
		if seenTargets[secret.Target] {
			errs.Add(path+".target", "must be unique")
		}
		seenTargets[secret.Target] = true
	}
	for i, mount := range environment.Spec.Mounts {
		path := fmt.Sprintf("spec.mounts[%d]", i)
		if mount.Source == "" || !filepath.IsAbs(mount.Target) || !oneOf(mount.Mode, "read-only", "read-write") {
			errs.Add(path, "requires source, an absolute target, and read-only or read-write mode")
		}
		if strings.Contains(mount.Source, ",") || strings.Contains(mount.Target, ",") {
			errs.Add(path, "source and target must not contain comma delimiters")
		}
		if mount.Shared {
			errs.Add(path+".shared", "shared mount propagation is not supported")
		}
		if seenTargets[mount.Target] {
			errs.Add(path+".target", "must be unique across mounts and secrets")
		}
		seenTargets[mount.Target] = true
	}
	for i, certificate := range environment.Spec.Certificates {
		if certificate.Source == "" {
			errs.Add(fmt.Sprintf("spec.certificates[%d].source", i), "is required")
		}
	}
	if environment.Spec.Proxy != nil {
		validateProxy("spec.proxy.http", environment.Spec.Proxy.HTTP, &errs)
		validateProxy("spec.proxy.https", environment.Spec.Proxy.HTTPS, &errs)
	}
	for key := range environment.Spec.Environment {
		if !envKeyPattern.MatchString(key) {
			errs.Add("spec.environment."+key, "invalid environment-variable name")
		}
		if looksSecret(key) {
			errs.Add("spec.environment."+key, "secret-like values must use spec.secrets")
		}
	}
	return errs.Err()
}

func ValidateDefaults(defaults Defaults) error {
	var errs ValidationErrors
	if defaults.APIVersion != APIVersion {
		errs.Add("apiVersion", "must be "+APIVersion)
	}
	if defaults.Kind != DefaultsKind {
		errs.Add("kind", "must be "+DefaultsKind)
	}
	if !validSecurityClass(defaults.Spec.SecurityClass) {
		errs.Add("spec.securityClass", "invalid security class")
	}
	if !validResourceClass(defaults.Spec.ResourceClass) {
		errs.Add("spec.resourceClass", "invalid resource class")
	}
	if defaults.Spec.AuditMaxMiB < 1 || defaults.Spec.AuditMaxMiB > 1024 {
		errs.Add("spec.auditMaxMiB", "must be between 1 and 1024")
	}
	for i, root := range defaults.Spec.AllowedMountRoots {
		if !filepath.IsAbs(root) {
			errs.Add(fmt.Sprintf("spec.allowedMountRoots[%d]", i), "must be absolute")
		}
	}
	return errs.Err()
}

func validateHeader(apiVersion, kind, expectedKind, name string, errs *ValidationErrors) {
	if apiVersion != APIVersion {
		errs.Add("apiVersion", "must be "+APIVersion)
	}
	if kind != expectedKind {
		errs.Add("kind", "must be "+expectedKind)
	}
	if err := ValidateName(name); err != nil {
		errs.Add("metadata.name", err.Error())
	}
}

func validateProxy(path, value string, errs *ValidationErrors) {
	if value == "" {
		return
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || !oneOf(parsed.Scheme, "http", "https") {
		errs.Add(path, "must be an http or https URL")
		return
	}
	if parsed.User != nil {
		errs.Add(path, "must not contain credentials; use a secret-aware proxy provider")
	}
}

func looksSecret(key string) bool {
	upper := strings.ToUpper(key)
	for _, marker := range []string{"PASSWORD", "PASSWD", "TOKEN", "SECRET", "PRIVATE_KEY", "API_KEY", "CREDENTIAL"} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

func unsafeCLIValue(value string) bool {
	return strings.HasPrefix(value, "-") || strings.IndexFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || r < 0x20 || r == 0x7f
	}) >= 0
}

func oneOf(value string, choices ...string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}

func validSecurityClass(value string) bool {
	return oneOf(value, "sandbox", "development", "integration", "elevated-lab")
}

func validResourceClass(value string) bool {
	return oneOf(value, "battery", "balanced", "performance", "custom")
}

func validRootFS(value string) bool {
	return oneOf(value, "persistent", "recreate", "ephemeral", "read-only")
}
