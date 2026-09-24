package renderer

import "github.com/raphgm/container-preflight/pkg/report"

type Renderer interface {
	Render(report.Report) error
}
