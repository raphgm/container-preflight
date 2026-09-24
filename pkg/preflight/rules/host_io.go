package rules

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/raphgm/container-preflight/pkg/preflight/fact"
)

type portUse struct {
	service string
	loc     string
}

var portConflictRule = rule{
	id:    "ports.conflict",
	title: "Services do not publish the same host port",
	needs: []fact.ID{fact.ComposeFile},
	check: func(_ context.Context, env *Env) []Finding {
		var out []Finding
		used := map[string]portUse{}
		for _, svc := range env.Project.Services {
			for _, p := range svc.Ports {
				width := p.End - p.Start + 1
				if svc.Replicas > 1 && width < svc.Replicas {
					out = append(out, Finding{
						Rule:     "ports.conflict",
						Severity: fact.Error,
						Service:  svc.Name,
						Title:    fmt.Sprintf("%d replicas cannot all publish host port %s", svc.Replicas, p.String()),
						Evidence: []string{"project: each replica binds the same host port"},
						Fix:      fmt.Sprintf("Publish a range at least %d wide (e.g. \"%d-%d:%d\"), drop the host port, or put a proxy in front.", svc.Replicas, p.Start, p.Start+svc.Replicas-1, p.Target),
						Location: svc.Location.String(),
						Predicts: "Bind for 0.0.0.0:" + fmt.Sprint(p.Start) + " failed: port is already allocated",
					})
				}
				for port := p.Start; port <= p.End; port++ {
					key := fmt.Sprintf("%s:%d/%s", p.HostIP, port, p.Protocol)
					if prev, ok := used[key]; ok && prev.service != svc.Name {
						out = append(out, Finding{
							Rule:     "ports.conflict",
							Severity: fact.Error,
							Service:  svc.Name,
							Title:    fmt.Sprintf("host port %d/%s is published by both %s and %s", port, p.Protocol, prev.service, svc.Name),
							Fix:      "Give one of them a different host port.",
							Location: svc.Location.String(),
							Predicts: fmt.Sprintf("Bind for 0.0.0.0:%d failed: port is already allocated", port),
						})
						break
					}
					used[key] = portUse{svc.Name, svc.Location.String()}
				}
			}
		}
		return out
	},
}

var hostPortRule = rule{
	id:    "ports.host",
	title: "Published ports are free on the host",
	needs: []fact.ID{fact.LocalDaemon, fact.ComposeFile},
	check: func(_ context.Context, env *Env) []Finding {
		h := env.Host
		unprivStart := int64(1024)
		if v, ok := h.Sysctls["net.ipv4.ip_unprivileged_port_start"]; ok {
			unprivStart = v
		}
		var out []Finding
		busy := map[string]int{} // port -> index in out
		for _, svc := range env.Project.Services {
			for _, p := range svc.Ports {
				if h.Rootless && int64(p.Start) < unprivStart {
					out = append(out, Finding{
						Rule:     "ports.host",
						Severity: fact.Error,
						Service:  svc.Name,
						Title:    fmt.Sprintf("rootless Docker cannot publish privileged port %d", p.Start),
						Evidence: []string{"host: daemon runs rootless", fmt.Sprintf("host: net.ipv4.ip_unprivileged_port_start = %d", unprivStart)},
						Fix:      "Publish a port ≥ 1024, or `sudo sysctl net.ipv4.ip_unprivileged_port_start=0`.",
						Location: svc.Location.String(),
						Predicts: "cannot expose privileged port " + fmt.Sprint(p.Start),
					})
				}
				for port := p.Start; port <= p.End; port++ {
					key := fmt.Sprintf("%d/%s", port, p.Protocol)
					if i, ok := busy[key]; ok {
						out[i].Service += ", " + svc.Name
						break
					}
					holder, c := h.PortHolder(p.HostIP, port, p.Protocol)
					if holder == "" {
						continue
					}
					// Compose replaces its own containers on `up`.
					if c != nil && c.ComposeProject == env.Project.Name {
						continue
					}
					out = append(out, Finding{
						Rule:     "ports.host",
						Severity: fact.Error,
						Service:  svc.Name,
						Title:    fmt.Sprintf("host port %d/%s is already in use by %s", port, p.Protocol, holder),
						Evidence: []string{fmt.Sprintf("host: %s is listening on %d", holder, port)},
						Fix:      "Stop it, or publish a different host port.",
						Location: svc.Location.String(),
						Predicts: fmt.Sprintf("Bind for 0.0.0.0:%d failed: port is already allocated / address already in use", port),
					})
					busy[key] = len(out) - 1
					break
				}
			}
		}
		return out
	},
}

var bindRule = rule{
	id:    "mounts.bind",
	title: "Bind-mount sources exist and have the right type",
	needs: []fact.ID{fact.LocalDaemon, fact.ComposeFile},
	check: func(_ context.Context, env *Env) []Finding {
		var out []Finding
		for _, svc := range env.Project.Services {
			for _, b := range svc.Binds {
				wantsFile := looksLikeFile(b.Target)
				st, err := os.Stat(b.Source)
				src := rel(env, b.Source)
				switch {
				case err != nil && !b.CreateHostPath:
					out = append(out, Finding{
						Rule: "mounts.bind", Severity: fact.Error, Service: svc.Name,
						Title:    "bind source " + src + " does not exist",
						Evidence: []string{"project: create_host_path: false"},
						Fix:      "Create " + src + " before starting.",
						Location: svc.Location.String(),
						Predicts: "invalid mount config for type \"bind\": bind source path does not exist",
					})
				case err != nil && wantsFile:
					out = append(out, Finding{
						Rule: "mounts.bind", Severity: fact.Error, Service: svc.Name,
						Title:    fmt.Sprintf("bind source %s is missing; Docker will create a directory where %s expects a file", src, b.Target),
						Evidence: []string{"host: " + src + " does not exist", "project: target " + b.Target + " looks like a file"},
						Fix:      "Create the file " + src + " (check the path is relative to the Compose file).",
						Location: svc.Location.String(),
						Predicts: "Are you trying to mount a directory onto a file (or vice-versa)?",
					})
				case err != nil:
					out = append(out, Finding{
						Rule: "mounts.bind", Severity: fact.Warning, Service: svc.Name,
						Title:    "bind source " + src + " does not exist and will be created empty, owned by root",
						Fix:      "Create it yourself if the container expects content or write access.",
						Location: svc.Location.String(),
					})
				case st.IsDir() && wantsFile && isEmptyDir(b.Source):
					out = append(out, Finding{
						Rule: "mounts.bind", Severity: fact.Error, Service: svc.Name,
						Title:    fmt.Sprintf("%s is an empty directory but %s expects a file", src, b.Target),
						Evidence: []string{"host: " + src + " is an empty directory, typically left by an earlier run when the file was missing"},
						Fix:      "Delete the empty directory " + src + " and create the file.",
						Location: svc.Location.String(),
						Predicts: "Are you trying to mount a directory onto a file (or vice-versa)?",
					})
				}
			}
		}
		return out
	},
}

// Docker Desktop for Mac shares these host paths with its VM by default.
var desktopSharedPaths = []string{"/Users", "/Volumes", "/private", "/tmp", "/var/folders"}

var fileSharingRule = rule{
	id:    "mounts.sharing",
	title: "Bind mounts are inside Docker Desktop's shared paths",
	needs: []fact.ID{fact.LocalDaemon, fact.ComposeFile},
	check: func(_ context.Context, env *Env) []Finding {
		h := env.Host
		if !h.IsDesktop() || (h.ClientOS != "darwin" && h.ClientOS != "linux") {
			return nil
		}
		defaults := desktopSharedPaths
		if h.ClientOS == "linux" {
			// Docker Desktop for Linux shares only the home directory.
			if home, err := os.UserHomeDir(); err == nil {
				defaults = []string{home}
			}
		}
		var out []Finding
		for _, svc := range env.Project.Services {
			for _, b := range svc.Binds {
				paths, source := defaults, "shares "+strings.Join(defaults, ", ")+" by default"
				if len(h.SharedPaths) > 0 {
					paths, source = h.SharedPaths, "shares "+strings.Join(h.SharedPaths, ", ")+" (read from the running VM)"
				}
				shared := false
				for _, p := range paths {
					if b.Source == p || strings.HasPrefix(b.Source, p+"/") {
						shared = true
					}
				}
				if shared {
					continue
				}
				out = append(out, Finding{
					Rule: "mounts.sharing", Severity: fact.Error, Service: svc.Name,
					Title:    "bind source " + b.Source + " is outside Docker Desktop's shared paths",
					Evidence: []string{"host: Docker Desktop " + source},
					Fix:      "Add the path in Docker Desktop → Settings → Resources → File sharing.",
					Location: svc.Location.String(),
					Predicts: "mounts denied: The path " + b.Source + " is not shared from the host and is not known to Docker.",
				})
			}
		}
		return out
	},
}

func looksLikeFile(target string) bool {
	base := filepath.Base(target)
	return strings.Contains(strings.TrimPrefix(base, "."), ".") && !strings.HasSuffix(target, "/")
}

func isEmptyDir(path string) bool {
	entries, err := os.ReadDir(path)
	return err == nil && len(entries) == 0
}
