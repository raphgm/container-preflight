package rules

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/raphgm/container-doctor/pkg/preflight/fact"
	"github.com/raphgm/container-doctor/pkg/preflight/host"
	"github.com/raphgm/container-doctor/pkg/preflight/project"
	"github.com/raphgm/container-doctor/pkg/preflight/registry"
)

func load(t *testing.T, files map[string]string) *project.Project {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	facts := fact.NewSet()
	p, err := project.Load(context.Background(), dir, facts)
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := facts.Root(fact.ComposeFile); ok {
		t.Fatalf("compose load failed: %s", r.Detail)
	}
	return p
}

// armDesktop is a MacBook running Docker Desktop with 4 GiB for the VM.
func armDesktop() *host.Profile {
	return &host.Profile{
		ClientOS: "darwin", ClientArch: "arm64", ClientMem: 16 << 30,
		Platform: "linux/arm64", OperatingSystem: "Docker Desktop",
		MemTotal: 4 << 30, Runtimes: []string{"runc"},
		ComposeVersion: "2.29.1", BuildxInstalled: true,
		Emulated: []string{"linux/amd64"}, EmulationKnown: true,
		Sysctls: map[string]int64{"vm.max_map_count": 262144},
		Local:   true, DiskFree: 50 << 30,
		LocalImages: map[string]bool{}, PublishedPorts: map[string][]host.Container{},
	}
}

// oldLinux is an amd64 server with distro packages and default sysctls.
func oldLinux() *host.Profile {
	return &host.Profile{
		ClientOS: "linux", ClientArch: "amd64",
		Platform: "linux/amd64", OperatingSystem: "Ubuntu 20.04",
		MemTotal: 8 << 30, Runtimes: []string{"runc"},
		ComposeVersion: "1.29.2", EmulationKnown: true,
		Sysctls: map[string]int64{"vm.max_map_count": 65530},
		Local:   true, DiskFree: 2 << 30, DiskExact: true,
		LocalImages: map[string]bool{}, PublishedPorts: map[string][]host.Container{},
	}
}

// fakeRegistry answers from a table: ref -> platforms (prefix "single:" for a
// single-platform image), or an error.
type fakeRegistry map[string]string

func (f fakeRegistry) Resolve(_ context.Context, ref, platform string) (*registry.Image, error) {
	v, ok := f[ref]
	if !ok {
		return nil, fmt.Errorf("%w: %s", registry.ErrNotFound, ref)
	}
	if v == "401" {
		return nil, fmt.Errorf("%w: %s", registry.ErrUnauthorized, ref)
	}
	img := &registry.Image{Ref: ref, Index: true, CompressedSize: 500 << 20}
	if s, ok := strings.CutPrefix(v, "single:"); ok {
		img.Index, v = false, s
	}
	img.Platforms = strings.Split(v, ",")
	return img, nil
}

func run(t *testing.T, r Rule, p *project.Project, h *host.Profile, reg fakeRegistry) []Finding {
	t.Helper()
	env := &Env{Project: p, Host: h, Facts: fact.NewSet(), Images: map[string]ImageResult{}}
	for _, pl := range Pulls(p, h) {
		img, err := reg.Resolve(context.Background(), pl.Ref, pl.Platform)
		env.Images[ImageKey(pl.Ref, pl.Platform)] = ImageResult{img, err}
	}
	return r.Check(context.Background(), env)
}

func titles(fs []Finding) string {
	var out []string
	for _, f := range fs {
		out = append(out, string(f.Severity)+": "+f.Title)
	}
	return strings.Join(out, "\n")
}

func expect(t *testing.T, fs []Finding, sev fact.Severity, substr string) {
	t.Helper()
	for _, f := range fs {
		if f.Severity == sev && strings.Contains(f.Title, substr) {
			return
		}
	}
	t.Errorf("want %s finding containing %q, got:\n%s", sev, substr, titles(fs))
}

func expectNone(t *testing.T, fs []Finding) {
	t.Helper()
	if len(fs) > 0 {
		t.Errorf("want no findings, got:\n%s", titles(fs))
	}
}

var multiArch = "linux/amd64,linux/arm64"

func TestImagePlatform(t *testing.T) {
	p := load(t, map[string]string{"compose.yaml": `services:
  db: {image: "mcr.microsoft.com/mssql/server:2022-latest"}
  legacy: {image: "acme/amd64-index:1"}
  pinned: {image: "acme/amd64-index:1", platform: linux/amd64}
  ok: {image: "postgres:16"}
`})
	reg := fakeRegistry{
		"mcr.microsoft.com/mssql/server:2022-latest": "single:linux/amd64",
		"acme/amd64-index:1":                         "linux/amd64",
		"postgres:16":                                multiArch,
	}

	fs := run(t, imagePlatformRule, p, armDesktop(), reg)
	expect(t, fs, fact.Warning, "mssql/server:2022-latest will run under linux/amd64 emulation")
	expect(t, fs, fact.Error, "acme/amd64-index:1 has no linux/arm64 variant")
	expect(t, fs, fact.Warning, "acme/amd64-index:1 will run under linux/amd64 emulation") // pinned
	if len(fs) != 3 {
		t.Errorf("postgres should pass; got:\n%s", titles(fs))
	}

	noEmu := armDesktop()
	noEmu.OperatingSystem, noEmu.Emulated = "Ubuntu 24.04", nil
	fs = run(t, imagePlatformRule, p, noEmu, reg)
	expect(t, fs, fact.Error, "is linux/amd64 only and this host cannot run it")

	colima := armDesktop()
	colima.OperatingSystem, colima.Emulators = "Ubuntu 24.04", map[string]string{"linux/amd64": "qemu"}
	fs = run(t, imagePlatformRule, p, colima, reg)
	expect(t, fs, fact.Error, "mssql/server:2022-latest crashes under QEMU linux/amd64 emulation")

	rosetta := armDesktop()
	rosetta.Emulators = map[string]string{"linux/amd64": "rosetta"}
	expect(t, run(t, imagePlatformRule, p, rosetta, reg), fact.Warning, "mssql/server:2022-latest will run under linux/amd64 emulation")

	unknown := armDesktop()
	unknown.OperatingSystem, unknown.Emulated, unknown.EmulationKnown = "Ubuntu 24.04", nil, false
	fs = run(t, imagePlatformRule, p, unknown, reg)
	expect(t, fs, fact.Warning, "could not tell whether this host emulates it")
	if strings.Contains(titles(fs), "cannot run it") {
		t.Error("unknown emulation must not be reported as a certain failure")
	}

	expectNone(t, run(t, imagePlatformRule, p, oldLinux(), reg))
}

func TestBaseImagesOfBuildsAreChecked(t *testing.T) {
	p := load(t, map[string]string{
		"compose.yaml": "services:\n  app: {build: .}\n",
		"Dockerfile": `FROM --platform=$BUILDPLATFORM golang:1.23 AS build
FROM acme/amd64-index:1 AS unused
FROM acme/runtime:1
`,
	})
	reg := fakeRegistry{"golang:1.23": multiArch, "acme/runtime:1": "linux/amd64", "acme/amd64-index:1": "linux/amd64"}
	fs := run(t, imagePlatformRule, p, armDesktop(), reg)
	expect(t, fs, fact.Error, "base image acme/runtime:1 has no linux/arm64 variant")
	if strings.Contains(titles(fs), "amd64-index") {
		t.Error("unreachable stage must not be checked")
	}
}

func TestImageExists(t *testing.T) {
	p := load(t, map[string]string{"compose.yaml": `services:
  a: {image: "redis:7.9.99"}
  b: {image: "acme/private:1"}
  c: {image: "redis:7"}
`})
	h := armDesktop()
	h.LocalImages[registry.Canonical("redis:7")] = true
	fs := run(t, imageExistsRule, p, h, fakeRegistry{"acme/private:1": "401"})
	expect(t, fs, fact.Error, "redis:7.9.99 does not exist")
	expect(t, fs, fact.Warning, "acme/private:1 is private or does not exist")
	if len(fs) != 2 {
		t.Errorf("local image must not be looked up:\n%s", titles(fs))
	}
}

func TestBuildContext(t *testing.T) {
	p := load(t, map[string]string{
		"compose.yaml": `services:
  web: {build: ./web}
  api: {build: {context: ./api, target: nope}}
  gone: {build: ./missing}
`,
		"web/Dockerfile": `FROM node:20 AS build
COPY package.json yarn.lock ./
COPY .env ./
COPY ../shared ./shared
FROM nginx
COPY --from=build /app /app
COPY dist/ /usr/share/nginx/html
`,
		"web/package.json":  "{}",
		"web/.env":          "",
		"web/.dockerignore": ".env\n",
		"api/Dockerfile":    "FROM alpine AS base\n",
	})
	fs := run(t, buildContextRule, p, armDesktop(), nil)
	expect(t, fs, fact.Error, `COPY source "yarn.lock" not found`)
	expect(t, fs, fact.Error, `COPY source ".env" is excluded by .dockerignore`)
	expect(t, fs, fact.Error, `"../shared" is outside the build context`)
	expect(t, fs, fact.Error, `COPY source "dist/" not found`)
	expect(t, fs, fact.Error, `build target "nope" is not a stage`)
	expect(t, fs, fact.Error, "build context missing does not exist")
	if strings.Contains(titles(fs), "package.json") {
		t.Error("existing file flagged")
	}
}

func TestBuildKit(t *testing.T) {
	p := load(t, map[string]string{
		"compose.yaml": "services:\n  app: {build: .}\n",
		"Dockerfile":   "FROM alpine\nRUN --mount=type=cache,target=/c true\nCOPY --chmod=755 x /x\nRUN <<EOF\necho hi\nEOF\n",
		"x":            "",
	})
	expectNone(t, run(t, buildKitRule, p, armDesktop(), nil))
	fs := run(t, buildKitRule, p, oldLinux(), nil)
	expect(t, fs, fact.Error, "RUN --mount requires BuildKit")
	expect(t, fs, fact.Error, "COPY --chmod requires BuildKit")
	expect(t, fs, fact.Error, "heredoc")
}

func TestComposeVersion(t *testing.T) {
	p := load(t, map[string]string{"compose.yaml": `services:
  app:
    image: alpine
    develop: {watch: [{path: ., action: sync, target: /app}]}
    post_start: [{command: echo}]
`})
	h := armDesktop()
	h.ComposeVersion = "2.24.0-desktop.1"
	fs := run(t, composeFeaturesRule, p, h, nil)
	expect(t, fs, fact.Error, "post_start` needs Compose 2.30.0")
	if strings.Contains(titles(fs), "develop") {
		t.Error("2.24 supports develop")
	}
	fs = run(t, composeFeaturesRule, p, oldLinux(), nil)
	expect(t, fs, fact.Warning, "legacy docker-compose 1.29.2")
	expect(t, fs, fact.Error, "develop` needs Compose 2.22.0")
}

func TestMemory(t *testing.T) {
	p := load(t, map[string]string{"compose.yaml": `services:
  db: {image: "mcr.microsoft.com/mssql/server:2022-latest"}
  worker: {image: alpine, mem_limit: 1g, deploy: {replicas: 3}}
  huge: {image: alpine, mem_limit: 8g}
`})
	small := armDesktop()
	small.MemTotal = 1900 * mib
	fs := run(t, memoryRule, p, small, nil)
	expect(t, fs, fact.Error, "mssql/server needs at least")
	expect(t, fs, fact.Warning, "memory limit 8.0 GiB is larger than all memory Docker has")
	expect(t, fs, fact.Warning, "services need")
	if !strings.Contains(strings.Join(fs[0].Evidence, " "), "Docker Desktop VM has") {
		t.Errorf("desktop evidence should name the VM: %v", fs[0].Evidence)
	}

	big := armDesktop()
	big.MemTotal = 32 << 30
	expectNone(t, run(t, memoryRule, p, big, nil))
}

func TestSysctlKnowsSingleNodeElasticsearch(t *testing.T) {
	p := load(t, map[string]string{"compose.yaml": `services:
  es: {image: "docker.elastic.co/elasticsearch/elasticsearch:8.15.0"}
  dev: {image: "opensearchproject/opensearch:2", environment: {discovery.type: single-node}}
`})
	fs := run(t, sysctlRule, p, oldLinux(), nil)
	expect(t, fs, fact.Error, "elasticsearch needs vm.max_map_count")
	if len(fs) != 1 {
		t.Errorf("single-node opensearch skips bootstrap checks:\n%s", titles(fs))
	}
	expectNone(t, run(t, sysctlRule, p, armDesktop(), nil))

	blind := oldLinux()
	blind.Sysctls = map[string]int64{}
	expect(t, run(t, sysctlRule, p, blind, nil), fact.Warning, "could not read it")
}

func TestGPU(t *testing.T) {
	p := load(t, map[string]string{"compose.yaml": `services:
  ml:
    image: pytorch/pytorch
    deploy: {resources: {reservations: {devices: [{driver: nvidia, count: 1, capabilities: [gpu]}]}}}
`})
	fs := run(t, gpuRule, p, armDesktop(), nil)
	expect(t, fs, fact.Error, "requests a GPU")
	if !strings.Contains(strings.Join(fs[0].Evidence, " "), "macOS") {
		t.Error("macOS should be named as the reason")
	}
	withGPU := oldLinux()
	withGPU.Runtimes = append(withGPU.Runtimes, "nvidia")
	expectNone(t, run(t, gpuRule, p, withGPU, nil))
}

func TestPorts(t *testing.T) {
	p := load(t, map[string]string{"compose.yaml": `name: shop
services:
  web: {image: nginx, ports: ["8080:80", "80:80"], deploy: {replicas: 2}}
  api: {image: node, ports: ["8080:3000"]}
  admin: {image: node, ports: ["9000:3000"]}
  metrics: {image: node, ports: ["9100:9100"]}
`})
	fs := run(t, portConflictRule, p, armDesktop(), nil)
	expect(t, fs, fact.Error, "2 replicas cannot all publish host port 8080")
	expect(t, fs, fact.Error, "host port 8080/tcp is published by both api and web")

	h := oldLinux()
	h.Rootless = true
	h.PublishedPorts["9000/tcp"] = []host.Container{{Name: "other-admin-1", ComposeProject: "other"}}
	h.PublishedPorts["9100/tcp"] = []host.Container{{Name: "shop-metrics-1", ComposeProject: "shop"}}
	h.PortProbe = func(_ string, port int, _ string) bool { return port == 8080 }
	fs = run(t, hostPortRule, p, h, nil)
	expect(t, fs, fact.Error, "rootless Docker cannot publish privileged port 80")
	expect(t, fs, fact.Error, "host port 8080/tcp is already in use by another process")
	expect(t, fs, fact.Error, "host port 9000/tcp is already in use by container other-admin-1")
	if strings.Contains(titles(fs), "9100") {
		t.Error("the project's own running container must not count as a conflict")
	}
	if n := strings.Count(titles(fs), "8080"); n != 1 {
		t.Errorf("port 8080 reported %d times; want once", n)
	}
}

func TestBinds(t *testing.T) {
	p := load(t, map[string]string{
		"compose.yaml": `services:
  web:
    image: nginx
    volumes:
      - ./nginx.conf:/etc/nginx/nginx.conf:ro
      - ./stale.conf:/etc/app/stale.conf
      - ./data:/data
      - ./html:/usr/share/nginx/html
      - type: bind
        source: ./strict
        target: /strict
        bind: {create_host_path: false}
`,
		"html/index.html": "",
	})
	if err := os.Mkdir(filepath.Join(p.Dir, "stale.conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	fs := run(t, bindRule, p, armDesktop(), nil)
	expect(t, fs, fact.Error, "bind source nginx.conf is missing; Docker will create a directory")
	expect(t, fs, fact.Error, "stale.conf is an empty directory but /etc/app/stale.conf expects a file")
	expect(t, fs, fact.Warning, "bind source data does not exist and will be created empty")
	expect(t, fs, fact.Error, "bind source strict does not exist")
	if strings.Contains(titles(fs), "html") {
		t.Error("existing directory flagged")
	}
}

func TestEnv(t *testing.T) {
	p := load(t, map[string]string{
		"compose.yaml": `services:
  app:
    image: alpine
    environment:
      A: ${SET_IN_DOTENV}
      B: ${UNSET_VAR}
      C: ${WITH_DEFAULT:-x}
      D: $$NOT_INTERPOLATED
      E: ${NEEDED:?please set}
`,
		".env": "SET_IN_DOTENV=1\n",
	})
	fs := run(t, envRule, p, armDesktop(), nil)
	expect(t, fs, fact.Warning, "UNSET_VAR is not set")
	expect(t, fs, fact.Error, "required variable NEEDED is not set")
	if len(fs) != 2 {
		t.Errorf("want exactly 2 findings:\n%s", titles(fs))
	}
}
