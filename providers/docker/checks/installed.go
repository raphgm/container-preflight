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
	return "docker.installed"
}

func (i *Installed) Name() string {
	return "Docker Installed"
}

func (i *Installed) Description() string {
	return "Checks if Docker is installed on the system."
}

func (i *Installed) Run(ctx context.Context) report.Result {
	version, err := i.runner.Run(
		ctx,
		"docker",
		"version",
		"--format",
		"{{.Client.Version}}",
	)

	if err != nil {
		return report.Result{
			ID:             i.ID(),
			Name:           i.Name(),
			Status:         report.Fail,
			Severity:       report.Critical,
			Summary:        "Docker is not installed.",
			Recommendation: "Install Docker Desktop or Docker Engine.",
			Documentation:  "https://docs.docker.com/get-docker/",
		}
	}

	return report.Result{
		ID:       i.ID(),
		Name:     i.Name(),
		Status:   report.Pass,
		Severity: report.Info,
		Summary:  "Docker is installed.",
		Details:  "Version: " + version,
	}
}
