package checks

import (
	"context"
	"fmt"

	"github.com/shirou/gopsutil/v3/mem"
	"github.com/raphgm/container-preflight/pkg/report"
)

type Memory struct{}

func NewMemory() *Memory {
	return &Memory{}
}

func (m *Memory) ID() string {
	return "system.memory"
}

func (m *Memory) Name() string {
	return "System Memory"
}

func (m *Memory) Description() string {
	return "Checks the total available system memory (RAM)."
}

func (m *Memory) Run(ctx context.Context) report.Result {
	v, err := mem.VirtualMemory()
	if err != nil {
		return report.Result{
			ID:       m.ID(),
			Name:     m.Name(),
			Status:   report.Fail,
			Severity: report.Critical,
			Summary:  "Failed to retrieve system memory info.",
			Details:  err.Error(),
		}
	}

	totalGB := float64(v.Total) / (1024 * 1024 * 1024)

	if totalGB < 2.0 {
		return report.Result{
			ID:             m.ID(),
			Name:           m.Name(),
			Status:         report.Warn,
			Severity:       report.Warning,
			Summary:        "Low system memory.",
			Details:        fmt.Sprintf("Found %.2f GB of RAM.", totalGB),
			Recommendation: "Running containers typically requires at least 2 GB of RAM for stable performance.",
		}
	}

	return report.Result{
		ID:       m.ID(),
		Name:     m.Name(),
		Status:   report.Pass,
		Severity: report.Info,
		Summary:  "Sufficient system memory.",
		Details:  fmt.Sprintf("Found %.2f GB of RAM.", totalGB),
	}
}
