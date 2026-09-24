package rules

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/raphgm/container-doctor/pkg/preflight/fact"
	"github.com/raphgm/container-doctor/pkg/preflight/project"
)

var buildContextRule = rule{
	id:    "build.context",
	title: "Build inputs exist in the build context",
	needs: []fact.ID{fact.ComposeFile},
	check: func(_ context.Context, env *Env) []Finding {
		var out []Finding
		for _, svc := range env.Project.Services {
			b := svc.Build
			if b == nil {
				continue
			}
			f := func(sev fact.Severity, title, fix, loc, predicts string, evidence ...string) {
				out = append(out, Finding{Rule: "build.context", Severity: sev, Service: svc.Name, Title: title, Fix: fix, Location: loc, Predicts: predicts, Evidence: evidence})
			}
			if st, err := os.Stat(b.Context); err != nil || !st.IsDir() {
				f(fact.Error, "build context "+rel(env, b.Context)+" does not exist", "Fix `build.context` in the Compose file.", svc.Location.String(), "unable to prepare context: path not found")
				continue
			}
			if b.DockerfilePath == "" {
				continue
			}
			if _, err := os.Stat(b.DockerfilePath); err != nil {
				f(fact.Error, "Dockerfile "+rel(env, b.DockerfilePath)+" does not exist", "Fix `build.dockerfile`; it is relative to the build context.", svc.Location.String(), "failed to read dockerfile: open Dockerfile: no such file or directory")
				continue
			}
			if b.ParseErr != nil {
				f(fact.Error, "Dockerfile cannot be parsed", "Fix the syntax error.", rel(env, b.DockerfilePath), "dockerfile parse error", b.ParseErr.Error())
				continue
			}
			df := b.Dockerfile
			reach, err := df.Reachable(b.Target)
			if err != nil {
				f(fact.Error, fmt.Sprintf("build target %q is not a stage in the Dockerfile", b.Target), "Fix `build.target`.", svc.Location.String(), "target stage \""+b.Target+"\" could not be found")
				continue
			}

			bc, err := project.OpenContext(b.Context, b.DockerfilePath)
			if err != nil {
				f(fact.Warning, ".dockerignore cannot be read", "", rel(env, b.Context), "", err.Error())
				continue
			}
			// The two builders word a missing COPY source differently.
			notFound := func(src string) string {
				if env.Host.BuildxInstalled && !env.Host.BuildKitDisabled {
					return `failed to compute cache key: failed to calculate checksum of ref …: "/` + strings.TrimPrefix(src, "/") + `": not found`
				}
				return "COPY failed: file not found in build context or excluded by .dockerignore: stat " + strings.TrimPrefix(src, "/") + ": file does not exist"
			}
			for _, i := range reach {
				st := df.Stages[i]
				if st.Unresolved() {
					f(fact.Error, "base image "+st.Base+" depends on an ARG with no value", "Give the ARG a default before the first FROM, or pass it in `build.args`.", st.Location.String(), "invalid reference format")
				}
				for _, cp := range st.Copies {
					for _, src := range cp.Sources {
						status, pattern := bc.Check(src)
						switch status {
						case project.SourceMissing:
							f(fact.Error, fmt.Sprintf("%s source %q not found in build context", cp.Instruction, src),
								"Create the file, fix the path (it is relative to "+rel(env, b.Context)+"), or run the step that generates it first.",
								cp.Location.String(), notFound(src),
								"context: "+rel(env, b.Context))
						case project.SourceIgnored:
							f(fact.Error, fmt.Sprintf("%s source %q is excluded by .dockerignore", cp.Instruction, src),
								fmt.Sprintf("Remove or negate the pattern %q in %s.", pattern, rel(env, bc.IgnoreFile)),
								cp.Location.String(), notFound(src),
								fmt.Sprintf("%s: pattern %q", rel(env, bc.IgnoreFile), pattern))
						case project.SourceOutside:
							f(fact.Error, fmt.Sprintf("%s source %q is outside the build context", cp.Instruction, src),
								"Move the file into the context, widen `build.context`, or use `build.additional_contexts`.",
								cp.Location.String(), "forbidden path outside the build context")
						}
					}
				}
			}
		}
		return out
	},
}

var buildKitRule = rule{
	id:    "build.buildkit",
	title: "Builder supports the Dockerfile syntax",
	needs: []fact.ID{fact.DockerCLI, fact.ComposeFile},
	check: func(_ context.Context, env *Env) []Finding {
		h := env.Host
		if h.BuildxInstalled && !h.BuildKitDisabled {
			return nil
		}
		reason := "host: docker buildx plugin is not installed, so `docker build` falls back to the legacy builder"
		fix := "Install the buildx plugin (`docker-buildx-plugin` on Linux; bundled with Docker Desktop)."
		if h.BuildKitDisabled {
			reason = "host: DOCKER_BUILDKIT=0 forces the legacy builder"
			fix = "Unset DOCKER_BUILDKIT."
		}
		var out []Finding
		for _, svc := range env.Project.Services {
			if svc.Build == nil || svc.Build.Dockerfile == nil {
				continue
			}
			for _, feat := range svc.Build.Dockerfile.BuildKitFeatures {
				predicts := ""
				if strings.Contains(feat.Name, "--") {
					predicts = "the --" + lastWord(feat.Name) + " option requires BuildKit. Refer to https://docs.docker.com/go/buildkit/ to learn how to build images with BuildKit enabled"
				}
				out = append(out, Finding{
					Rule:     "build.buildkit",
					Severity: fact.Error,
					Service:  svc.Name,
					Title:    feat.Name + " requires BuildKit",
					Evidence: []string{reason},
					Fix:      fix,
					Location: feat.Location.String(),
					Predicts: predicts,
				})
			}
		}
		return out
	},
}

var composeFeaturesRule = rule{
	id:    "compose.version",
	title: "Installed Compose understands the Compose file",
	needs: []fact.ID{fact.Compose},
	check: func(_ context.Context, env *Env) []Finding {
		have := env.Host.ComposeVersion
		var out []Finding
		if strings.HasPrefix(have, "1.") {
			out = append(out, Finding{
				Rule:     "compose.version",
				Severity: fact.Warning,
				Title:    "only legacy docker-compose " + have + " is installed",
				Evidence: []string{"host: `docker compose` (v2) is missing; found docker-compose " + have},
				Fix:      "Install the Compose v2 plugin; v1 has been end-of-life since 2023.",
			})
		}
		for _, feat := range env.Project.ComposeFeatures {
			if versionLess(have, feat.MinVersion) {
				out = append(out, Finding{
					Rule:     "compose.version",
					Severity: fact.Error,
					Title:    fmt.Sprintf("`%s` needs Compose %s or newer", feat.Name, feat.MinVersion),
					Evidence: []string{"host: Compose " + have},
					Fix:      "Upgrade Docker Compose (or Docker Desktop).",
					Location: feat.Location.String(),
				})
			}
		}
		return out
	},
}

var envRule = rule{
	id:    "compose.env",
	title: "Interpolated variables have values",
	check: func(_ context.Context, env *Env) []Finding {
		var out []Finding
		seen := map[string]bool{}
		for _, ref := range env.Project.EnvRefs {
			if _, set := env.Project.Env[ref.Name]; set || ref.HasDefault || seen[ref.Name] {
				continue
			}
			seen[ref.Name] = true
			if ref.Required {
				out = append(out, Finding{
					Rule:     "compose.env",
					Severity: fact.Error,
					Title:    "required variable " + ref.Name + " is not set",
					Evidence: []string{"project: ${" + ref.Name + ":?…} has no value in the environment or .env"},
					Fix:      "Set " + ref.Name + " in .env or the shell.",
					Location: ref.Location.String(),
					Predicts: "required variable " + ref.Name + " is missing a value",
				})
				continue
			}
			out = append(out, Finding{
				Rule:     "compose.env",
				Severity: fact.Warning,
				Title:    "variable " + ref.Name + " is not set and defaults to an empty string",
				Evidence: []string{"project: $" + ref.Name + " not found in the environment or .env"},
				Fix:      "Set it in .env, or write ${" + ref.Name + ":-default}.",
				Location: ref.Location.String(),
				Predicts: `The "` + ref.Name + `" variable is not set. Defaulting to a blank string.`,
			})
		}
		for _, ef := range env.Project.EnvFiles {
			if _, err := os.Stat(ef.Path); err != nil && !ef.Required {
				out = append(out, Finding{
					Rule:     "compose.env",
					Severity: fact.Info,
					Service:  ef.Service,
					Title:    "optional env_file " + rel(env, ef.Path) + " is missing",
					Fix:      "Create it if the service needs those variables.",
				})
			}
		}
		return out
	},
}

func rel(env *Env, path string) string {
	if r, err := filepath.Rel(env.Project.Dir, path); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return path
}

func lastWord(s string) string {
	s = s[strings.LastIndex(s, " ")+1:]
	return strings.TrimPrefix(s, "--")
}

// versionLess compares dotted versions numerically, ignoring suffixes such
// as "-desktop.1".
func versionLess(a, b string) bool {
	pa, pb := versionParts(a), versionParts(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

func versionParts(v string) [3]int {
	var out [3]int
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	for i, p := range strings.SplitN(v, ".", 3) {
		out[i], _ = strconv.Atoi(p)
	}
	return out
}
