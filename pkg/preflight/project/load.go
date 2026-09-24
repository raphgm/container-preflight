package project

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/compose-spec/compose-go/v2/cli"
	"github.com/compose-spec/compose-go/v2/dotenv"
	"github.com/compose-spec/compose-go/v2/types"
	"github.com/sirupsen/logrus"

	"github.com/raphgm/container-doctor/pkg/preflight/fact"
)

// composeFileNames is the lookup order Compose itself uses.
var composeFileNames = []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"}

// Load extracts requirements from dir. A Compose file is used when present;
// otherwise a lone Dockerfile is treated as a single built service. Parse
// problems are returned as fact failures so rules depending on them are
// reported as blocked rather than silently skipped.
func Load(ctx context.Context, dir string, facts *fact.Set) (*Project, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}

	p := &Project{Dir: abs, Name: filepath.Base(abs), Env: environment(abs)}

	for _, name := range composeFileNames {
		if _, err := os.Stat(filepath.Join(abs, name)); err == nil {
			p.ComposeFile = filepath.Join(abs, name)
			break
		}
	}

	if p.ComposeFile == "" {
		dockerfile := filepath.Join(abs, "Dockerfile")
		if _, err := os.Stat(dockerfile); err != nil {
			return nil, fmt.Errorf("no compose file or Dockerfile found in %s", abs)
		}
		svc := &Service{
			Name:     p.Name,
			Replicas: 1,
			Build:    &Build{Context: abs, DockerfilePath: dockerfile},
			Location: Location{File: dockerfile},
		}
		loadDockerfile(svc.Build, facts)
		p.Services = append(p.Services, svc)
		return p, nil
	}

	raw, err := os.ReadFile(p.ComposeFile)
	if err != nil {
		return nil, err
	}
	p.EnvRefs = scanEnvRefs(p.ComposeFile, raw)
	p.ComposeFeatures = scanComposeFeatures(p.ComposeFile, raw)

	cp, err := loadCompose(ctx, abs, p.ComposeFile, nil)
	if err != nil {
		// Missing ${VAR:?} values are reported precisely by the env rule;
		// keep analysing the rest of the file instead of stopping here.
		if placeholders := missingRequired(p); len(placeholders) > 0 {
			cp, err = loadCompose(ctx, abs, p.ComposeFile, placeholders)
		}
	}
	if err != nil {
		facts.Fail(fact.Failure{
			Fact:     fact.ComposeFile,
			Severity: fact.Error,
			Summary:  "Compose file cannot be loaded",
			Detail:   err.Error(),
			Fix:      "Fix the error above; `docker compose config` shows the same message.",
		})
		return p, nil
	}
	p.Name = cp.Name

	lines := serviceLines(raw)
	for _, name := range cp.ServiceNames() {
		sc := cp.Services[name]
		svc := convertService(sc)
		svc.Location = Location{File: p.ComposeFile, Line: lines[name]}
		if svc.Build != nil {
			loadDockerfile(svc.Build, facts)
		}
		for _, ef := range sc.EnvFiles {
			p.EnvFiles = append(p.EnvFiles, EnvFile{Service: name, Path: ef.Path, Required: bool(ef.Required)})
		}
		p.Services = append(p.Services, svc)
	}
	return p, nil
}

func loadCompose(ctx context.Context, dir, file string, extraEnv map[string]string) (*types.Project, error) {
	// compose-go logs interpolation warnings; preflight reports those itself.
	logrus.SetOutput(io.Discard)

	fns := []cli.ProjectOptionsFn{
		cli.WithWorkingDirectory(dir),
		cli.WithOsEnv,
		cli.WithEnvFiles(),
		cli.WithDotEnv,
	}
	if len(extraEnv) > 0 {
		var kv []string
		for k, v := range extraEnv {
			kv = append(kv, k+"="+v)
		}
		fns = append(fns, cli.WithEnv(kv))
	}
	opts, err := cli.NewProjectOptions([]string{file}, fns...)
	if err != nil {
		return nil, err
	}
	return cli.ProjectFromOptions(ctx, opts)
}

// missingRequired returns placeholder values for ${VAR:?} references that
// have no value.
func missingRequired(p *Project) map[string]string {
	out := map[string]string{}
	for _, r := range p.EnvRefs {
		if _, set := p.Env[r.Name]; r.Required && !set {
			out[r.Name] = "preflight-placeholder"
		}
	}
	return out
}

func convertService(sc types.ServiceConfig) *Service {
	svc := &Service{
		Name:        sc.Name,
		Platform:    sc.Platform,
		User:        sc.User,
		MemLimit:    int64(sc.MemLimit),
		Replicas:    1,
		Environment: map[string]string{},
	}
	if sc.Build == nil {
		svc.Image = sc.Image
	} else {
		b := &Build{
			Context:   sc.Build.Context,
			Target:    sc.Build.Target,
			Platforms: sc.Build.Platforms,
			Args:      map[string]string{},
		}
		for k, v := range sc.Build.Args {
			if v != nil {
				b.Args[k] = *v
			}
		}
		switch {
		case sc.Build.DockerfileInline != "":
			b.DockerfilePath = ""
		case filepath.IsAbs(sc.Build.Dockerfile):
			b.DockerfilePath = sc.Build.Dockerfile
		default:
			df := sc.Build.Dockerfile
			if df == "" {
				df = "Dockerfile"
			}
			b.DockerfilePath = filepath.Join(sc.Build.Context, df)
		}
		svc.Build = b
	}

	if sc.Scale != nil {
		svc.Replicas = *sc.Scale
	}
	if d := sc.Deploy; d != nil {
		if d.Replicas != nil {
			svc.Replicas = *d.Replicas
		}
		if lim := d.Resources.Limits; lim != nil {
			if l := int64(lim.MemoryBytes); l > 0 && (svc.MemLimit == 0 || l < svc.MemLimit) {
				svc.MemLimit = l
			}
		}
		if r := d.Resources.Reservations; r != nil {
			svc.MemReservation = int64(r.MemoryBytes)
			for _, dev := range r.Devices {
				if hasGPU(dev.Capabilities) {
					svc.GPU = true
				}
			}
		}
	}
	if int64(sc.MemReservation) > svc.MemReservation {
		svc.MemReservation = int64(sc.MemReservation)
	}
	if len(sc.Gpus) > 0 {
		svc.GPU = true
	}

	for k, v := range sc.Environment {
		if v != nil {
			svc.Environment[k] = *v
		}
	}

	for _, pc := range sc.Ports {
		if pc.Published == "" {
			continue
		}
		start, end, err := parseRange(pc.Published)
		if err != nil {
			continue
		}
		proto := pc.Protocol
		if proto == "" {
			proto = "tcp"
		}
		svc.Ports = append(svc.Ports, Port{HostIP: pc.HostIP, Start: start, End: end, Target: pc.Target, Protocol: proto})
	}

	for _, v := range sc.Volumes {
		if v.Type != types.VolumeTypeBind {
			continue
		}
		create, selinux := true, ""
		if v.Bind != nil {
			create, selinux = bool(v.Bind.CreateHostPath), v.Bind.SELinux
		}
		svc.Binds = append(svc.Binds, Bind{Source: v.Source, Target: v.Target, CreateHostPath: create, ReadOnly: v.ReadOnly, SELinux: selinux})
	}
	return svc
}

func hasGPU(caps []string) bool {
	for _, c := range caps {
		if c == "gpu" {
			return true
		}
	}
	return false
}

func parseRange(s string) (int, int, error) {
	lo, hi, found := strings.Cut(s, "-")
	start, err := strconv.Atoi(lo)
	if err != nil {
		return 0, 0, err
	}
	if !found {
		return start, start, nil
	}
	end, err := strconv.Atoi(hi)
	return start, end, err
}

func loadDockerfile(b *Build, facts *fact.Set) {
	if b.DockerfilePath == "" {
		return
	}
	df, err := ParseDockerfile(b.DockerfilePath, b.Args)
	if err != nil {
		b.ParseErr = err
		return
	}
	b.Dockerfile = df
}

// environment is what Compose interpolates with: the .env file, overridden
// by the process environment.
func environment(dir string) map[string]string {
	env := map[string]string{}
	if dot, err := dotenv.GetEnvFromFile(map[string]string{}, []string{filepath.Join(dir, ".env")}); err == nil {
		for k, v := range dot {
			env[k] = v
		}
	}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	return env
}
