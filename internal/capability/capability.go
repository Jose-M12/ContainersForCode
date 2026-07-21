package capability

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"time"

	"containersagents.dev/v2/internal/podman"
)

type Report struct {
	DetectedAt      time.Time `json:"detectedAt"`
	PodmanVersion   string    `json:"podmanVersion"`
	Rootless        bool      `json:"rootless"`
	CgroupVersion   string    `json:"cgroupVersion"`
	OCIRuntime      string    `json:"ociRuntime"`
	StorageDriver   string    `json:"storageDriver"`
	Secrets         bool      `json:"secrets"`
	JSONOutput      bool      `json:"jsonOutput"`
	ResourceLimits  bool      `json:"resourceLimits"`
	ResourceUpdate  bool      `json:"resourceUpdate"`
	SELinux         bool      `json:"selinux"`
	AppArmor        bool      `json:"apparmor"`
	Architecture    string    `json:"architecture"`
	OperatingSystem string    `json:"operatingSystem"`
	PodmanMachine   bool      `json:"podmanMachine"`
}

func Discover(ctx context.Context, runner podman.Runner) (Report, error) {
	if err := runner.Available(); err != nil {
		return Report{}, err
	}
	versionResult, err := runner.Run(ctx, "version", "--format", "json")
	if err != nil {
		return Report{}, err
	}
	infoResult, err := runner.Run(ctx, "info", "--format", "json")
	if err != nil {
		return Report{}, err
	}
	var version, info map[string]any
	if err := json.Unmarshal([]byte(versionResult.Stdout), &version); err != nil {
		return Report{}, fmt.Errorf("decode Podman version JSON: %w", err)
	}
	if err := json.Unmarshal([]byte(infoResult.Stdout), &info); err != nil {
		return Report{}, fmt.Errorf("decode Podman info JSON: %w", err)
	}
	host := mapValue(info, "host", "Host")
	security := mapValue(host, "security", "Security")
	store := mapValue(info, "store", "Store")
	server := mapValue(version, "Server", "server")
	client := mapValue(version, "Client", "client")
	report := Report{
		DetectedAt:      time.Now().UTC(),
		PodmanVersion:   firstString(server, "Version", "version"),
		Rootless:        boolValue(security, "rootless", "Rootless"),
		CgroupVersion:   firstString(host, "cgroupVersion", "CgroupVersion"),
		OCIRuntime:      firstString(mapValue(host, "ociRuntime", "OCIRuntime"), "name", "Name"),
		StorageDriver:   firstString(store, "graphDriverName", "GraphDriverName"),
		JSONOutput:      true,
		SELinux:         boolValue(security, "selinuxEnabled", "SELinuxEnabled"),
		AppArmor:        boolValue(security, "apparmorEnabled", "AppArmorEnabled"),
		Architecture:    firstString(host, "arch", "Arch"),
		OperatingSystem: firstString(host, "os", "OS"),
	}
	if report.PodmanVersion == "" {
		report.PodmanVersion = firstString(client, "Version", "version")
	}
	if report.Architecture == "" {
		report.Architecture = runtime.GOARCH
	}
	if report.OperatingSystem == "" {
		report.OperatingSystem = runtime.GOOS
	}
	report.ResourceLimits = strings.TrimSpace(report.CgroupVersion) == "v2" || strings.TrimSpace(report.CgroupVersion) == "2"
	if help, helpErr := runner.Run(ctx, "secret", "--help"); helpErr == nil && strings.Contains(strings.ToLower(help.Stdout+help.Stderr), "secret") {
		report.Secrets = true
	}
	if help, helpErr := runner.Run(ctx, "container", "update", "--help"); helpErr == nil && strings.Contains(help.Stdout+help.Stderr, "--memory") {
		report.ResourceUpdate = true
	}
	remote := boolValue(client, "Remote", "remote") || boolValue(info, "remote", "Remote")
	report.PodmanMachine = runtime.GOOS != "linux" || remote
	return report, nil
}

func mapValue(source map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if value, ok := source[key].(map[string]any); ok {
			return value
		}
	}
	return map[string]any{}
}

func firstString(source map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := source[key]; ok && value != nil {
			return fmt.Sprint(value)
		}
	}
	return ""
}

func boolValue(source map[string]any, keys ...string) bool {
	for _, key := range keys {
		if value, ok := source[key].(bool); ok {
			return value
		}
	}
	return false
}
