package hostinfo

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type Resources struct {
	LogicalCPUs      int    `json:"logicalCPUs"`
	TotalMemoryBytes int64  `json:"totalMemoryBytes"`
	AvailableBytes   int64  `json:"availableMemoryBytes"`
	PowerSource      string `json:"powerSource"`
}

func Detect() (Resources, error) {
	resources := Resources{LogicalCPUs: runtime.NumCPU(), PowerSource: detectPower()}
	if runtime.GOOS == "darwin" {
		output, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
		if err != nil {
			return resources, fmt.Errorf("discover macOS memory: %w", err)
		}
		value, err := strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64)
		if err != nil || value <= 0 {
			return resources, fmt.Errorf("parse macOS physical memory")
		}
		resources.TotalMemoryBytes, resources.AvailableBytes = value, value
		return resources, nil
	}
	if runtime.GOOS != "linux" {
		return resources, fmt.Errorf("automatic memory discovery is not yet supported on %s; run cagent inside Podman Machine or use Linux", runtime.GOOS)
	}
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return resources, fmt.Errorf("read host memory information: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, parseErr := strconv.ParseInt(fields[1], 10, 64)
		if parseErr != nil {
			continue
		}
		switch strings.TrimSuffix(fields[0], ":") {
		case "MemTotal":
			resources.TotalMemoryBytes = value * 1024
		case "MemAvailable":
			resources.AvailableBytes = value * 1024
		}
	}
	if err := scanner.Err(); err != nil {
		return resources, err
	}
	if resources.TotalMemoryBytes <= 0 {
		return resources, fmt.Errorf("host total memory is unavailable")
	}
	return resources, nil
}

func detectPower() string {
	if runtime.GOOS == "darwin" {
		output, err := exec.Command("pmset", "-g", "batt").Output()
		if err != nil {
			return "unknown"
		}
		text := strings.ToLower(string(output))
		if strings.Contains(text, "ac power") {
			return "ac"
		}
		if strings.Contains(text, "battery power") {
			return "battery"
		}
		return "unknown"
	}
	if runtime.GOOS != "linux" {
		return "unknown"
	}
	entries, err := filepath.Glob("/sys/class/power_supply/*")
	if err != nil || len(entries) == 0 {
		return "unknown"
	}
	hasBattery := false
	for _, entry := range entries {
		typeData, _ := os.ReadFile(filepath.Join(entry, "type"))
		typeName := strings.TrimSpace(string(typeData))
		if typeName == "Battery" {
			hasBattery = true
		}
		if typeName == "Mains" || typeName == "USB" || typeName == "USB_C" {
			online, _ := os.ReadFile(filepath.Join(entry, "online"))
			if strings.TrimSpace(string(online)) == "1" {
				return "ac"
			}
		}
	}
	if hasBattery {
		return "battery"
	}
	return "unknown"
}
