package preflight

import (
	"context"
	"sort"
	"time"

	"github.com/raphgm/container-preflight/internal/executor"
	"github.com/raphgm/container-preflight/pkg/preflight/fact"
	"github.com/raphgm/container-preflight/pkg/preflight/k8s"
	"github.com/raphgm/container-preflight/pkg/preflight/registry"
	"github.com/raphgm/container-preflight/pkg/preflight/rules"
)

type K8sOptions struct {
	Paths    []string
	Context  string
	Offline  bool
	Snapshot *k8s.Snapshot
	Runner   executor.Runner
	Resolver registry.Resolver
}

// RunK8s predicts what applying the manifests to a cluster would fail on.
func RunK8s(ctx context.Context, opts K8sOptions) (*Report, error) {
	start := time.Now()
	if opts.Runner == nil {
		opts.Runner = executor.New()
	}
	if opts.Resolver == nil {
		opts.Resolver = registry.NewRemote()
	}
	m, err := k8s.Load(opts.Paths)
	if err != nil {
		return nil, err
	}

	var cluster *k8s.Cluster
	var facts *fact.Set
	if opts.Snapshot != nil {
		cluster, facts = opts.Snapshot.Restore()
	} else {
		facts = fact.NewSet()
		cluster = k8s.Probe(ctx, opts.Runner, opts.Context, facts)
	}

	env := &k8s.Env{Manifests: m, Cluster: cluster, Facts: facts, Images: map[string]rules.ImageResult{}}
	if opts.Offline {
		facts.Fail(fact.Failure{Fact: fact.Registry, Severity: fact.Info, Summary: "Registry checks skipped (--offline)"})
	} else {
		platform := "linux/amd64"
		if ps := cluster.Platforms(); len(ps) > 0 {
			platform = ps[0]
		}
		var pulls []rules.Pull
		seen := map[string]bool{}
		for _, w := range m.Workloads {
			for _, c := range append(append([]k8s.Container{}, w.Pod.InitContainers...), w.Pod.Containers...) {
				if c.Image != "" && !seen[c.Image] {
					seen[c.Image] = true
					pulls = append(pulls, rules.Pull{Ref: c.Image, Platform: platform})
				}
			}
		}
		byKey := resolveImages(ctx, opts.Resolver, pulls, facts)
		for _, p := range pulls {
			env.Images[p.Ref] = byKey[rules.ImageKey(p.Ref, p.Platform)]
		}
	}

	rep := &Report{Project: "kubernetes manifests", Cluster: cluster, Timestamp: start}
	blocked := map[fact.ID][]string{}
	for _, r := range k8s.All() {
		if root, ok := blockingRoot(facts, r.Needs); ok {
			blocked[root.Fact] = append(blocked[root.Fact], r.ID)
			continue
		}
		found := r.Check(ctx, env)
		if len(found) == 0 {
			rep.Passed = append(rep.Passed, r.ID)
		}
		rep.Findings = append(rep.Findings, found...)
	}
	for _, root := range facts.Roots() {
		rep.Roots = append(rep.Roots, RootCause{Failure: root, Blocks: blocked[root.Fact]})
	}
	sort.SliceStable(rep.Findings, func(i, j int) bool { return rank(rep.Findings[i].Severity) < rank(rep.Findings[j].Severity) })
	rep.Duration = time.Since(start)
	return rep, nil
}
