package learn

import (
	"regexp"
	"testing"
)

func TestExtractPicksSpecificCauseFromComposeOutput(t *testing.T) {
	out := "Attaching to db-1\n" +
		"db-1  | SQL Server 2022 will run as non-root by default.\n" +
		"db-1  | /opt/mssql/bin/sqlservr: Invalid mapping of address 0x4005353000 in reserved address space below 0x400000000000. Possible causes:\n" +
		"db-1  | 1) the process (itself, or via a wrapper) starts-up its own running environment\n" +
		"db-1 exited with code 1\n"
	f, ok := Extract(out)
	if !ok || f.Service != "db" || f.ExitCode != 1 {
		t.Fatalf("got %+v", f)
	}
	if want := "/opt/mssql/bin/sqlservr: Invalid mapping"; f.Line[:len(want)] != want {
		t.Errorf("line = %q", f.Line)
	}
}

func TestExtractIgnoresGenericWrappers(t *testing.T) {
	f, ok := Extract("Error response from daemon:\nERROR: max virtual memory areas vm.max_map_count [65530] is too low, increase to at least [262144]\nsearch-1 exited with code 78\n")
	if !ok || f.Service != "search" || f.ExitCode != 78 {
		t.Fatalf("got %+v", f)
	}
}

func TestSignatureGeneralizesVolatileParts(t *testing.T) {
	a := "/opt/mssql/bin/sqlservr: Invalid mapping of address 0x4005353000 in reserved address space below 0x400000000000. Possible causes:"
	b := "/usr/bin/sqlservr: Invalid mapping of address 0x7f00aa in reserved address space below 0x400000000000. Possible causes:"
	re := regexp.MustCompile(Signature(a))
	if !re.MatchString(a) || !re.MatchString(b) {
		t.Errorf("signature %q should match both", Signature(a))
	}
	if re.MatchString("exec format error") {
		t.Error("signature too loose")
	}
}

func features(kv ...string) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func TestLearnGeneralizeAndRefine(t *testing.T) {
	s := &Store{path: t.TempDir() + "/learned.yaml"}
	crash := Failure{Line: "WARNING: MongoDB 5.0+ requires a CPU with AVX support, and your current system does not appear to have that!", Service: "db"}

	// First failure: colima, QEMU, small VM.
	out := s.ObserveFailure(crash, features("image", "docker.io/library/mongo", "run.platform", "linux/amd64", "host.platform", "linux/arm64", "emulation", "qemu", "memory", "<2GiB", "engine", "colima", "build", "yes", "buildkit", "no"), "mac-1")
	r := out.Rule
	if !out.Created || r.When["emulation"] != "qemu" || r.When["memory"] != "<2GiB" || r.When["image"] != "docker.io/library/mongo" {
		t.Fatalf("new rule when = %v", r.When)
	}
	if _, ok := r.When["engine"]; ok {
		t.Error("engine is recorded but not a starting condition")
	}
	if r.When["run.platform"] != "linux/amd64" || r.When["buildkit"] != "no" {
		t.Errorf("foreign platform and builder of a built service are conditions: %v", r.When)
	}
	image := s.ObserveFailure(Failure{Line: "other failure xyz"}, features("image", "x", "run.platform", "linux/arm64", "host.platform", "linux/arm64", "build", "no", "buildkit", "no"), "m").Rule
	if len(image.When) != 1 {
		t.Errorf("native platform and unused builder must not be conditions: %v", image.When)
	}

	// Same failure on a bigger VM: memory was not the cause.
	out = s.ObserveFailure(crash, features("image", "docker.io/library/mongo", "run.platform", "linux/amd64", "host.platform", "linux/arm64", "emulation", "qemu", "memory", "<8GiB", "build", "yes", "buildkit", "yes"), "mac-2")
	if out.Created || len(out.Dropped) != 2 || r.When["memory"] != "" || r.When["buildkit"] != "" {
		t.Fatalf("generalization: dropped=%v when=%v", out.Dropped, r.When)
	}

	// Mongo under Rosetta works: rule does not hold, untouched.
	if n := len(s.ObserveSuccess("db", features("image", "docker.io/library/mongo", "run.platform", "linux/amd64", "emulation", "rosetta"), "mac-3")); n != 0 {
		t.Errorf("rosetta success should not touch the rule")
	}
	if r.Status != Active {
		t.Errorf("status = %s", r.Status)
	}

	// A success that satisfies every condition but differs on a feature
	// all failures share specializes the rule.
	r.When = map[string]string{"image": "docker.io/library/mongo"}
	touched := s.ObserveSuccess("db", features("image", "docker.io/library/mongo", "run.platform", "linux/arm64", "emulation", "none"), "linux-arm")
	if len(touched) != 1 || r.When["run.platform"] != "linux/amd64" || r.Status != Active {
		t.Errorf("specialize: when=%v status=%s", r.When, r.Status)
	}

	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
}

func TestSuccessLearnsMissingVariable(t *testing.T) {
	s := &Store{path: t.TempDir() + "/learned.yaml"}
	fail := Failure{Line: "Error: Database is uninitialized and superuser password is not specified.", Service: "db"}
	r := s.ObserveFailure(fail, features("image", "docker.io/library/postgres", "emulation", "none"), "mac").Rule
	if len(r.When) != 1 {
		t.Fatalf("when = %v", r.When)
	}
	s.ObserveSuccess("db", features("image", "docker.io/library/postgres", "emulation", "none", "env.POSTGRES_PASSWORD", "set"), "mac")
	if r.When["env.POSTGRES_PASSWORD"] != unset || r.Status != Active {
		t.Errorf("when = %v status = %s", r.When, r.Status)
	}
	if r.holds(features("image", "docker.io/library/postgres", "env.POSTGRES_PASSWORD", "set")) {
		t.Error("rule must not fire once the password is set")
	}
	if !r.holds(features("image", "docker.io/library/postgres")) {
		t.Error("rule must fire when the password is missing")
	}
}
