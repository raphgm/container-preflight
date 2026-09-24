// Package host builds a capability profile of the machine that will run the
// project: the Docker daemon's platform and resources (which on macOS and
// Windows are the VM's, not the laptop's), installed tooling, emulation, and
// live port usage.
package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"

	"github.com/raphgm/container-preflight/internal/executor"
	"github.com/raphgm/container-preflight/pkg/preflight/fact"
	"github.com/raphgm/container-preflight/pkg/preflight/registry"
)

type Profile struct {
	// ClientOS/ClientArch describe the machine preflight runs on.
	ClientOS   string
	ClientArch string
	ClientMem  int64

	// ClientDiskFree is free space on this machine's home volume, where
	// Docker Desktop, Colima and OrbStack keep their VM disks.
	ClientDiskFree int64

	// Platform is the daemon's native platform, e.g. linux/arm64.
	Platform        string
	OperatingSystem string
	ServerVersion   string
	KernelVersion   string
	MemTotal        int64
	NCPU            int
	Runtimes        []string
	Rootless        bool
	SELinux         bool
	CgroupVersion   string
	DockerRootDir   string
	Endpoint        string

	// DiskFree is the free space where images are stored. DiskExact is
	// false when only an upper bound is known (Docker Desktop's VM disk
	// cannot be measured from the host).
	DiskFree  int64
	DiskExact bool

	ComposeVersion   string
	BuildxInstalled  bool
	BuildxVersion    string
	BuildKitDisabled bool

	// Emulated lists foreign platforms the host can run or build.
	// EmulationKnown is false when the daemon's kernel could not be
	// inspected, so an empty Emulated list proves nothing.
	Emulated       []string
	EmulationKnown bool

	// Emulators maps an emulated platform to the mechanism: "qemu" or
	// "rosetta". Some software runs under Rosetta but crashes under QEMU.
	Emulators map[string]string

	// SharedPaths are the host folders Docker Desktop shares with its VM,
	// read from the running VM. Empty when unknown.
	SharedPaths []string

	// Sysctls holds kernel settings read on a local Linux daemon host.
	Sysctls map[string]int64

	// LocalImages holds canonical references already present in the
	// daemon; Compose does not pull these.
	LocalImages map[string]bool

	// PublishedPorts maps "port/proto" to containers already publishing it.
	PublishedPorts map[string][]Container

	// PortProbe and Lsof are nil on remote daemons.
	PortProbe func(ip string, port int, proto string) bool `json:"-"`
	Lsof      func(port int, proto string) string          `json:"-"`

	// RunningFromInstaller is true when Docker Desktop runs from its
	// mounted installer disk image instead of /Applications.
	RunningFromInstaller bool

	// Local is true when the daemon endpoint is on this machine.
	Local bool

	// MissingCredentialHelper names a credsStore/credHelpers program that
	// ~/.docker/config.json configures but that is not installed.
	MissingCredentialHelper string
}

type Container struct {
	Name           string
	ComposeProject string
}

// UsesVM reports whether the daemon runs in a VM whose disk image lives on
// this machine and grows as images are pulled.
func (p *Profile) UsesVM() bool {
	return p.Local && (p.ClientOS == "darwin" || p.ClientOS == "windows" || p.IsDesktop() || strings.Contains(p.Endpoint, "/.colima/"))
}

// MapsOwnership reports whether bind-mounted files appear owned by the
// container user regardless of host ownership: true for the file sharing of
// Docker Desktop, Colima and OrbStack on macOS and Windows.
func (p *Profile) MapsOwnership() bool {
	return p.ClientOS == "darwin" || p.ClientOS == "windows"
}

// IsDesktop reports whether the daemon runs inside Docker Desktop's VM.
func (p *Profile) IsDesktop() bool {
	return strings.Contains(p.OperatingSystem, "Docker Desktop")
}

// CanRun reports whether platform runs natively or under emulation.
func (p *Profile) CanRun(platform string) (native, emulated bool) {
	if registry.Compatible(platform, p.Platform) {
		return true, false
	}
	for _, e := range p.Emulated {
		if registry.Compatible(platform, e) {
			return false, true
		}
	}
	return false, false
}

// PortHolder reports what already holds a host port: a container, a local
// process, or "" when the port is free.
func (p *Profile) PortHolder(ip string, port int, proto string) (holder string, container *Container) {
	if cs := p.PublishedPorts[fmt.Sprintf("%d/%s", port, proto)]; len(cs) > 0 {
		return "container " + cs[0].Name, &cs[0]
	}
	if p.PortProbe != nil && p.PortProbe(ip, port, proto) {
		if p.Lsof != nil {
			if name := p.Lsof(port, proto); name != "" {
				return "process " + name, nil
			}
		}
		return "another process", nil
	}
	return "", nil
}

// Probe inspects the host. Anything it cannot determine is recorded in
// facts with the reason, so dependent rules report the true root cause.
func Probe(ctx context.Context, run executor.Runner, facts *fact.Set) *Profile {
	run = timeoutRunner{run}
	p := &Profile{
		ClientOS:       runtime.GOOS,
		ClientArch:     runtime.GOARCH,
		Sysctls:        map[string]int64{},
		PublishedPorts: map[string][]Container{},
		LocalImages:    map[string]bool{},
	}
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		p.ClientMem = int64(vm.Total)
	}
	if home, err := os.UserHomeDir(); err == nil {
		if u, err := disk.UsageWithContext(ctx, home); err == nil {
			p.ClientDiskFree = int64(u.Free)
		}
	}
	p.BuildKitDisabled = os.Getenv("DOCKER_BUILDKIT") == "0"

	if _, err := run.Run(ctx, "docker", "--version"); err != nil {
		facts.Fail(fact.Failure{
			Fact:     fact.DockerCLI,
			Severity: fact.Error,
			Summary:  "Docker CLI not found",
			Detail:   errText(err),
			Fix:      "Install Docker Desktop, OrbStack, Colima or Docker Engine, and make sure `docker` is on PATH.",
		})
		return p
	}

	p.MissingCredentialHelper = missingCredentialHelper()
	p.probeEndpoint(ctx, run, facts)
	p.probeDaemon(ctx, run, facts)
	p.probeCompose(ctx, run, facts)
	p.probeBuildx(ctx, run)
	return p
}

func (p *Profile) probeDaemon(ctx context.Context, run executor.Runner, facts *fact.Set) {
	out, err := run.Run(ctx, "docker", "info", "--format", "{{json .}}")
	var info dockerInfo
	if err == nil {
		err = json.Unmarshal([]byte(out), &info)
	}
	if err == nil && len(info.ServerErrors) > 0 {
		err = errors.New(strings.Join(info.ServerErrors, "; "))
	}
	if err == nil && info.ServerVersion == "" {
		err = errors.New("docker info returned no server details")
	}
	if err != nil {
		ctxName, _ := run.Run(ctx, "docker", "context", "show")
		facts.Fail(diagnoseDaemon(gatherClues(ctxName, p.Endpoint, errText(err), p.ClientDiskFree)))
		return
	}

	p.Platform = registry.Normalize(info.OSType + "/" + info.Architecture)
	p.OperatingSystem = info.OperatingSystem
	p.ServerVersion = info.ServerVersion
	p.KernelVersion = info.KernelVersion
	p.MemTotal = info.MemTotal
	p.NCPU = info.NCPU
	p.CgroupVersion = info.CgroupVersion
	p.DockerRootDir = info.DockerRootDir
	for name := range info.Runtimes {
		p.Runtimes = append(p.Runtimes, name)
	}
	for _, opt := range info.SecurityOptions {
		if strings.Contains(opt, "name=rootless") {
			p.Rootless = true
		}
		if strings.Contains(opt, "name=selinux") {
			p.SELinux = true
		}
	}

	if p.IsDesktop() {
		// Docker Desktop's VM supports amd64 on Apple silicon (Rosetta or
		// QEMU) and ships its own sysctl defaults.
		if p.Platform == "linux/arm64" {
			p.Emulated = append(p.Emulated, "linux/amd64")
		} else if p.Platform == "linux/amd64" {
			p.Emulated = append(p.Emulated, "linux/arm64", "linux/arm/v7")
		}
		p.Sysctls = map[string]int64{"vm.max_map_count": 262144}
		p.EmulationKnown = true
		p.DiskExact = false
		p.readDesktopVM(ctx, run)
	} else if shell := p.kernelShell(); shell != nil {
		p.readKernel(ctx, run, shell)
	}

	if out, err := run.Run(ctx, "docker", "image", "ls", "--format", "{{.Repository}}:{{.Tag}}"); err == nil {
		for _, ref := range strings.Split(out, "\n") {
			if ref != "" && !strings.Contains(ref, "<none>") {
				p.LocalImages[registry.Canonical(ref)] = true
			}
		}
	}

	if out, err := run.Run(ctx, "docker", "ps", "--format", "{{.Names}}\t{{.Label \"com.docker.compose.project\"}}\t{{.Ports}}"); err == nil {
		p.PublishedPorts = parsePS(out)
	}
}

// probeEndpoint decides from the CLI context whether the daemon runs on
// this machine. Only then do local ports, paths and kernel settings apply.
func (p *Profile) probeEndpoint(ctx context.Context, run executor.Runner, facts *fact.Set) {
	p.Endpoint = os.Getenv("DOCKER_HOST")
	if p.Endpoint == "" {
		p.Endpoint, _ = run.Run(ctx, "docker", "context", "inspect", "--format", "{{.Endpoints.docker.Host}}")
	}
	p.Local = p.Endpoint == "" || strings.HasPrefix(p.Endpoint, "unix://") || strings.HasPrefix(p.Endpoint, "npipe://")
	if !p.Local {
		facts.Fail(fact.Failure{
			Fact:     fact.LocalDaemon,
			Severity: fact.Info,
			Summary:  "Docker daemon is remote (" + p.Endpoint + ")",
			Detail:   "Ports, bind-mount paths and kernel settings live on the remote host and cannot be checked from here.",
			Fix:      "Run container-preflight on the daemon host for full coverage.",
		})
		return
	}

	p.PortProbe = portInUse
	p.Lsof = lsofHolder
	if home, err := os.UserHomeDir(); err == nil {
		if usage, err := disk.UsageWithContext(ctx, home); err == nil {
			p.DiskFree = int64(usage.Free)
		}
	}
}

// readDesktopVM reads Docker Desktop's VM command line on macOS: it shows
// whether amd64 runs under Rosetta or QEMU and which folders are shared.
func (p *Profile) readDesktopVM(ctx context.Context, run executor.Runner) {
	if p.ClientOS != "darwin" {
		return
	}
	pid, err := run.Run(ctx, "pgrep", "-f", "com.docker.virtualization")
	if err != nil || pid == "" {
		return
	}
	args, err := run.Run(ctx, "ps", "-o", "args=", "-p", strings.Fields(pid)[0])
	if err != nil {
		return
	}
	fields := strings.Fields(args)
	emulator := "qemu"
	for i, f := range fields {
		switch f {
		case "--rosetta":
			emulator = "rosetta"
		case "--virtiofs":
			if i+1 < len(fields) {
				p.SharedPaths = append(p.SharedPaths, fields[i+1])
			}
		}
	}
	for _, e := range p.Emulated {
		if p.Emulators == nil {
			p.Emulators = map[string]string{}
		}
		if e == "linux/amd64" {
			p.Emulators[e] = emulator
		} else {
			p.Emulators[e] = "qemu"
		}
	}
	if strings.Contains(args, "/Volumes/") && strings.Contains(args, "Docker.app") {
		p.RunningFromInstaller = true
	}
}

// kernelShell returns the command prefix that runs a shell where the
// daemon's kernel lives: this machine for a local Linux daemon, or the VM
// for Colima. It returns nil when there is no way in.
func (p *Profile) kernelShell() []string {
	if !p.Local {
		return nil
	}
	sock := strings.TrimPrefix(p.Endpoint, "unix://")
	if i := strings.Index(sock, "/.colima/"); i >= 0 {
		profile := strings.SplitN(sock[i+len("/.colima/"):], "/", 2)[0]
		if profile == "" || profile == "_lima" {
			profile = "default"
		}
		return []string{"colima", "ssh", "-p", profile, "--"}
	}
	if runtime.GOOS == "linux" {
		return []string{}
	}
	return nil
}

// kernelScript prints the kernel facts preflight needs, one per line.
const kernelScript = `echo max_map_count=$(cat /proc/sys/vm/max_map_count 2>/dev/null)
echo unpriv_port_start=$(cat /proc/sys/net/ipv4/ip_unprivileged_port_start 2>/dev/null)
echo binfmt=$(ls /proc/sys/fs/binfmt_misc 2>/dev/null | tr '\n' ' ')
echo disk_free=$(df -B1 --output=avail "$0" 2>/dev/null | tail -1)`

func (p *Profile) readKernel(ctx context.Context, run executor.Runner, shell []string) {
	root := p.DockerRootDir
	if root == "" {
		root = "/var/lib/docker"
	}
	args := append(append([]string{}, shell...), "sh", "-c", kernelScript, root)
	out, err := run.Run(ctx, args[0], args[1:]...)
	if err != nil {
		return
	}
	native := p.Platform
	for _, line := range strings.Split(out, "\n") {
		key, val, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || val == "" {
			continue
		}
		switch key {
		case "max_map_count":
			if v, err := strconv.ParseInt(val, 10, 64); err == nil {
				p.Sysctls["vm.max_map_count"] = v
			}
		case "unpriv_port_start":
			if v, err := strconv.ParseInt(val, 10, 64); err == nil {
				p.Sysctls["net.ipv4.ip_unprivileged_port_start"] = v
			}
		case "binfmt":
			p.EmulationKnown = true
			for _, h := range strings.Fields(val) {
				arch, ok := binfmtPlatforms[h]
				if !ok || arch == native {
					continue
				}
				if !containsStr(p.Emulated, arch) {
					p.Emulated = append(p.Emulated, arch)
				}
				if p.Emulators == nil {
					p.Emulators = map[string]string{}
				}
				// Rosetta wins when both are registered: the kernel
				// prefers it on Apple virtualization.
				if h == "rosetta" || p.Emulators[arch] == "" {
					p.Emulators[arch] = strings.SplitN(h, "-", 2)[0]
				}
			}
		case "disk_free":
			if v, err := strconv.ParseInt(val, 10, 64); err == nil {
				p.DiskFree, p.DiskExact = v, true
			}
		}
	}
}

// binfmtPlatforms maps binfmt_misc handler names (QEMU and Rosetta) to the
// platforms they run.
var binfmtPlatforms = map[string]string{
	"qemu-x86_64":  "linux/amd64",
	"rosetta":      "linux/amd64",
	"qemu-aarch64": "linux/arm64",
	"qemu-arm":     "linux/arm/v7",
	"qemu-riscv64": "linux/riscv64",
	"qemu-ppc64le": "linux/ppc64le",
	"qemu-s390x":   "linux/s390x",
}

func (p *Profile) probeCompose(ctx context.Context, run executor.Runner, facts *fact.Set) {
	if v, err := run.Run(ctx, "docker", "compose", "version", "--short"); err == nil {
		p.ComposeVersion = strings.TrimPrefix(v, "v")
		return
	}
	if v, err := run.Run(ctx, "docker-compose", "version", "--short"); err == nil {
		p.ComposeVersion = strings.TrimPrefix(v, "v")
		return
	}
	facts.Fail(fact.Failure{
		Fact:     fact.Compose,
		Severity: fact.Error,
		Summary:  "Docker Compose is not installed",
		Fix:      "Install the Compose v2 plugin (bundled with Docker Desktop; `docker-compose-plugin` package on Linux).",
	})
}

func (p *Profile) probeBuildx(ctx context.Context, run executor.Runner) {
	v, err := run.Run(ctx, "docker", "buildx", "version")
	if err != nil {
		return
	}
	p.BuildxInstalled = true
	if f := strings.Fields(v); len(f) >= 2 {
		p.BuildxVersion = strings.TrimPrefix(f[1], "v")
	}
	if out, err := run.Run(ctx, "docker", "buildx", "inspect"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "Platforms:"); ok {
				p.EmulationKnown = true
				for _, pl := range strings.Split(rest, ",") {
					pl = registry.Normalize(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(pl), "*")))
					if pl != "" && pl != p.Platform && !containsStr(p.Emulated, pl) {
						p.Emulated = append(p.Emulated, pl)
					}
				}
			}
		}
	}
}

type dockerInfo struct {
	ServerVersion   string
	OSType          string
	Architecture    string
	OperatingSystem string
	KernelVersion   string
	MemTotal        int64
	NCPU            int
	CgroupVersion   string
	DockerRootDir   string
	Runtimes        map[string]json.RawMessage
	SecurityOptions []string
	ServerErrors    []string
}

// parsePS reads `docker ps` rows: name, compose project, ports.
func parsePS(out string) map[string][]Container {
	res := map[string][]Container{}
	for _, line := range strings.Split(out, "\n") {
		cols := strings.Split(line, "\t")
		if len(cols) < 3 {
			continue
		}
		c := Container{Name: cols[0], ComposeProject: cols[1]}
		for _, mapping := range strings.Split(cols[2], ", ") {
			// 0.0.0.0:8080->80/tcp, :::8080->80/tcp, 0.0.0.0:8000-8001->80-81/tcp
			left, right, ok := strings.Cut(mapping, "->")
			if !ok {
				continue
			}
			_, proto, _ := strings.Cut(right, "/")
			hostPorts := left[strings.LastIndex(left, ":")+1:]
			lo, hi, _ := strings.Cut(hostPorts, "-")
			start, err := strconv.Atoi(lo)
			if err != nil {
				continue
			}
			end := start
			if hi != "" {
				if e, err := strconv.Atoi(hi); err == nil {
					end = e
				}
			}
			for port := start; port <= end; port++ {
				key := fmt.Sprintf("%d/%s", port, proto)
				if !containsContainer(res[key], c.Name) {
					res[key] = append(res[key], c)
				}
			}
		}
	}
	return res
}

func portInUse(ip string, port int, proto string) bool {
	if ip == "" {
		ip = "0.0.0.0"
	}
	addr := net.JoinHostPort(ip, strconv.Itoa(port))
	if proto == "udp" {
		c, err := net.ListenPacket("udp", addr)
		if err != nil {
			return true
		}
		c.Close()
		return false
	}
	// A listener on 127.0.0.1 alone still blocks Docker's 0.0.0.0 bind on
	// Linux, and on macOS a bind can succeed despite it, so dial as well.
	if c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 300*time.Millisecond); err == nil {
		c.Close()
		return true
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return true
	}
	l.Close()
	return false
}

func lsofHolder(port int, proto string) string {
	args := []string{"-nP", fmt.Sprintf("-i%s:%d", strings.ToUpper(proto), port), "-Fcp"}
	if proto == "tcp" {
		args = append(args, "-sTCP:LISTEN")
	}
	out, err := osexec.Command("lsof", args...).Output()
	if err != nil {
		return ""
	}
	var pid, cmd string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "p") && pid == "" {
			pid = line[1:]
		}
		if strings.HasPrefix(line, "c") && cmd == "" {
			cmd = line[1:]
		}
	}
	if cmd == "" {
		return ""
	}
	return fmt.Sprintf("%s (pid %s)", cmd, pid)
}

// missingCredentialHelper returns the first credential helper configured in
// the Docker CLI config that cannot be found on PATH.
func missingCredentialHelper() string {
	dir := os.Getenv("DOCKER_CONFIG")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".docker")
	}
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		CredsStore  string            `json:"credsStore"`
		CredHelpers map[string]string `json:"credHelpers"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return ""
	}
	helpers := []string{cfg.CredsStore}
	for _, h := range cfg.CredHelpers {
		helpers = append(helpers, h)
	}
	for _, h := range helpers {
		if h == "" {
			continue
		}
		if _, err := osexec.LookPath("docker-credential-" + h); err != nil {
			return "docker-credential-" + h
		}
	}
	return ""
}

// commandTimeout bounds each probe command: a daemon that is starting or
// starved of memory accepts connections but never answers.
const commandTimeout = 20 * time.Second

type timeoutRunner struct{ executor.Runner }

func (t timeoutRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	out, err := t.Runner.Run(cctx, name, args...)
	if err != nil && cctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("`%s %s` did not answer within %s (the daemon may still be starting, or be out of memory)", name, strings.Join(args, " "), commandTimeout)
	}
	return out, err
}

func errText(err error) string {
	if errors.Is(err, osexec.ErrNotFound) {
		return "`docker` executable not found on PATH"
	}
	return strings.TrimPrefix(strings.TrimSpace(err.Error()), "exit status 1: ")
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func containsContainer(cs []Container, name string) bool {
	for _, c := range cs {
		if c.Name == name {
			return true
		}
	}
	return false
}
