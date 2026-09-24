package checks

import (
	"context"

	"github.com/raphgm/container-preflight/internal/executor"
	"github.com/raphgm/container-preflight/pkg/report"
)

type Context struct {
	runner executor.Runner
}

func NewContext(runner executor.Runner) *Context {
	return &Context{
		runner: runner,
	}
}

func (c *Context) ID() string {
	return "docker.context"
}

func (c *Context) Name() string {
	return "Docker Context"
}

func (c *Context) Description() string {
	return "Checks the currently active Docker context."
}

func (c *Context) Run(ctx context.Context) report.Result {
	contextName, err := c.runner.Run(
		ctx,
		"docker",
		"context",
		"show",
	)

	if err != nil {
		return report.Result{
			ID:       c.ID(),
			Name:     c.Name(),
			Status:   report.Fail,
			Severity: report.Critical,
			Summary:  "Unable to determine Docker context.",
		}
	}

	return report.Result{
		ID:       c.ID(),
		Name:     c.Name(),
		Status:   report.Pass,
		Severity: report.Info,
		Summary:  "Current Docker context.",
		Details:  contextName,
	}
}
