package host

import (
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/raphgm/container-doctor/pkg/preflight/fact"
)

// engine is a local Docker engine this machine could use.
type engine struct {
	Name    string
	Socket  string // socket path when the engine is running
	Start   string // command or action that starts it
	Context string // docker context it registers
}

// daemonClues is what diagnoseDaemon reasons over. It is gathered by
// gatherClues and kept separate so the reasoning can be tested.
type daemonClues struct {
	Context   string
	Endpoint  string
	Err       string
	Installed []engine
	Running   []engine

	// DiskFree is free space on this machine; VM engines cannot start
	// without room for their disk image.
	DiskFree int64
}

// diagnoseDaemon turns "cannot connect" into the specific reason: the
// context points at an engine that is gone, an installed engine is stopped,
// another engine is running under a different context, or permissions.
func diagnoseDaemon(c daemonClues) fact.Failure {
	f := fact.Failure{
		Fact:     fact.Daemon,
		Severity: fact.Error,
		Summary:  "Docker daemon is not reachable",
		Detail:   c.Err,
	}
	socket := strings.TrimPrefix(c.Endpoint, "unix://")
	ctxName := c.Context
	if ctxName == "" {
		ctxName = "default"
	}

	switch {
	case strings.Contains(c.Err, "did not answer within"):
		f.Summary = "Docker daemon accepts connections but does not answer"
		fix := "Wait for it to finish starting; if it stays stuck, restart it."
		if len(c.Running) > 1 {
			var names []string
			for _, r := range c.Running {
				names = append(names, r.Name)
			}
			fix = "Several Docker VMs are running (" + strings.Join(names, ", ") + ") and compete for memory. Stop all but one, e.g. `colima stop`."
		}
		f.Fix = fix
		return f

	case strings.Contains(c.Err, "permission denied"):
		f.Summary = "No permission to use the Docker socket " + socket
		f.Fix = "Add yourself to the docker group (`sudo usermod -aG docker $USER`, then log in again), or use rootless Docker."
		return f

	case len(c.Running) > 0 && !strings.EqualFold(c.Running[0].Socket, socket):
		r := c.Running[0]
		f.Summary = fmt.Sprintf("%s is running, but the Docker context %q points elsewhere (%s)", r.Name, ctxName, socket)
		f.Fix = "`docker context use " + r.Context + "`"
		return f

	case strings.HasPrefix(c.Endpoint, "unix://"):
		owner := socketOwner(socket)
		ownerInstalled := false
		for _, e := range c.Installed {
			if e.Name == owner {
				ownerInstalled = true
			}
		}
		switch {
		case owner != "" && !ownerInstalled:
			f.Summary = fmt.Sprintf("Docker context %q still points to %s, which is not installed", ctxName, owner)
			f.Detail = socket + " is left over from it; nothing is listening there"
		case !fileExists(socket):
			f.Summary = fmt.Sprintf("Docker context %q points to %s, which does not exist", ctxName, socket)
		case owner != "":
			f.Summary = owner + " is installed but not running"
		}
	}

	switch len(c.Installed) {
	case 0:
		f.Fix = "Install a Docker engine (Docker Desktop, OrbStack or Colima) and start it."
	default:
		e := c.Installed[0]
		f.Fix = fmt.Sprintf("Start %s: `%s`", e.Name, e.Start)
		if e.Context != "" && e.Context != ctxName {
			f.Fix += fmt.Sprintf(", then `docker context use %s`", e.Context)
		}
		if c.DiskFree > 0 && c.DiskFree < 2<<30 && e.Name != "Docker Engine" {
			f.Fix = fmt.Sprintf("Free disk space first: only %d MiB free, and %s needs a few GiB for its VM. Then: %s",
				c.DiskFree>>20, e.Name, strings.TrimPrefix(f.Fix, "Start "+e.Name+": "))
		}
		if len(c.Installed) > 1 {
			var others []string
			for _, o := range c.Installed[1:] {
				others = append(others, o.Name)
			}
			f.Fix += " (also installed: " + strings.Join(others, ", ") + ")"
		}
	}
	return f
}

func socketOwner(socket string) string {
	switch {
	case strings.Contains(socket, "/.docker/run/"), strings.Contains(socket, "docker-desktop"):
		return "Docker Desktop"
	case strings.Contains(socket, "/.colima/"):
		return "Colima"
	case strings.Contains(socket, "/.orbstack/"):
		return "OrbStack"
	case strings.Contains(socket, "podman"):
		return "Podman"
	}
	return ""
}

// gatherClues looks for local engines: installed apps and binaries, and the
// sockets of those that are running.
func gatherClues(ctxName, endpoint, errText string, diskFree int64) daemonClues {
	c := daemonClues{Context: ctxName, Endpoint: endpoint, Err: errText, DiskFree: diskFree}
	home, _ := os.UserHomeDir()

	candidates := []struct {
		engine
		installed func() bool
	}{
		{engine{"Colima", filepath.Join(home, ".colima/default/docker.sock"), "colima start", "colima"}, func() bool { return onPath("colima") }},
		{engine{"OrbStack", filepath.Join(home, ".orbstack/run/docker.sock"), "open -a OrbStack", "orbstack"}, func() bool {
			return fileExists("/Applications/OrbStack.app") || onPath("orb")
		}},
		{engine{"Docker Desktop", filepath.Join(home, ".docker/run/docker.sock"), "open -a Docker", "desktop-linux"}, func() bool {
			return fileExists("/Applications/Docker.app")
		}},
	}
	if runtime.GOOS == "linux" {
		candidates = append(candidates, struct {
			engine
			installed func() bool
		}{engine{"Docker Engine", "/var/run/docker.sock", "sudo systemctl start docker", "default"}, func() bool { return onPath("dockerd") }})
	}

	for _, cand := range candidates {
		if fileExists(cand.Socket) {
			c.Running = append(c.Running, cand.engine)
		}
		if cand.installed() {
			c.Installed = append(c.Installed, cand.engine)
		}
	}
	return c
}

func onPath(bin string) bool {
	_, err := osexec.LookPath(bin)
	return err == nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
