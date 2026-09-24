package provider

import "github.com/raphgm/container-preflight/pkg/check"

type Provider interface {
	Name() string

	Checks() []check.Check
}
