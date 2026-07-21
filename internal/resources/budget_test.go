package resources

import (
	"math"
	"testing"

	"containersagents.dev/v2/internal/hostinfo"
	"containersagents.dev/v2/internal/manifest"
)

func TestBalancedBudget(t *testing.T) {
	host := hostinfo.Resources{LogicalCPUs: 16, TotalMemoryBytes: 32 * GiB}
	budget, err := Calculate("balanced", manifest.ResourceOverrides{}, manifest.MinimumResources{}, host)
	if err != nil {
		t.Fatal(err)
	}
	if budget.HostReserve != 8*GiB || budget.AggregatePool != 24*GiB {
		t.Fatalf("unexpected reserve/pool: %#v", budget)
	}
	if budget.MemoryBytes != 12*GiB || budget.CPUs != 8 || budget.BuildJobs != 2 {
		t.Fatalf("unexpected balanced budget: %#v", budget)
	}
}

func TestLowMemoryReserve(t *testing.T) {
	host := hostinfo.Resources{LogicalCPUs: 4, TotalMemoryBytes: 6 * GiB}
	budget, err := Calculate("battery", manifest.ResourceOverrides{}, manifest.MinimumResources{}, host)
	if err != nil {
		t.Fatal(err)
	}
	if budget.HostReserve != 3*GiB {
		t.Fatalf("expected 50%% reserve, got %d", budget.HostReserve)
	}
}

func TestProfileMinimumRefusesStart(t *testing.T) {
	host := hostinfo.Resources{LogicalCPUs: 4, TotalMemoryBytes: 8 * GiB}
	_, err := Calculate("battery", manifest.ResourceOverrides{}, manifest.MinimumResources{MemoryMiB: 4096}, host)
	if err == nil {
		t.Fatal("expected profile minimum error")
	}
}

func TestAggregateMemory(t *testing.T) {
	if err := CheckAggregate(10*GiB, 6*GiB, 5*GiB); err == nil {
		t.Fatal("expected aggregate budget refusal")
	}
	if err := CheckAggregate(10*GiB, 6*GiB, 4*GiB); err != nil {
		t.Fatalf("unexpected aggregate refusal: %v", err)
	}
}

func TestResourceConversionsRejectOverflow(t *testing.T) {
	host := hostinfo.Resources{LogicalCPUs: 4, TotalMemoryBytes: 8 * GiB}
	overflowMiB := int(math.MaxInt64/MiB) + 1
	_, err := Calculate("custom", manifest.ResourceOverrides{MemoryMiB: overflowMiB, CPUs: 1}, manifest.MinimumResources{}, host)
	if err == nil {
		t.Fatal("expected memory conversion overflow rejection")
	}
	if err := CheckAggregate(math.MaxInt64, math.MaxInt64-1, 2); err == nil {
		t.Fatal("expected aggregate addition overflow rejection")
	}
}
