package checks

import (
	"context"
	"fmt"
	"runtime"

	"github.com/raphgm/container-preflight/pkg/report"
)

type Arch struct{}

func NewArch() *Arch {
	return &Arch{}
}

func (a *Arch) ID() string {
	return "system.arch"
}

func (a *Arch) Name() string {
	return "System Architecture"
}

func (a *Arch) Description() string {
	return "Checks the underlying operating system and architecture."
}

func (a *Arch) Run(ctx context.Context) report.Result {
	return report.Result{
		ID:       a.ID(),
		Name:     a.Name(),
		Status:   report.Pass,
		Severity: report.Info,
		Summary:  "System architecture retrieved.",
		Details:  fmt.Sprintf("OS: %s, Arch: %s", runtime.GOOS, runtime.GOARCH),
	}
}
