package checks

import (
	"context"

	"github.com/raphgm/container-preflight/internal/executor"
	"github.com/raphgm/container-preflight/pkg/report"
)

type Installed struct {
	runner executor.Runner
}

func NewInstalled(runner executor.Runner) *Installed {
	return &Installed{
		runner: runner,
	}
}

func (i *Installed) ID() string {
	return "buildx.installed"
}

func (i *Installed) Name() string {
	return "Buildx Installed"
}

func (i *Installed) Description() string {
	return "Checks if Docker Buildx is installed."
}

func (i *Installed) Run(ctx context.Context) report.Result {
	version, err := i.runner.Run(
		ctx,
		"docker",
		"buildx",
		"version",
	)

	if err != nil {
		return report.Result{
			ID:             i.ID(),
			Name:           i.Name(),
			Status:         report.Fail,
			Severity:       report.Critical,
			Summary:        "Buildx is not available.",
			Recommendation: "Update your Docker installation to include Buildx.",
		}
	}

	return report.Result{
		ID:       i.ID(),
		Name:     i.Name(),
		Status:   report.Pass,
		Severity: report.Info,
		Summary:  "Buildx is installed.",
		Details:  version,
	}
}
