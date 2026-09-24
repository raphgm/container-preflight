package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/docker/go-units"
	"github.com/mattn/go-shellwords"
)

// FromCommand builds a one-service project from a `docker run`, `docker
// create` or `docker pull` command line, so the same rules apply to
// containers started without Compose. Relative bind sources resolve
// against dir, as the shell would.
func FromCommand(cmdline, dir string) (*Project, error) {
	args, err := shellwords.Parse(cmdline)
	if err != nil {
		return nil, fmt.Errorf("cannot parse command: %w", err)
	}
	if len(args) > 0 && (args[0] == "docker" || strings.HasSuffix(args[0], "/docker")) {
		args = args[1:]
	}
	if len(args) > 0 && args[0] == "container" {
		args = args[1:]
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("expected `docker run`, `docker create` or `docker pull`")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	p := &Project{Dir: abs, Name: filepath.Base(abs), Env: environment(abs)}
	svc := &Service{Replicas: 1, Environment: map[string]string{}, Location: Location{File: "command line"}}

	verb, rest := args[0], args[1:]
	switch verb {
	case "run", "create", "pull":
	default:
		return nil, fmt.Errorf("unsupported command %q: use docker run, create or pull", verb)
	}

	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			svc.Image = a
			break // the rest is the container's command
		}
		name, val, hasVal := strings.Cut(a, "=")
		next := func() string {
			if hasVal {
				return val
			}
			if i+1 < len(rest) {
				i++
				return rest[i]
			}
			return ""
		}
		switch name {
		case "-p", "--publish":
			if port, ok := parsePublish(next()); ok {
				svc.Ports = append(svc.Ports, port)
			}
		case "-v", "--volume":
			if b, ok := parseVolume(next(), abs); ok {
				svc.Binds = append(svc.Binds, b)
			}
		case "--mount":
			if b, ok := parseMount(next(), abs); ok {
				svc.Binds = append(svc.Binds, b)
			}
		case "--platform":
			svc.Platform = next()
		case "-e", "--env":
			k, v, ok := strings.Cut(next(), "=")
			if !ok {
				v = os.Getenv(k)
			}
			svc.Environment[k] = v
		case "-u", "--user":
			svc.User = next()
		case "-m", "--memory":
			if n, err := units.RAMInBytes(next()); err == nil {
				svc.MemLimit = n
			}
		case "--memory-reservation":
			if n, err := units.RAMInBytes(next()); err == nil {
				svc.MemReservation = n
			}
		case "--gpus":
			next()
			svc.GPU = true
		case "--name":
			svc.Name = next()
		default:
			// Flags that take a value in the next argument; the rest are
			// booleans. Only the common ones matter for skipping.
			if !hasVal && takesValue[name] {
				i++
			}
		}
	}
	if svc.Image == "" {
		return nil, fmt.Errorf("no image in command")
	}
	if svc.Name == "" {
		svc.Name = strings.SplitN(filepath.Base(svc.Image), ":", 2)[0]
	}
	p.Services = []*Service{svc}
	return p, nil
}

var takesValue = map[string]bool{
	"-w": true, "--workdir": true, "--network": true, "--net": true, "-h": true, "--hostname": true,
	"--entrypoint": true, "--restart": true, "-l": true, "--label": true, "--env-file": true,
	"--add-host": true, "--device": true, "--cpus": true, "--shm-size": true, "--ulimit": true,
	"--log-driver": true, "--log-opt": true, "--cap-add": true, "--cap-drop": true, "--dns": true,
	"--security-opt": true, "--tmpfs": true, "--pull": true, "--ipc": true, "--pid": true,
	"--stop-signal": true, "--health-cmd": true, "--expose": true, "--link": true, "--sysctl": true,
	"--runtime": true, "--cgroupns": true, "--userns": true, "-a": true, "--attach": true,
}

// parsePublish reads [ip:]hostPort[-end]:containerPort[/proto].
func parsePublish(s string) (Port, bool) {
	proto := "tcp"
	if spec, pr, ok := strings.Cut(s, "/"); ok {
		s, proto = spec, pr
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 {
		return Port{}, false // container port only: nothing published on a fixed host port
	}
	ip := ""
	if len(parts) == 3 {
		ip = parts[0]
	}
	start, end, err := parseRange(parts[len(parts)-2])
	if err != nil || parts[len(parts)-2] == "" {
		return Port{}, false
	}
	target, _ := strconv.Atoi(strings.SplitN(parts[len(parts)-1], "-", 2)[0])
	return Port{HostIP: ip, Start: start, End: end, Target: uint32(target), Protocol: proto}, true
}

// parseVolume reads source:target[:opts]; named volumes are not binds.
func parseVolume(s, dir string) (Bind, bool) {
	parts := strings.Split(s, ":")
	if len(parts) < 2 {
		return Bind{}, false
	}
	src := parts[0]
	if !strings.HasPrefix(src, "/") && !strings.HasPrefix(src, ".") && !strings.HasPrefix(src, "~") {
		return Bind{}, false
	}
	b := Bind{Source: absPath(src, dir), Target: parts[1], CreateHostPath: true}
	if len(parts) > 2 {
		for _, o := range strings.Split(parts[2], ",") {
			switch o {
			case "ro":
				b.ReadOnly = true
			case "z", "Z":
				b.SELinux = o
			}
		}
	}
	return b, true
}

// parseMount reads --mount type=bind,source=…,target=…; unlike -v, a
// missing source is an error rather than created.
func parseMount(s, dir string) (Bind, bool) {
	kv := map[string]string{}
	for _, f := range strings.Split(s, ",") {
		k, v, _ := strings.Cut(f, "=")
		kv[k] = v
	}
	if kv["type"] != "bind" {
		return Bind{}, false
	}
	src := kv["source"]
	if src == "" {
		src = kv["src"]
	}
	dst := kv["target"]
	if dst == "" {
		dst = kv["destination"]
	}
	if dst == "" {
		dst = kv["dst"]
	}
	_, ro := kv["readonly"]
	if _, r := kv["ro"]; r {
		ro = true
	}
	return Bind{Source: absPath(src, dir), Target: dst, ReadOnly: ro}, src != ""
}

func absPath(p, dir string) string {
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	return filepath.Clean(p)
}
