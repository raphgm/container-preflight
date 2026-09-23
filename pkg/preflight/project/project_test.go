package project

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/raphgm/container-doctor/pkg/preflight/fact"
)

func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDockerfileStagesAndReachability(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{"Dockerfile": `ARG NODE=20
FROM node:${NODE}-alpine AS deps
COPY package.json ./
RUN --mount=type=cache,target=/root/.npm npm ci

FROM golang:1.22 AS unused
COPY missing.go ./

FROM --platform=$BUILDPLATFORM deps AS build
COPY --chmod=755 src ./src

FROM gcr.io/distroless/nodejs20
COPY --from=build /app /app
`})

	df, err := ParseDockerfile(filepath.Join(dir, "Dockerfile"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := df.Stages[0].Base; got != "node:20-alpine" {
		t.Errorf("ARG not expanded: base = %q", got)
	}
	if df.Stages[2].BaseStage != 0 {
		t.Errorf("stage 2 should derive from stage 0, got %d", df.Stages[2].BaseStage)
	}
	if df.Stages[2].Platform != "$BUILDPLATFORM" {
		t.Errorf("platform ARG must be left for the builder, got %q", df.Stages[2].Platform)
	}

	reach, _ := df.Reachable("")
	if want := []int{0, 2, 3}; !equal(reach, want) {
		t.Errorf("reachable = %v; want %v (stage 1 is never built)", reach, want)
	}

	var names []string
	for _, f := range df.BuildKitFeatures {
		names = append(names, f.Name)
	}
	if !contains(names, "RUN --mount") || !contains(names, "COPY --chmod") {
		t.Errorf("BuildKit features = %v", names)
	}
}

func TestBuildArgOverridesDefault(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{"Dockerfile": "ARG TAG=1\nFROM alpine:${TAG}\n"})
	df, err := ParseDockerfile(filepath.Join(dir, "Dockerfile"), map[string]string{"TAG": "3.20"})
	if err != nil {
		t.Fatal(err)
	}
	if df.Stages[0].Base != "alpine:3.20" {
		t.Errorf("base = %q", df.Stages[0].Base)
	}
}

func TestLoadCompose(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{
		"compose.yaml": `include:
  - other.yaml
services:
  db:
    image: postgres:16
    mem_limit: 512m
    ports: ["5432:5432"]
    environment:
      POSTGRES_PASSWORD: ${DB_PASSWORD:?set a password}
  web:
    build: .
    platform: linux/amd64
    ports: ["8000-8001:80"]
    volumes:
      - ./nginx.conf:/etc/nginx/nginx.conf:ro
      - data:/data
    deploy:
      replicas: 2
      resources:
        reservations:
          devices: [{capabilities: [gpu]}]
    develop:
      watch: [{path: ., action: rebuild}]
    depends_on:
      db: {condition: service_started, required: false}
    environment:
      MODE: ${MODE:-dev}
      TOKEN: $API_TOKEN
volumes: {data: {}}
`,
		"other.yaml": "services: {}\n",
		"Dockerfile": "FROM nginx:1.27\n",
		".env":       "DB_PASSWORD=x\n",
	})

	facts := fact.NewSet()
	p, err := Load(context.Background(), dir, facts)
	if err != nil {
		t.Fatal(err)
	}
	if facts.Failed(fact.ComposeFile) {
		r, _ := facts.Root(fact.ComposeFile)
		t.Fatalf("compose failed to load: %s", r.Detail)
	}
	if len(p.Services) != 2 {
		t.Fatalf("services = %d", len(p.Services))
	}
	db, web := p.Services[0], p.Services[1]
	if db.Image != "postgres:16" || db.MemLimit != 512*1024*1024 {
		t.Errorf("db = %+v", db)
	}
	if web.Image != "" || web.Build == nil || web.Build.Dockerfile == nil {
		t.Fatalf("web build not parsed: %+v", web)
	}
	if web.Replicas != 2 || !web.GPU || web.Platform != "linux/amd64" {
		t.Errorf("web = replicas %d gpu %v platform %q", web.Replicas, web.GPU, web.Platform)
	}
	if len(web.Ports) != 1 || web.Ports[0].Start != 8000 || web.Ports[0].End != 8001 {
		t.Errorf("ports = %+v", web.Ports)
	}
	if len(web.Binds) != 1 || web.Binds[0].Source != filepath.Join(dir, "nginx.conf") {
		t.Errorf("binds = %+v", web.Binds)
	}

	var features []string
	for _, f := range p.ComposeFeatures {
		features = append(features, f.Name)
	}
	for _, want := range []string{"include", "services.web.develop", "services.web.depends_on.required"} {
		if !contains(features, want) {
			t.Errorf("missing compose feature %q in %v", want, features)
		}
	}

	refs := map[string]EnvRef{}
	for _, r := range p.EnvRefs {
		refs[r.Name] = r
	}
	if !refs["DB_PASSWORD"].Required || !refs["MODE"].HasDefault || refs["API_TOKEN"].HasDefault {
		t.Errorf("env refs = %+v", refs)
	}
	if p.Env["DB_PASSWORD"] != "x" {
		t.Error(".env not read")
	}
}

func TestBrokenComposeIsAFactFailure(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{"compose.yaml": "services:\n  a:\n    image: [not, a, string]\n"})
	facts := fact.NewSet()
	if _, err := Load(context.Background(), dir, facts); err != nil {
		t.Fatal(err)
	}
	if !facts.Failed(fact.ComposeFile) {
		t.Fatal("expected compose file failure")
	}
}

func equal(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func TestBuildContextCheck(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{
		"Dockerfile":    "FROM alpine\n",
		".dockerignore": "*.env\nsecrets/\nnode_modules\n!keep.env\n",
		"app.py":        "",
		"prod.env":      "",
		"keep.env":      "",
		"secrets/key":   "",
	})
	bc, err := OpenContext(dir, filepath.Join(dir, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]SourceStatus{
		"app.py":        SourceOK,
		"./app.py":      SourceOK,
		"*.py":          SourceOK,
		"prod.env":      SourceIgnored,
		"keep.env":      SourceOK,
		"secrets/key":   SourceIgnored,
		"secrets":       SourceIgnored,
		"missing.txt":   SourceMissing,
		"../outside":    SourceOutside,
		"$APP_DIR":      SourceSkipped,
		"https://x.y/z": SourceSkipped,
		".":             SourceOK,
	}
	for src, want := range cases {
		if got, pattern := bc.Check(src); got != want {
			t.Errorf("Check(%q) = %v (%q); want %v", src, got, pattern, want)
		}
	}
	if _, pattern := bc.Check("prod.env"); pattern != "*.env" {
		t.Errorf("blocking pattern = %q; want *.env", pattern)
	}
}
