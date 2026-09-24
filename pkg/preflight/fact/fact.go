// Package fact defines the facts preflight depends on and the causal graph
// between them. A fact "fails" when it cannot be determined; every rule that
// needs a failed fact is reported as blocked by that fact's root cause instead
// of producing its own, misleading, failure.
package fact

import "sort"

type ID string

const (
	DockerCLI   ID = "docker.cli"
	Daemon      ID = "docker.daemon"
	LocalDaemon ID = "docker.local"
	Compose     ID = "docker.compose"
	Buildx      ID = "docker.buildx"
	Registry    ID = "registry.network"
	ComposeFile ID = "project.compose"
	Dockerfile  ID = "project.dockerfile"

	Kubectl     ID = "k8s.kubectl"
	Cluster     ID = "k8s.cluster"
	ClusterAPIs ID = "k8s.apis"
	Nodes       ID = "k8s.nodes"
	Storage     ID = "k8s.storage"
	Manifests   ID = "project.manifests"
)

// parents encodes the causal edges: a fact cannot be known unless its parent
// is known. LocalDaemon ("the daemon endpoint is on this machine") comes
// from the CLI context, so host-side checks still run when the daemon is
// down.
var parents = map[ID]ID{
	Daemon:      DockerCLI,
	LocalDaemon: DockerCLI,
	Compose:     DockerCLI,
	Buildx:      DockerCLI,

	Cluster:     Kubectl,
	ClusterAPIs: Cluster,
	Nodes:       Cluster,
	Storage:     Cluster,
}

// Parent returns the direct causal parent of id, if any.
func Parent(id ID) (ID, bool) {
	p, ok := parents[id]
	return p, ok
}

type Severity string

const (
	Error   Severity = "ERROR"
	Warning Severity = "WARN"
	Info    Severity = "INFO"
)

// Failure explains why a fact could not be determined.
type Failure struct {
	Fact     ID
	Severity Severity
	Summary  string
	Detail   string
	Fix      string
}

// Set records which facts failed.
type Set struct {
	failed map[ID]Failure
}

func NewSet() *Set {
	return &Set{failed: map[ID]Failure{}}
}

func (s *Set) Fail(f Failure) {
	if _, exists := s.failed[f.Fact]; !exists {
		s.failed[f.Fact] = f
	}
}

// Failed reports whether id is unknown, either directly or because an
// ancestor failed.
func (s *Set) Failed(id ID) bool {
	_, ok := s.Root(id)
	return ok
}

// Root returns the top-most failed fact on id's ancestor chain. Fixing the
// root is what unblocks id.
func (s *Set) Root(id ID) (Failure, bool) {
	var root Failure
	found := false
	for cur, ok := id, true; ok; cur, ok = Parent(cur) {
		if f, failed := s.failed[cur]; failed {
			root, found = f, true
		}
	}
	return root, found
}

// All returns every recorded failure.
func (s *Set) All() []Failure {
	var out []Failure
	for _, f := range s.failed {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Fact < out[j].Fact })
	return out
}

// Roots returns every failed fact whose ancestors are all known.
func (s *Set) Roots() []Failure {
	var out []Failure
	for id, f := range s.failed {
		if r, _ := s.Root(id); r.Fact == id {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Fact < out[j].Fact })
	return out
}
