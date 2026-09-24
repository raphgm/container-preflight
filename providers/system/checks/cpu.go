package checks

import (
	"context"
	"fmt"
	"runtime"

	"github.com/raphgm/container-preflight/pkg/report"
)

type CPU struct{}

func NewCPU() *CPU {
	return &CPU{}
}

func (c *CPU) ID() string {
	return "system.cpu"
}

func (c *CPU) Name() string {
	return "CPU Cores"
}

func (c *CPU) Description() string {
	return "Checks the number of available CPU cores."
}

func (c *CPU) Run(ctx context.Context) report.Result {
	cores := runtime.NumCPU()

	if cores < 2 {
		return report.Result{
			ID:             c.ID(),
			Name:           c.Name(),
			Status:         report.Warn,
			Severity:       report.Warning,
			Summary:        "Low CPU core count.",
			Details:        fmt.Sprintf("Found %d core(s).", cores),
			Recommendation: "Running containers often requires at least 2 CPU cores for acceptable performance.",
		}
	}

	return report.Result{
		ID:       c.ID(),
		Name:     c.Name(),
		Status:   report.Pass,
		Severity: report.Info,
		Summary:  "Sufficient CPU cores available.",
		Details:  fmt.Sprintf("Found %d core(s).", cores),
	}
}
