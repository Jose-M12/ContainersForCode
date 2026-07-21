package manifest

const (
	APIVersion      = "containersagents.dev/v2alpha1"
	ProfileKind     = "Profile"
	EnvironmentKind = "Environment"
	DefaultsKind    = "Defaults"
)

type Metadata struct {
	Name string `json:"name"`
}

type Profile struct {
	APIVersion string      `json:"apiVersion"`
	Kind       string      `json:"kind"`
	Metadata   Metadata    `json:"metadata"`
	Spec       ProfileSpec `json:"spec"`
}

type ProfileSpec struct {
	Image            ImageSpec         `json:"image"`
	Runtime          RuntimeSpec       `json:"runtime"`
	Features         []string          `json:"features,omitempty"`
	Defaults         ProfileDefaults   `json:"defaults,omitempty"`
	MinimumResources MinimumResources  `json:"minimumResources,omitempty"`
	Checks           []ValidationCheck `json:"checks,omitempty"`
}

type ImageSpec struct {
	Mode          string            `json:"mode"`
	Repository    string            `json:"repository,omitempty"`
	Reference     string            `json:"reference,omitempty"`
	Containerfile string            `json:"containerfile,omitempty"`
	Context       string            `json:"context,omitempty"`
	PullPolicy    string            `json:"pullPolicy,omitempty"`
	BuildArgs     map[string]string `json:"buildArgs,omitempty"`
	BuildSecrets  []BuildSecret     `json:"buildSecrets,omitempty"`
	Target        string            `json:"target,omitempty"`
}

type BuildSecret struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

type RuntimeSpec struct {
	User         string   `json:"user,omitempty"`
	UID          int      `json:"uid,omitempty"`
	GID          int      `json:"gid,omitempty"`
	Home         string   `json:"home,omitempty"`
	Workdir      string   `json:"workdir,omitempty"`
	Shell        []string `json:"shell"`
	Keepalive    []string `json:"keepalive"`
	IdentityMode string   `json:"identityMode"`
	AllowSudo    bool     `json:"allowSudo,omitempty"`
	CABundle     string   `json:"caBundle,omitempty"`
}

type ProfileDefaults struct {
	SecurityClass     string `json:"securityClass,omitempty"`
	ResourceClass     string `json:"resourceClass,omitempty"`
	RootFSPersistence string `json:"rootfsPersistence,omitempty"`
}

type MinimumResources struct {
	MemoryMiB int     `json:"memoryMiB,omitempty"`
	CPUs      float64 `json:"cpus,omitempty"`
	PIDs      int     `json:"pids,omitempty"`
	SHMMiB    int     `json:"shmMiB,omitempty"`
}

type ValidationCheck struct {
	Name           string   `json:"name"`
	Command        []string `json:"command"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty"`
}

type Environment struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Metadata   Metadata        `json:"metadata"`
	Spec       EnvironmentSpec `json:"spec"`
}

type EnvironmentSpec struct {
	Profile       string            `json:"profile"`
	Project       *ProjectSpec      `json:"project,omitempty"`
	Home          HomeSpec          `json:"home,omitempty"`
	RootFS        RootFSSpec        `json:"rootfs,omitempty"`
	SecurityClass string            `json:"securityClass,omitempty"`
	ResourceClass string            `json:"resourceClass,omitempty"`
	Security      SecuritySpec      `json:"security,omitempty"`
	Resources     ResourceOverrides `json:"resources,omitempty"`
	Concurrency   ConcurrencySpec   `json:"concurrency,omitempty"`
	Network       NetworkSpec       `json:"network,omitempty"`
	Secrets       []SecretSpec      `json:"secrets,omitempty"`
	Mounts        []MountSpec       `json:"mounts,omitempty"`
	Certificates  []CertificateSpec `json:"certificates,omitempty"`
	Proxy         *ProxySpec        `json:"proxy,omitempty"`
	Environment   map[string]string `json:"environment,omitempty"`
}

type ProjectSpec struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Mode   string `json:"mode"`
}

type HomeSpec struct {
	Persistence string `json:"persistence,omitempty"`
}

type RootFSSpec struct {
	Persistence string `json:"persistence,omitempty"`
}

type SecuritySpec struct {
	Capabilities    []string `json:"capabilities,omitempty"`
	NoNewPrivileges *bool    `json:"noNewPrivileges,omitempty"`
}

type ResourceOverrides struct {
	MemoryMiB int     `json:"memoryMiB,omitempty"`
	SwapMiB   int     `json:"swapMiB,omitempty"`
	CPUs      float64 `json:"cpus,omitempty"`
	PIDs      int     `json:"pids,omitempty"`
	SHMMiB    int     `json:"shmMiB,omitempty"`
	BuildJobs int     `json:"buildJobs,omitempty"`
}

type ConcurrencySpec struct {
	ProjectMode string `json:"projectMode,omitempty"`
}

type NetworkSpec struct {
	Mode  string     `json:"mode,omitempty"`
	Ports []PortSpec `json:"ports,omitempty"`
}

type PortSpec struct {
	HostIP        string `json:"hostIP,omitempty"`
	HostPort      int    `json:"hostPort"`
	ContainerPort int    `json:"containerPort"`
	Protocol      string `json:"protocol,omitempty"`
}

type SecretSpec struct {
	Name   string `json:"name"`
	Target string `json:"target"`
	UID    int    `json:"uid,omitempty"`
	GID    int    `json:"gid,omitempty"`
	Mode   int    `json:"mode,omitempty"`
}

type MountSpec struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Mode   string `json:"mode"`
	Shared bool   `json:"shared,omitempty"`
}

type CertificateSpec struct {
	Source string `json:"source"`
	Name   string `json:"name,omitempty"`
}

type ProxySpec struct {
	HTTP    string   `json:"http,omitempty"`
	HTTPS   string   `json:"https,omitempty"`
	NoProxy []string `json:"noProxy,omitempty"`
}

type Defaults struct {
	APIVersion string       `json:"apiVersion"`
	Kind       string       `json:"kind"`
	Spec       DefaultsSpec `json:"spec"`
}

type DefaultsSpec struct {
	SecurityClass     string   `json:"securityClass,omitempty"`
	ResourceClass     string   `json:"resourceClass,omitempty"`
	AllowedMountRoots []string `json:"allowedMountRoots,omitempty"`
	StrictResources   bool     `json:"strictResources,omitempty"`
	AuditMaxMiB       int      `json:"auditMaxMiB,omitempty"`
	StopOnShellExit   *bool    `json:"stopOnShellExit,omitempty"`
}
