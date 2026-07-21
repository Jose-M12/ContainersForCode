package policy

import (
	"fmt"
	"sort"

	"containersagents.dev/v2/internal/manifest"
)

type SecurityPlan struct {
	Class            string   `json:"class"`
	PodmanArgs       []string `json:"podmanArgs"`
	ProjectReadOnly  bool     `json:"projectReadOnly"`
	HomeAllowed      bool     `json:"homeAllowed"`
	NetworkMode      string   `json:"networkMode"`
	Warnings         []string `json:"warnings,omitempty"`
	PolicyExceptions []string `json:"policyExceptions,omitempty"`
}

func CompileSecurity(environment manifest.Environment, profile manifest.Profile) (SecurityPlan, error) {
	class := environment.Spec.SecurityClass
	plan := SecurityPlan{Class: class, HomeAllowed: true, NetworkMode: environment.Spec.Network.Mode}
	switch class {
	case "sandbox":
		plan.PodmanArgs = append(plan.PodmanArgs, "--read-only", "--cap-drop=all", "--security-opt=no-new-privileges", "--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=256m", "--tmpfs", "/run:rw,nosuid,nodev,size=64m")
		plan.ProjectReadOnly = true
		plan.HomeAllowed = false
		plan.NetworkMode = "none"
	case "development":
		if environment.Spec.Security.NoNewPrivileges != nil && *environment.Spec.Security.NoNewPrivileges {
			if profile.Spec.Runtime.AllowSudo {
				return SecurityPlan{}, fmt.Errorf("noNewPrivileges cannot be enabled for a profile that declares setuid-based sudo")
			}
			plan.PodmanArgs = append(plan.PodmanArgs, "--security-opt=no-new-privileges")
		}
	case "integration":
		if environment.Spec.Network.Mode == "none" {
			plan.NetworkMode = "none"
		}
	case "elevated-lab":
		if len(environment.Spec.Security.Capabilities) == 0 {
			plan.Warnings = append(plan.Warnings, "elevated-lab has no selected capabilities")
		}
		for _, capability := range environment.Spec.Security.Capabilities {
			plan.PodmanArgs = append(plan.PodmanArgs, "--cap-add="+capability)
			plan.PolicyExceptions = append(plan.PolicyExceptions, "added capability "+capability)
		}
	default:
		return SecurityPlan{}, fmt.Errorf("unknown security class %q", class)
	}
	if environment.Spec.RootFS.Persistence == "read-only" && class != "sandbox" {
		plan.PodmanArgs = append(plan.PodmanArgs, "--read-only", "--tmpfs", "/tmp:rw,nosuid,nodev,size=256m", "--tmpfs", "/run:rw,nosuid,nodev,size=64m")
	}
	if plan.ProjectReadOnly && environment.Spec.Project != nil && environment.Spec.Project.Mode == "read-write" {
		plan.Warnings = append(plan.Warnings, "sandbox policy downgrades the project mount to read-only")
	}
	if class != "elevated-lab" && len(environment.Spec.Security.Capabilities) > 0 {
		return SecurityPlan{}, fmt.Errorf("additional capabilities require elevated-lab security class")
	}
	sort.Strings(plan.PolicyExceptions)
	return plan, nil
}
