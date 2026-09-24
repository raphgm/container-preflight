package checks

import (
	"context"

	"github.com/raphgm/container-preflight/internal/executor"
	"github.com/raphgm/container-preflight/pkg/report"
)

type Daemon struct {
	runner executor.Runner
}

func NewDaemon(runner executor.Runner) *Daemon {
	return &Daemon{
		runner: runner,
	}
}

func (d *Daemon) ID() string {
	return "docker.daemon"
}

func (d *Daemon) Name() string {
	return "Docker Daemon"
}

func (d *Daemon) Description() string {
	return "Checks if the Docker daemon is running."
}

func (d *Daemon) Run(ctx context.Context) report.Result {
	_, err := d.runner.Run(
		ctx,
		"docker",
		"info",
	)

	if err != nil {
		return report.Result{
			ID:             d.ID(),
			Name:           d.Name(),
			Status:         report.Fail,
			Severity:       report.Critical,
			Summary:        "Docker daemon is unavailable.",
			Recommendation: "Start Docker Desktop or the Docker daemon.",
		}
	}

	return report.Result{
		ID:       d.ID(),
		Name:     d.Name(),
		Status:   report.Pass,
		Severity: report.Info,
		Summary:  "Docker daemon is running.",
	}
}
