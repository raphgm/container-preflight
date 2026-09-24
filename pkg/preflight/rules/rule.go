// Package rules holds the preflight rules. Each rule joins a project
// requirement with a host capability and predicts a concrete failure,
// quoting the evidence from both sides.
package rules

import (
	"context"
	"fmt"
	"strings"

	"github.com/raphgm/container-preflight/pkg/preflight/fact"
	"github.com/raphgm/container-preflight/pkg/preflight/host"
	"github.com/raphgm/container-preflight/pkg/preflight/project"
	"github.com/raphgm/container-preflight/pkg/preflight/registry"
)

// Env is everything a rule may read.
type Env struct {
	Project *project.Project
	Host    *host.Profile
	Facts   *fact.Set

	// Images holds registry lookups keyed by ImageKey. It is filled
	// before rules run.
	Images map[string]ImageResult
}

type ImageResult struct {
	Image *registry.Image
	Err   error
}

func ImageKey(ref, platform string) string {
	return registry.Canonical(ref) + "|" + registry.Normalize(platform)
}

// Finding is one predicted failure (or risk), with the evidence behind it.
type Finding struct {
	Rule     string        `json:"rule"`
	Severity fact.Severity `json:"severity"`
	Service  string        `json:"service,omitempty"`
	Title    string        `json:"title"`
	Evidence []string      `json:"evidence,omitempty"`
	Fix      string        `json:"fix,omitempty"`
	Location string        `json:"location,omitempty"`

	// Predicts is the error Docker would print, when known. It links the
	// finding to real-world failure reports.
	Predicts string `json:"predicts,omitempty"`
}

// Rule checks one kind of project/host mismatch.
type Rule interface {
	ID() string
	Title() string
	// Needs lists the facts that must be known for the rule to run.
	Needs() []fact.ID
	Check(ctx context.Context, env *Env) []Finding
}

type rule struct {
	id    string
	title string
	needs []fact.ID
	check func(ctx context.Context, env *Env) []Finding
}

func (r rule) ID() string                                    { return r.id }
func (r rule) Title() string                                 { return r.title }
func (r rule) Needs() []fact.ID                              { return r.needs }
func (r rule) Check(ctx context.Context, env *Env) []Finding { return r.check(ctx, env) }

// All returns every rule in report order.
func All() []Rule {
	return []Rule{
		imageExistsRule,
		imagePlatformRule,
		credentialsRule,
		buildContextRule,
		buildKitRule,
		composeFeaturesRule,
		envRule,
		memoryRule,
		diskRule,
		hostDiskRule,
		installerRule,
		gpuRule,
		sysctlRule,
		portConflictRule,
		hostPortRule,
		bindRule,
		permissionsRule,
		fileSharingRule,
	}
}

// hostPlatform is the daemon's platform, or this machine's when the daemon
// is unknown (they match for local Docker Desktop, Colima and Engine).
func hostPlatform(h *host.Profile) string {
	if h.Platform != "" {
		return h.Platform
	}
	return "linux/" + h.ClientArch
}

// Pull is an image Docker will fetch for a service: either the service image
// or a base image of a build stage that BuildKit will actually build.
type Pull struct {
	Service  string
	Ref      string
	Platform string
	Explicit bool
	Base     bool
	Location project.Location

	// Local is true when the image is already in the daemon, so Compose
	// will not pull it. Its platform still matters.
	Local bool
}

// Pulls lists every image the project uses, for the platform Docker will
// request.
func Pulls(p *project.Project, h *host.Profile) []Pull {
	var out []Pull
	seen := map[string]bool{}
	add := func(pl Pull) {
		pl.Local = h.LocalImages[registry.Canonical(pl.Ref)]
		key := pl.Service + "|" + ImageKey(pl.Ref, pl.Platform)
		if !seen[key] {
			seen[key] = true
			out = append(out, pl)
		}
	}
	native := hostPlatform(h)

	for _, svc := range p.Services {
		targets := []string{native}
		explicit := false
		if svc.Platform != "" {
			targets, explicit = []string{svc.Platform}, true
		} else if svc.Build != nil && len(svc.Build.Platforms) > 0 {
			targets, explicit = svc.Build.Platforms, true
		}

		if svc.Image != "" {
			for _, t := range targets {
				add(Pull{Service: svc.Name, Ref: svc.Image, Platform: t, Explicit: explicit, Location: svc.Location})
			}
		}
		if svc.Build == nil || svc.Build.Dockerfile == nil {
			continue
		}
		df := svc.Build.Dockerfile
		reach, err := df.Reachable(svc.Build.Target)
		if err != nil {
			continue
		}
		for _, i := range reach {
			st := df.Stages[i]
			if st.BaseStage >= 0 || strings.EqualFold(st.Base, "scratch") || st.Unresolved() {
				continue
			}
			for _, t := range targets {
				pl, exp := t, explicit
				switch {
				case strings.Contains(st.Platform, "BUILDPLATFORM"):
					pl, exp = native, true
				case strings.Contains(st.Platform, "TARGETPLATFORM"):
				case st.Platform != "" && !strings.Contains(st.Platform, "$"):
					pl, exp = st.Platform, true
				}
				add(Pull{Service: svc.Name, Ref: st.Base, Platform: pl, Explicit: exp, Base: true, Location: st.Location})
			}
		}
	}
	return out
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
