package engine

import (
	"testing"

	"github.com/raphgm/container-preflight/pkg/check"
)

// mockProvider implements the provider.Provider interface for testing
type mockProvider struct {
	name   string
	checks []check.Check
}

func (m *mockProvider) Name() string {
	return m.name
}

func (m *mockProvider) Checks() []check.Check {
	return m.checks
}

func TestRegistry_RegisterAndProviders(t *testing.T) {
	reg := NewRegistry()

	if len(reg.Providers()) != 0 {
		t.Errorf("expected 0 providers, got %d", len(reg.Providers()))
	}

	p1 := &mockProvider{name: "Mock Provider 1"}
	p2 := &mockProvider{name: "Mock Provider 2"}

	reg.Register(p1)
	reg.Register(p2)

	providers := reg.Providers()
	if len(providers) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(providers))
	}

	if providers[0].Name() != "Mock Provider 1" {
		t.Errorf("expected Mock Provider 1, got %s", providers[0].Name())
	}
	if providers[1].Name() != "Mock Provider 2" {
		t.Errorf("expected Mock Provider 2, got %s", providers[1].Name())
	}
}
