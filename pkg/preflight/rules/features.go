package rules

import (
	"sort"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"

	"github.com/raphgm/container-doctor/pkg/preflight/host"
	"github.com/raphgm/container-doctor/pkg/preflight/project"
	"github.com/raphgm/container-doctor/pkg/preflight/registry"
)

// Features describes one service on one host as flat key/value pairs.
// Learned rules are conjunctions over these keys, so the vocabulary is kept
// small and host-independent where possible (buckets, not exact numbers).
func Features(env *Env, svc *project.Service) map[string]string {
	h := env.Host
	f := map[string]string{
		"host.platform": hostPlatform(h),
		"engine":        engineKind(h),
		"memory":        memoryBucket(h.MemTotal),
		"buildkit":      yesNo(h.BuildxInstalled && !h.BuildKitDisabled),
	}
	if v := h.ComposeVersion; v != "" {
		f["compose.major"] = strings.SplitN(v, ".", 2)[0]
	}

	f["build"] = yesNo(svc.Build != nil)

	// Environment variables are recorded as set/empty, never their values:
	// learned rules are shared and must not leak secrets. A variable that is
	// absent reads as "unset" (see learn).
	for k, v := range svc.Environment {
		if v == "" {
			f["env."+k] = "empty"
		} else {
			f["env."+k] = "set"
		}
	}

	ref := serviceImage(svc)
	if ref == "" {
		return f
	}
	f["image"] = repository(ref)

	want := hostPlatform(h)
	if svc.Platform != "" {
		want = svc.Platform
	}
	runAs := registry.Normalize(want)
	if res, ok := env.Images[ImageKey(ref, want)]; ok && res.Image != nil {
		ps := append([]string(nil), res.Image.Platforms...)
		sort.Strings(ps)
		f["image.platforms"] = strings.Join(ps, ",")
		available := false
		for _, p := range ps {
			if registry.Compatible(p, want) {
				available = true
			}
		}
		if !available && len(ps) == 1 {
			runAs = ps[0]
		}
	}
	f["run.platform"] = runAs

	switch native, emulated := h.CanRun(runAs); {
	case native:
		f["emulation"] = "none"
	case emulated && h.Emulators[runAs] != "":
		f["emulation"] = h.Emulators[runAs]
	case emulated:
		f["emulation"] = "yes"
	case h.EmulationKnown:
		f["emulation"] = "unavailable"
	default:
		f["emulation"] = "unknown"
	}
	return f
}

// serviceImage is the image the service's containers run: its image, or the
// base of the final build stage.
func serviceImage(svc *project.Service) string {
	if svc.Image != "" {
		return svc.Image
	}
	if svc.Build == nil || svc.Build.Dockerfile == nil || len(svc.Build.Dockerfile.Stages) == 0 {
		return ""
	}
	df := svc.Build.Dockerfile
	reach, err := df.Reachable(svc.Build.Target)
	if err != nil || len(reach) == 0 {
		return ""
	}
	st := df.Stages[reach[len(reach)-1]]
	for st.BaseStage >= 0 {
		st = df.Stages[st.BaseStage]
	}
	if st.Unresolved() || strings.EqualFold(st.Base, "scratch") {
		return ""
	}
	return st.Base
}

// repository strips the tag or digest, so a rule learned on mongo:7 also
// covers mongo:7.0.14.
func repository(ref string) string {
	parsed, err := name.ParseReference(ref)
	if err != nil {
		return ref
	}
	return parsed.Context().Name()
}

func engineKind(h *host.Profile) string {
	switch {
	case h.OperatingSystem == "":
		return "unknown"
	case h.IsDesktop():
		return "docker-desktop"
	case strings.Contains(h.Endpoint, "/.colima/"):
		return "colima"
	case strings.Contains(h.Endpoint, "/.orbstack/") || strings.Contains(h.OperatingSystem, "OrbStack"):
		return "orbstack"
	case !h.Local:
		return "remote"
	}
	return "linux"
}

func memoryBucket(n int64) string {
	const gib = 1 << 30
	switch {
	case n == 0:
		return "unknown"
	case n < 2*gib:
		return "<2GiB"
	case n < 4*gib:
		return "<4GiB"
	case n < 8*gib:
		return "<8GiB"
	}
	return ">=8GiB"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
