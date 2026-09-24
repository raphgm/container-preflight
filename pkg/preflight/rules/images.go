package rules

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/raphgm/container-doctor/pkg/preflight/fact"
	"github.com/raphgm/container-doctor/pkg/preflight/registry"
)

func pullKind(p Pull) string {
	if p.Base {
		return "base image"
	}
	return "image"
}

var imageExistsRule = rule{
	id:    "image.exists",
	title: "Images exist in their registries",
	needs: []fact.ID{fact.Registry, fact.ComposeFile},
	check: func(_ context.Context, env *Env) []Finding {
		var out []Finding
		reported := map[string]bool{}
		for _, p := range Pulls(env.Project, env.Host) {
			res, ok := env.Images[ImageKey(p.Ref, p.Platform)]
			if p.Local || !ok || res.Err == nil || reported[p.Service+p.Ref] {
				continue
			}
			reported[p.Service+p.Ref] = true
			switch {
			case errors.Is(res.Err, registry.ErrNotFound):
				out = append(out, Finding{
					Rule:     "image.exists",
					Severity: fact.Error,
					Service:  p.Service,
					Title:    fmt.Sprintf("%s %s does not exist", pullKind(p), p.Ref),
					Evidence: []string{"registry: " + firstLine(res.Err)},
					Fix:      "Check the tag on the registry's web page; tags are often renamed or removed.",
					Location: p.Location.String(),
					Predicts: "failed to resolve reference \"" + strings.Replace(registry.Canonical(p.Ref), "index.docker.io/", "docker.io/", 1) + "\": not found",
				})
			case errors.Is(res.Err, registry.ErrUnauthorized):
				out = append(out, Finding{
					Rule:     "image.exists",
					Severity: fact.Warning,
					Service:  p.Service,
					Title:    fmt.Sprintf("%s %s is private or does not exist", pullKind(p), p.Ref),
					Evidence: []string{"registry: " + firstLine(res.Err), "Docker Hub gives the same answer for private and missing repositories"},
					Fix:      "Check the name, or `docker login` to the registry that hosts it.",
					Location: p.Location.String(),
					Predicts: "pull access denied, repository does not exist or may require 'docker login'",
				})
			}
		}
		return out
	},
}

var imagePlatformRule = rule{
	id:    "image.platform",
	title: "Images ship the platform this host runs",
	needs: []fact.ID{fact.Daemon, fact.Registry, fact.ComposeFile},
	check: func(_ context.Context, env *Env) []Finding {
		var out []Finding
		h := env.Host
		for _, p := range Pulls(env.Project, h) {
			res, ok := env.Images[ImageKey(p.Ref, p.Platform)]
			if !ok || res.Err != nil {
				continue
			}
			img := res.Image
			want := registry.Normalize(p.Platform)
			evidence := []string{
				fmt.Sprintf("host: daemon runs %s (%s)", h.Platform, h.OperatingSystem),
				fmt.Sprintf("registry: %s ships %s", p.Ref, strings.Join(img.Platforms, ", ")),
			}
			if p.Explicit {
				evidence = append(evidence, "project: platform "+want+" requested explicitly")
			}

			available := false
			for _, pl := range img.Platforms {
				if registry.Compatible(pl, want) {
					available = true
				}
			}

			if img.Index && !available {
				fix := "Use an image or tag that publishes " + want + "."
				if !p.Explicit {
					for _, pl := range img.Platforms {
						if _, emu := h.CanRun(pl); emu {
							fix = fmt.Sprintf("Add `platform: %s` to run it under emulation, or use an image that publishes %s.", pl, want)
							break
						}
					}
				}
				out = append(out, Finding{
					Rule:     "image.platform",
					Severity: fact.Error,
					Service:  p.Service,
					Title:    fmt.Sprintf("%s %s has no %s variant", pullKind(p), p.Ref, want),
					Evidence: evidence,
					Fix:      fix,
					Location: p.Location.String(),
					Predicts: "no matching manifest for " + want + " in the manifest list entries",
				})
				continue
			}

			// The image will be fetched for its own platform; it must be
			// runnable here.
			runAs := want
			if !available && len(img.Platforms) == 1 {
				runAs = img.Platforms[0]
			}
			native, emulated := h.CanRun(runAs)
			switch {
			case native:
			case !emulated && !h.EmulationKnown:
				out = append(out, Finding{
					Rule:     "image.platform",
					Severity: fact.Warning,
					Service:  p.Service,
					Title:    fmt.Sprintf("%s %s is %s only; could not tell whether this host emulates it", pullKind(p), p.Ref, runAs),
					Evidence: append(evidence, "host: the daemon's kernel could not be inspected for binfmt handlers"),
					Fix:      "Check with `docker run --rm --platform " + runAs + " alpine uname -m`, or use an image built for " + h.Platform + ".",
					Location: p.Location.String(),
					Predicts: "exec format error (if no emulation)",
				})
			case emulated && h.Emulators[registry.Normalize(runAs)] == "qemu" && qemuCrash(p.Ref) != "":
				out = append(out, Finding{
					Rule:     "image.platform",
					Severity: fact.Error,
					Service:  p.Service,
					Title:    fmt.Sprintf("%s %s crashes under QEMU %s emulation", pullKind(p), p.Ref, runAs),
					Evidence: append(evidence, "host: "+runAs+" is emulated with QEMU (no Rosetta)", "known: this image does not run under QEMU user-mode emulation"),
					Fix:      "Use a native " + h.Platform + " alternative, enable Rosetta (`colima start --vz-rosetta`, or Docker Desktop's Rosetta setting), or run on an amd64 host.",
					Location: p.Location.String(),
					Predicts: qemuCrash(p.Ref),
				})
			case emulated:
				out = append(out, Finding{
					Rule:     "image.platform",
					Severity: fact.Warning,
					Service:  p.Service,
					Title:    fmt.Sprintf("%s %s will run under %s emulation", pullKind(p), p.Ref, runAs),
					Evidence: append(evidence, "host: can emulate "+strings.Join(h.Emulated, ", ")),
					Fix:      "Expect it to be several times slower; JIT runtimes and databases can crash under emulation. Prefer a native " + h.Platform + " image.",
					Location: p.Location.String(),
					Predicts: "The requested image's platform (" + runAs + ") does not match the detected host platform (" + h.Platform + ")",
				})
			default:
				out = append(out, Finding{
					Rule:     "image.platform",
					Severity: fact.Error,
					Service:  p.Service,
					Title:    fmt.Sprintf("%s %s is %s only and this host cannot run it", pullKind(p), p.Ref, runAs),
					Evidence: append(evidence, "host: no emulation for "+runAs),
					Fix:      "Install QEMU binfmt handlers (`docker run --privileged --rm tonistiigi/binfmt --install all`) or use an image built for " + h.Platform + ".",
					Location: p.Location.String(),
					Predicts: "exec format error",
				})
			}
		}
		return out
	},
}

var credentialsRule = rule{
	id:    "registry.credentials",
	title: "Docker credential helper works",
	needs: []fact.ID{fact.DockerCLI},
	check: func(_ context.Context, env *Env) []Finding {
		helper := env.Host.MissingCredentialHelper
		if helper == "" {
			return nil
		}
		return []Finding{{
			Rule:     "registry.credentials",
			Severity: fact.Error,
			Title:    "Docker's configured credential helper " + helper + " is not installed",
			Evidence: []string{"host: ~/.docker/config.json names " + helper + ", which is not on PATH"},
			Fix:      "Install the helper, or remove the \"credsStore\" entry from ~/.docker/config.json (common after uninstalling Docker Desktop).",
			Predicts: "error getting credentials - err: exec: \"" + helper + "\": executable file not found in $PATH",
		}}
	},
}

func qemuCrash(ref string) string {
	for _, k := range knownFor(ref) {
		if k.qemuCrash != "" {
			return k.qemuCrash
		}
	}
	return ""
}

func firstLine(err error) string {
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
