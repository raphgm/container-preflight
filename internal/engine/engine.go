package engine

import (
	"context"
	"time"

	"github.com/raphgm/container-preflight/pkg/report"
)

type Engine struct {
	registry *Registry
}

func New(registry *Registry) *Engine {
	return &Engine{
		registry: registry,
	}
}

func (e *Engine) Run(ctx context.Context) report.Report {
	start := time.Now()

	var providers []report.ProviderReport

	for _, p := range e.registry.Providers() {
		pr := report.ProviderReport{
			Name: p.Name(),
		}

		for _, c := range p.Checks() {
			pr.Results = append(
				pr.Results,
				c.Run(ctx),
			)
		}

		providers = append(
			providers,
			pr,
		)
	}

	return report.Report{
		Timestamp: time.Now(),
		Duration:  time.Since(start),
		Providers: providers,
	}
}
