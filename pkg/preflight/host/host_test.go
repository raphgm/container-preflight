package host

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/raphgm/container-preflight/pkg/preflight/fact"
)

type fakeRunner map[string]string

func (f fakeRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	key := strings.TrimSpace(name + " " + strings.Join(args, " "))
	for prefix, out := range f {
		if strings.HasPrefix(key, prefix) {
			if strings.HasPrefix(out, "ERR:") {
				return "", errors.New(strings.TrimPrefix(out, "ERR:"))
			}
			return out, nil
		}
	}
	return "", fmt.Errorf("unexpected command %q", key)
}

type missingDocker struct{}

func (missingDocker) Run(context.Context, string, ...string) (string, error) {
	return "", fmt.Errorf("%w", exec.ErrNotFound)
}

func TestNoDockerCLIIsTheOnlyRootCause(t *testing.T) {
	facts := fact.NewSet()
	Probe(context.Background(), missingDocker{}, facts)
	roots := facts.Roots()
	if len(roots) != 1 || roots[0].Fact != fact.DockerCLI {
		t.Fatalf("roots = %+v", roots)
	}
	for _, id := range []fact.ID{fact.Daemon, fact.Compose, fact.Buildx, fact.LocalDaemon} {
		if !facts.Failed(id) {
			t.Errorf("%s should be unknown without the CLI", id)
		}
	}
}

func TestDaemonDown(t *testing.T) {
	facts := fact.NewSet()
	p := Probe(context.Background(), fakeRunner{
		"docker --version":      "Docker version 27.0.3",
		"docker info":           "ERR:Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?",
		"docker compose":        "2.29.1",
		"docker buildx version": "github.com/docker/buildx v0.16.1 abc",
		"docker buildx inspect": "Platforms: linux/arm64",
		"docker ps":             "ERR:cannot connect",
	}, facts)
	r, ok := facts.Root(fact.Daemon)
	if !ok || r.Fact != fact.Daemon || !strings.Contains(r.Detail, "Cannot connect") {
		t.Fatalf("root = %+v", r)
	}
	if facts.Failed(fact.LocalDaemon) || p.PortProbe == nil {
		t.Error("a local endpoint stays checkable while the daemon is down")
	}
	if facts.Failed(fact.Compose) || p.ComposeVersion != "2.29.1" || !p.BuildxInstalled {
		t.Errorf("compose/buildx do not need the daemon: %+v", p)
	}
}

func TestDesktopProfile(t *testing.T) {
	facts := fact.NewSet()
	info := `{"ServerVersion":"27.0.3","OSType":"linux","Architecture":"aarch64","OperatingSystem":"Docker Desktop","MemTotal":4102033408,"NCPU":4,"Runtimes":{"runc":{}},"SecurityOptions":["name=seccomp,profile=builtin","name=cgroupns"]}`
	p := Probe(context.Background(), fakeRunner{
		"docker --version":       "Docker version 27.0.3",
		"docker info":            info,
		"docker context inspect": "unix:///Users/me/.docker/run/docker.sock",
		"docker ps":              "db-1\tshop\t0.0.0.0:5432->5432/tcp, :::5432->5432/tcp\nweb\t\t0.0.0.0:8000-8001->80-81/tcp",
		"docker compose":         "v2.29.1-desktop.1",
		"docker buildx version":  "github.com/docker/buildx v0.16.1 abc",
		"docker buildx inspect":  "Name: desktop-linux\nPlatforms: linux/arm64, linux/amd64, linux/amd64/v2, linux/riscv64",
	}, facts)
	if len(facts.Roots()) != 0 {
		t.Fatalf("unexpected failures: %+v", facts.Roots())
	}
	if p.Platform != "linux/arm64" || !p.IsDesktop() || p.MemTotal != 4102033408 {
		t.Errorf("profile = %+v", p)
	}
	if native, emu := p.CanRun("linux/amd64"); native || !emu {
		t.Error("desktop on arm64 should emulate amd64")
	}
	if native, emu := p.CanRun("linux/s390x"); native || emu {
		t.Error("s390x is not available")
	}
	if holder, c := p.PortHolder("", 5432, "tcp"); c == nil || c.ComposeProject != "shop" {
		t.Errorf("5432 holder = %q %+v", holder, c)
	}
	if _, c := p.PortHolder("", 8001, "tcp"); c == nil || c.Name != "web" {
		t.Error("range ports not parsed")
	}
	if p.ComposeVersion != "2.29.1-desktop.1" {
		t.Errorf("compose version = %q", p.ComposeVersion)
	}
}

func TestColimaKernelProbe(t *testing.T) {
	facts := fact.NewSet()
	info := `{"ServerVersion":"29.1.3","OSType":"linux","Architecture":"aarch64","OperatingSystem":"Ubuntu 24.04.4 LTS","MemTotal":2053578752,"NCPU":2,"DockerRootDir":"/var/lib/docker"}`
	p := Probe(context.Background(), fakeRunner{
		"docker --version":               "Docker version 29.8.1",
		"docker context inspect":         "unix:///Users/me/.colima/default/docker.sock",
		"docker info":                    info,
		"colima ssh -p default -- sh -c": "max_map_count=1048576\nunpriv_port_start=1024\nbinfmt=python3.12 qemu-i386 qemu-x86_64 register status\ndisk_free=95000000000",
		"docker-compose":                 "5.5.1",
		"docker compose":                 "ERR:unknown command",
		"docker buildx":                  "ERR:unknown command",
	}, facts)
	if !p.EmulationKnown || len(p.Emulated) != 1 || p.Emulated[0] != "linux/amd64" || p.Emulators["linux/amd64"] != "qemu" {
		t.Errorf("emulation = %v known=%v", p.Emulated, p.EmulationKnown)
	}
	if p.Sysctls["vm.max_map_count"] != 1048576 || !p.DiskExact || p.DiskFree != 95000000000 {
		t.Errorf("kernel facts = %v disk=%d exact=%v", p.Sysctls, p.DiskFree, p.DiskExact)
	}
	if p.ComposeVersion != "5.5.1" || p.BuildxInstalled {
		t.Errorf("compose=%q buildx=%v", p.ComposeVersion, p.BuildxInstalled)
	}
}
