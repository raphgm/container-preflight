package checks

import (
	"context"
	"fmt"

	"github.com/shirou/gopsutil/v3/disk"
	"github.com/raphgm/container-preflight/pkg/report"
)

type Disk struct{}

func NewDisk() *Disk {
	return &Disk{}
}

func (d *Disk) ID() string {
	return "system.disk"
}

func (d *Disk) Name() string {
	return "Disk Space"
}

func (d *Disk) Description() string {
	return "Checks for sufficient free disk space on the primary partition."
}

func (d *Disk) Run(ctx context.Context) report.Result {
	usage, err := disk.Usage("/")
	if err != nil {
		return report.Result{
			ID:       d.ID(),
			Name:     d.Name(),
			Status:   report.Fail,
			Severity: report.Critical,
			Summary:  "Failed to retrieve disk usage info.",
			Details:  err.Error(),
		}
	}

	freeGB := float64(usage.Free) / (1024 * 1024 * 1024)

	if freeGB < 5.0 {
		return report.Result{
			ID:             d.ID(),
			Name:           d.Name(),
			Status:         report.Fail,
			Severity:       report.Critical,
			Summary:        "Critically low disk space.",
			Details:        fmt.Sprintf("Only %.2f GB of free space available on '/'.", freeGB),
			Recommendation: "Free up at least 5 GB of disk space. Containers and images require significant storage.",
		}
	}

	return report.Result{
		ID:       d.ID(),
		Name:     d.Name(),
		Status:   report.Pass,
		Severity: report.Info,
		Summary:  "Sufficient free disk space.",
		Details:  fmt.Sprintf("Found %.2f GB of free space.", freeGB),
	}
}
