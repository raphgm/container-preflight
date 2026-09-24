package checks

import (
	"context"
	"strings"

	"github.com/raphgm/container-preflight/internal/executor"
	"github.com/raphgm/container-preflight/pkg/report"
)

type Version struct {
	runner executor.Runner
}

func NewVersion(runner executor.Runner) *Version {
	return &Version{
		runner: runner,
	}
}

func (v *Version) ID() string {
	return "docker.version"
}

func (v *Version) Name() string {
	return "Docker Version"
}

func (v *Version) Description() string {
	return "Checks the installed Docker client and server versions."
}

func (v *Version) Run(ctx context.Context) report.Result {
	versionInfo, err := v.runner.Run(
		ctx,
		"docker",
		"version",
		"--format",
		"Client: {{.Client.Version}}, Server: {{if .Server}}{{.Server.Version}}{{else}}unreachable{{end}}",
	)

	if err != nil {
		return report.Result{
			ID:       v.ID(),
			Name:     v.Name(),
			Status:   report.Fail,
			Severity: report.Critical,
			Summary:  "Unable to determine full Docker version.",
			Details:  strings.TrimSpace(err.Error()),
		}
	}

	return report.Result{
		ID:       v.ID(),
		Name:     v.Name(),
		Status:   report.Pass,
		Severity: report.Info,
		Summary:  "Docker version retrieved.",
		Details:  versionInfo,
	}
}
