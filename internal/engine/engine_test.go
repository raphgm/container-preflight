package engine

import (
	"context"
	"testing"

	"github.com/raphgm/container-preflight/pkg/check"
	"github.com/raphgm/container-preflight/pkg/report"
)

// mockCheck implements check.Check
type mockCheck struct {
	id     string
	name   string
	result report.Result
}

func (m *mockCheck) ID() string { return m.id }
func (m *mockCheck) Name() string { return m.name }
func (m *mockCheck) Description() string { return "Mock check" }
func (m *mockCheck) Run(ctx context.Context) report.Result {
	return m.result
}

func TestEngine_Run(t *testing.T) {
	reg := NewRegistry()

	passCheck := &mockCheck{
		id:   "test.pass",
		name: "Passing Test",
		result: report.Result{
			Status: report.Pass,
		},
	}
	failCheck := &mockCheck{
		id:   "test.fail",
		name: "Failing Test",
		result: report.Result{
			Status: report.Fail,
		},
	}

	p1 := &mockProvider{
		name:   "Mock Provider 1",
		checks: []check.Check{passCheck, failCheck},
	}
	
	p2 := &mockProvider{
		name:   "Mock Provider 2",
		checks: []check.Check{passCheck},
	}

	reg.Register(p1)
	reg.Register(p2)

	eng := New(reg)
	rep := eng.Run(context.Background())

	if len(rep.Providers) != 2 {
		t.Fatalf("expected 2 provider reports, got %d", len(rep.Providers))
	}

	if rep.Providers[0].Name != "Mock Provider 1" {
		t.Errorf("expected provider 1 name to match, got %s", rep.Providers[0].Name)
	}

	if len(rep.Providers[0].Results) != 2 {
		t.Fatalf("expected 2 results for provider 1, got %d", len(rep.Providers[0].Results))
	}

	if rep.Providers[0].Results[0].Status != report.Pass {
		t.Errorf("expected first check to pass")
	}

	if rep.Providers[0].Results[1].Status != report.Fail {
		t.Errorf("expected second check to fail")
	}
}
