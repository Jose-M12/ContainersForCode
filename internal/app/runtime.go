package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"containersagents.dev/v2/internal/capability"
	"containersagents.dev/v2/internal/fsutil"
	"containersagents.dev/v2/internal/hostinfo"
	"containersagents.dev/v2/internal/manifest"
)

type runtimeContext struct {
	Capabilities capability.Report
	Host         hostinfo.Resources
	Defaults     manifest.Defaults
}

func (a *Application) discoverRuntime(ctx context.Context, force bool) (runtimeContext, error) {
	defaults, err := manifest.LoadDefaults(a.paths.DefaultsFile())
	if err != nil {
		return runtimeContext{}, err
	}
	var report capability.Report
	if !force {
		if data, readErr := os.ReadFile(a.paths.CapabilityCache()); readErr == nil {
			var cached capability.Report
			if json.Unmarshal(data, &cached) == nil && time.Since(cached.DetectedAt) < 5*time.Minute {
				report = cached
			}
		}
	}
	if report.DetectedAt.IsZero() {
		report, err = capability.Discover(ctx, a.runner)
		if err != nil {
			return runtimeContext{}, err
		}
		if data, marshalErr := json.MarshalIndent(report, "", "  "); marshalErr == nil {
			_ = fsutil.AtomicWrite(a.paths.CapabilityCache(), append(data, '\n'), 0600)
		}
	}
	host, err := hostinfo.Detect()
	if err != nil {
		return runtimeContext{}, err
	}
	return runtimeContext{Capabilities: report, Host: host, Defaults: defaults}, nil
}

func (a *Application) requireRootless(ctx context.Context) (runtimeContext, error) {
	runtime, err := a.discoverRuntime(ctx, false)
	if err != nil {
		return runtimeContext{}, err
	}
	if !runtime.Capabilities.Rootless {
		return runtimeContext{}, policyError("rootless Podman is required; Podman reports rootless=false")
	}
	return runtime, nil
}

func ensureOutput(value string) error {
	if value != "human" && value != "json" {
		return usage("--output must be human or json")
	}
	return nil
}

func humanBytes(value int64) string {
	const unit = int64(1024)
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	divisor, exponent := unit, 0
	for amount := value / unit; amount >= unit && exponent < 5; amount /= unit {
		divisor *= unit
		exponent++
	}
	return fmt.Sprintf("%.1f %ciB", float64(value)/float64(divisor), "KMGTPE"[exponent])
}
