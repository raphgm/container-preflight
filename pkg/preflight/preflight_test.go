package preflight

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/raphgm/container-doctor/pkg/preflight/fact"
	"github.com/raphgm/container-doctor/pkg/preflight/host"
	"github.com/raphgm/container-doctor/pkg/preflight/registry"
)

type daemonDown struct{}

func (daemonDown) Run(_ context.Context, name string, args ...string) (string, error) {
	cmd := name + " " + strings.Join(args, " ")
	switch {
	case strings.HasPrefix(cmd, "docker --version"):
		return "Docker version 27.0.3", nil
	case strings.HasPrefix(cmd, "docker context inspect"):
		return "unix:///var/run/docker.sock", nil
	case strings.HasPrefix(cmd, "docker compose version"):
		return "2.29.1", nil
	case strings.HasPrefix(cmd, "docker buildx version"):
		return "github.com/docker/buildx v0.16.1 x", nil
	}
	return "", errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock")
}

type offlineRegistry struct{}

func (offlineRegistry) Resolve(context.Context, string, string) (*registry.Image, error) {
	return nil, fmt.Errorf("%w: dial tcp: lookup registry-1.docker.io: no such host", registry.ErrNetwork)
}

func sampleProject(t *testing.T) string {
	dir := t.TempDir()
	files := map[string]string{
		"compose.yaml": "services:\n  web: {build: ., ports: [\"8080:80\"]}\n  api: {image: node:20, ports: [\"8080:3000\"]}\n",
		"Dockerfile":   "FROM nginx\nCOPY dist /usr/share/nginx/html\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestRootCausesBlockDependentsOnly(t *testing.T) {
	rep, err := Run(context.Background(), Options{Dir: sampleProject(t), Runner: daemonDown{}, Resolver: offlineRegistry{}})
	if err != nil {
		t.Fatal(err)
	}

	roots := map[fact.ID][]string{}
	for _, r := range rep.Roots {
		roots[r.Fact] = r.Blocks
	}
	if len(roots) != 2 {
		t.Fatalf("want 2 root causes (daemon, registry), got %+v", rep.Roots)
	}
	if !contains(roots[fact.Daemon], "resources.memory") || !contains(roots[fact.Daemon], "image.platform") {
		t.Errorf("daemon should block memory and platform checks: %v", roots[fact.Daemon])
	}
	if !contains(roots[fact.Registry], "image.exists") {
		t.Errorf("registry should block image.exists: %v", roots[fact.Registry])
	}

	// Checks that need neither still run and find real problems.
	var got []string
	for _, f := range rep.Findings {
		got = append(got, f.Rule)
	}
	for _, want := range []string{"build.context", "ports.conflict"} {
		if !contains(got, want) {
			t.Errorf("missing %s finding; got %v", want, got)
		}
	}
	for _, blocked := range []string{"resources.memory", "image.platform", "image.exists"} {
		if contains(got, blocked) || contains(rep.Passed, blocked) {
			t.Errorf("%s ran although it is blocked", blocked)
		}
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	snap := host.Capture(context.Background(), daemonDown{})
	b, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "host.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := host.LoadSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), Options{Dir: sampleProject(t), Host: loaded, Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rep.Roots {
		if r.Fact == fact.Daemon {
			found = true
		}
	}
	if !found {
		t.Errorf("daemon failure should survive the snapshot: %+v", rep.Roots)
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
