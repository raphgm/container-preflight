package checks

import (
	"context"
	"strings"

	"github.com/raphgm/container-preflight/internal/executor"
	"github.com/raphgm/container-preflight/pkg/report"
)

type Socket struct {
	runner executor.Runner
}

func NewSocket(runner executor.Runner) *Socket {
	return &Socket{
		runner: runner,
	}
}

func (s *Socket) ID() string {
	return "docker.socket"
}

func (s *Socket) Name() string {
	return "Docker Socket"
}

func (s *Socket) Description() string {
	return "Checks if the Docker socket is accessible."
}

func (s *Socket) Run(ctx context.Context) report.Result {
	// A simple docker ps helps verify if the socket permissions are correct
	_, err := s.runner.Run(
		ctx,
		"docker",
		"ps",
	)

	if err != nil {
		errorStr := err.Error()
		
		if strings.Contains(errorStr, "permission denied") {
			return report.Result{
				ID:             s.ID(),
				Name:           s.Name(),
				Status:         report.Fail,
				Severity:       report.Critical,
				Summary:        "Permission denied while accessing the Docker socket.",
				Recommendation: "Ensure your user is added to the 'docker' group or run with sudo.",
			}
		}

		return report.Result{
			ID:       s.ID(),
			Name:     s.Name(),
			Status:   report.Fail,
			Severity: report.Critical,
			Summary:  "Unable to access the Docker socket.",
			Details:  strings.TrimSpace(errorStr),
		}
	}

	return report.Result{
		ID:       s.ID(),
		Name:     s.Name(),
		Status:   report.Pass,
		Severity: report.Info,
		Summary:  "Docker socket is accessible.",
	}
}
