package engine

import (
	"github.com/raphgm/container-preflight/pkg/provider"
)

type Registry struct {
	providers []provider.Provider
}

func NewRegistry() *Registry {
	return &Registry{}
}

func (r *Registry) Register(p provider.Provider) {
	r.providers = append(r.providers, p)
}

func (r *Registry) Providers() []provider.Provider {
	return r.providers
}
