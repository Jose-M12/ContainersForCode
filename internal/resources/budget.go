package resources

import (
	"fmt"
	"math"

	"containersagents.dev/v2/internal/hostinfo"
	"containersagents.dev/v2/internal/manifest"
)

const GiB int64 = 1024 * 1024 * 1024
const MiB int64 = 1024 * 1024

type Budget struct {
	Class         string  `json:"class"`
	HostReserve   int64   `json:"hostReserveBytes"`
	AggregatePool int64   `json:"aggregatePoolBytes"`
	MemoryBytes   int64   `json:"memoryBytes"`
	SwapBytes     int64   `json:"swapBytes"`
	CPUs          float64 `json:"cpus"`
	PIDs          int     `json:"pids"`
	SHMBytes      int64   `json:"shmBytes"`
	BuildJobs     int     `json:"buildJobs"`
}

func Calculate(class string, overrides manifest.ResourceOverrides, minimum manifest.MinimumResources, host hostinfo.Resources) (Budget, error) {
	if host.TotalMemoryBytes <= 0 || host.LogicalCPUs <= 0 {
		return Budget{}, fmt.Errorf("host CPU and memory information is required")
	}
	reserve := maxInt64(4*GiB, host.TotalMemoryBytes/4)
	if host.TotalMemoryBytes < 8*GiB {
		reserve = host.TotalMemoryBytes / 2
	}
	pool := host.TotalMemoryBytes - reserve
	if pool < 512*1024*1024 {
		return Budget{}, fmt.Errorf("host reserve leaves less than 512 MiB for V2 environments")
	}
	budget := Budget{Class: class, HostReserve: reserve, AggregatePool: pool}
	var memoryShare, cpuShare float64
	var memoryCap int64
	var cpuCap float64
	switch class {
	case "battery":
		memoryShare, memoryCap, cpuShare, cpuCap, budget.BuildJobs = .45, 6*GiB, .35, 4, 1
	case "balanced":
		memoryShare, memoryCap, cpuShare, cpuCap, budget.BuildJobs = .65, 12*GiB, .55, 8, 2
	case "performance":
		memoryShare, memoryCap, cpuShare, cpuCap, budget.BuildJobs = .85, 20*GiB, .80, 12, 4
	case "custom":
		if overrides.MemoryMiB <= 0 || overrides.CPUs <= 0 {
			return Budget{}, fmt.Errorf("custom resource class requires positive memoryMiB and cpus")
		}
		memoryBytes, err := mibToBytes("memoryMiB", overrides.MemoryMiB)
		if err != nil {
			return Budget{}, err
		}
		budget.MemoryBytes = memoryBytes
		budget.CPUs = overrides.CPUs
		budget.BuildJobs = overrides.BuildJobs
		if budget.BuildJobs == 0 {
			budget.BuildJobs = 1
		}
	default:
		return Budget{}, fmt.Errorf("unknown resource class %q", class)
	}
	if class != "custom" {
		budget.MemoryBytes = minInt64(int64(float64(pool)*memoryShare), memoryCap)
		budget.CPUs = math.Min(math.Max(1, math.Ceil(float64(host.LogicalCPUs)*cpuShare*10)/10), cpuCap)
		if budget.BuildJobs > host.LogicalCPUs {
			budget.BuildJobs = host.LogicalCPUs
		}
	}
	if overrides.MemoryMiB > 0 {
		memoryBytes, err := mibToBytes("memoryMiB", overrides.MemoryMiB)
		if err != nil {
			return Budget{}, err
		}
		budget.MemoryBytes = memoryBytes
	}
	if overrides.CPUs > 0 {
		budget.CPUs = overrides.CPUs
	}
	if overrides.BuildJobs > 0 {
		budget.BuildJobs = overrides.BuildJobs
	}
	if budget.MemoryBytes > pool {
		return Budget{}, fmt.Errorf("requested memory %d MiB exceeds aggregate V2 pool %d MiB", budget.MemoryBytes/(1024*1024), pool/(1024*1024))
	}
	if budget.CPUs > float64(host.LogicalCPUs) {
		return Budget{}, fmt.Errorf("requested CPUs %.2f exceeds host logical CPUs %d", budget.CPUs, host.LogicalCPUs)
	}
	minimumMemory, err := mibToBytes("profile minimum memoryMiB", minimum.MemoryMiB)
	if err != nil {
		return Budget{}, err
	}
	if minimum.MemoryMiB > 0 && budget.MemoryBytes < minimumMemory {
		return Budget{}, fmt.Errorf("profile requires %d MiB but %s budget provides %d MiB", minimum.MemoryMiB, class, budget.MemoryBytes/(1024*1024))
	}
	if minimum.CPUs > 0 && budget.CPUs < minimum.CPUs {
		return Budget{}, fmt.Errorf("profile requires %.2f CPUs but %s budget provides %.2f", minimum.CPUs, class, budget.CPUs)
	}
	budget.PIDs = 2048
	budget.SHMBytes = 512 * 1024 * 1024
	if class == "battery" {
		budget.PIDs, budget.SHMBytes = 1024, 256*1024*1024
	}
	if overrides.PIDs > 0 {
		budget.PIDs = overrides.PIDs
	}
	if overrides.SHMMiB > 0 {
		shmBytes, err := mibToBytes("shmMiB", overrides.SHMMiB)
		if err != nil {
			return Budget{}, err
		}
		budget.SHMBytes = shmBytes
	}
	if minimum.PIDs > budget.PIDs {
		budget.PIDs = minimum.PIDs
	}
	minimumSHM, err := mibToBytes("profile minimum shmMiB", minimum.SHMMiB)
	if err != nil {
		return Budget{}, err
	}
	if minimumSHM > budget.SHMBytes {
		budget.SHMBytes = minimumSHM
	}
	if budget.MemoryBytes > math.MaxInt64-budget.MemoryBytes/4 {
		return Budget{}, fmt.Errorf("default memory+swap ceiling overflows supported size")
	}
	budget.SwapBytes = budget.MemoryBytes + budget.MemoryBytes/4
	if overrides.SwapMiB > 0 {
		swapBytes, err := mibToBytes("swapMiB", overrides.SwapMiB)
		if err != nil {
			return Budget{}, err
		}
		budget.SwapBytes = swapBytes
	}
	if budget.SwapBytes < budget.MemoryBytes {
		return Budget{}, fmt.Errorf("swapMiB is the combined memory+swap ceiling and cannot be below memoryMiB")
	}
	return budget, nil
}

func CheckAggregate(pool, running, requested int64) error {
	if pool < 0 || running < 0 || requested < 0 {
		return fmt.Errorf("aggregate memory inputs cannot be negative")
	}
	if running > pool || requested > pool-running {
		return fmt.Errorf("aggregate V2 memory budget exceeded: running %d MiB + requested %d MiB > pool %d MiB", running/(1024*1024), requested/(1024*1024), pool/(1024*1024))
	}
	return nil
}

func mibToBytes(field string, value int) (int64, error) {
	if value < 0 || uint64(value) > uint64(math.MaxInt64/MiB) {
		return 0, fmt.Errorf("%s is too large", field)
	}
	return int64(value) * MiB, nil
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
