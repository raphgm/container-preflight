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
	return "compose.installed"
}

func (i *Installed) Name() string {
	return "Docker Compose Installed"
}

func (i *Installed) Description() string {
	return "Checks if Docker Compose is installed on the system."
}

func (i *Installed) Run(ctx context.Context) report.Result {
	version, err := i.runner.Run(
		ctx,
		"docker",
		"compose",
		"version",
		"--short",
	)

	if err != nil {
		return report.Result{
			ID:             i.ID(),
			Name:           i.Name(),
			Status:         report.Fail,
			Severity:       report.Critical,
			Summary:        "Docker Compose is not installed or unavailable.",
			Recommendation: "Install Docker Compose or update your Docker Desktop installation.",
			Documentation:  "https://docs.docker.com/compose/install/",
		}
	}

	return report.Result{
		ID:       i.ID(),
		Name:     i.Name(),
		Status:   report.Pass,
		Severity: report.Info,
		Summary:  "Docker Compose is installed.",
		Details:  "Version: " + version,
	}
}
