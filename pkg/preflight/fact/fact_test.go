package fact

import "testing"

func TestRootWalksToTopMostFailure(t *testing.T) {
	s := NewSet()
	s.Fail(Failure{Fact: DockerCLI, Summary: "no docker"})
	s.Fail(Failure{Fact: Daemon, Summary: "daemon down"})

	r, ok := s.Root(Daemon)
	if !ok || r.Fact != DockerCLI {
		t.Fatalf("root of Daemon = %v, %v; want DockerCLI", r.Fact, ok)
	}
	roots := s.Roots()
	if len(roots) != 1 || roots[0].Fact != DockerCLI {
		t.Fatalf("roots = %+v; want only DockerCLI", roots)
	}
}

func TestDaemonDownLeavesHostFactsKnown(t *testing.T) {
	s := NewSet()
	s.Fail(Failure{Fact: Daemon, Summary: "daemon down"})
	if s.Failed(LocalDaemon) || s.Failed(Compose) {
		t.Fatal("endpoint locality and Compose do not depend on the daemon")
	}
}

func TestCLIFailureBlocksEverythingDockerSide(t *testing.T) {
	s := NewSet()
	s.Fail(Failure{Fact: DockerCLI})
	for _, id := range []ID{Daemon, LocalDaemon, Compose, Buildx} {
		if r, _ := s.Root(id); r.Fact != DockerCLI {
			t.Errorf("root of %s = %s; want %s", id, r.Fact, DockerCLI)
		}
	}
	if s.Failed(Registry) {
		t.Error("registry reachability does not depend on the docker CLI")
	}
}
