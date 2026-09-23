// Package preflight predicts whether a container project will build and run
// on this host before anything is executed. It joins requirements extracted
// from the project with a capability profile of the host and live registry
// metadata, and reports each predicted failure once, at its root cause.
package preflight

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/raphgm/container-doctor/internal/executor"
	"github.com/raphgm/container-doctor/pkg/preflight/fact"
	"github.com/raphgm/container-doctor/pkg/preflight/host"
	"github.com/raphgm/container-doctor/pkg/preflight/learn"
	"github.com/raphgm/container-doctor/pkg/preflight/project"
	"github.com/raphgm/container-doctor/pkg/preflight/registry"
	"github.com/raphgm/container-doctor/pkg/preflight/rules"
)

type Options struct {
	Dir     string
	Offline bool

	// Host, when set, replaces probing this machine: preflight predicts
	// for the machine the snapshot was taken on.
	Host *host.Snapshot

	Runner   executor.Runner
	Resolver registry.Resolver
	Rules    []rules.Rule
}

// RootCause is a fact that could not be established, with every rule it
// blocked. Fixing it is what makes those rules checkable.
type RootCause struct {
	fact.Failure
	Blocks []string `json:"blocks"`
}

type Report struct {
	Project   string          `json:"project"`
	Dir       string          `json:"dir"`
	HostName  string          `json:"hostName,omitempty"`
	Host      *host.Profile   `json:"host"`
	Roots     []RootCause     `json:"rootCauses"`
	Findings  []rules.Finding `json:"findings"`
	Passed    []string        `json:"passed"`
	Duration  time.Duration   `json:"duration"`
	Timestamp time.Time       `json:"timestamp"`
}

// Errors counts findings and root causes that will break the project.
func (r *Report) Errors() int {
	n := 0
	for _, f := range r.Findings {
		if f.Severity == fact.Error {
			n++
		}
	}
	for _, rc := range r.Roots {
		if rc.Severity == fact.Error {
			n++
		}
	}
	return n
}

// Prepare loads the project, profiles the host and resolves images: every
// input the rules read.
func Prepare(ctx context.Context, opts Options) (*rules.Env, error) {
	if opts.Runner == nil {
		opts.Runner = executor.New()
	}
	if opts.Resolver == nil {
		opts.Resolver = registry.NewRemote()
	}
	var h *host.Profile
	facts := fact.NewSet()
	if opts.Host != nil {
		h, facts = opts.Host.Restore()
	}
	proj, err := project.Load(ctx, opts.Dir, facts)
	if err != nil {
		return nil, err
	}
	if h == nil {
		h = host.Probe(ctx, opts.Runner, facts)
	}

	env := &rules.Env{Project: proj, Host: h, Facts: facts}
	if opts.Offline {
		facts.Fail(fact.Failure{
			Fact:     fact.Registry,
			Severity: fact.Info,
			Summary:  "Registry checks skipped (--offline)",
		})
	} else {
		env.Images = resolveImages(ctx, opts.Resolver, rules.Pulls(proj, h), facts)
	}
	return env, nil
}

// Run predicts the failures of the project in opts.Dir.
func Run(ctx context.Context, opts Options) (*Report, error) {
	start := time.Now()
	env, err := Prepare(ctx, opts)
	if err != nil {
		return nil, err
	}
	if opts.Rules == nil {
		opts.Rules = rules.All()
		learned, err := learn.Load(env.Project.Dir)
		if err != nil {
			return nil, err
		}
		opts.Rules = append(opts.Rules, learned.Active()...)
	}
	return Evaluate(ctx, env, opts.Rules, opts.Host, start), nil
}

// Evaluate runs rules against a prepared environment.
func Evaluate(ctx context.Context, env *rules.Env, rs []rules.Rule, snap *host.Snapshot, start time.Time) *Report {
	proj, h, facts := env.Project, env.Host, env.Facts
	rep := &Report{Project: proj.Name, Dir: proj.Dir, Host: h, Timestamp: start}
	if snap != nil {
		rep.HostName = snap.Hostname
	}
	blocked := map[fact.ID][]string{}
	for _, r := range rs {
		if root, ok := blockingRoot(facts, r.Needs()); ok {
			blocked[root.Fact] = append(blocked[root.Fact], r.ID())
			continue
		}
		found := r.Check(ctx, env)
		if len(found) == 0 {
			rep.Passed = append(rep.Passed, r.ID())
		}
		rep.Findings = append(rep.Findings, found...)
	}

	for _, root := range facts.Roots() {
		rep.Roots = append(rep.Roots, RootCause{Failure: root, Blocks: blocked[root.Fact]})
	}
	sort.SliceStable(rep.Findings, func(i, j int) bool {
		return rank(rep.Findings[i].Severity) < rank(rep.Findings[j].Severity)
	})
	rep.Duration = time.Since(start)
	return rep
}

func blockingRoot(facts *fact.Set, needs []fact.ID) (fact.Failure, bool) {
	for _, id := range needs {
		if root, ok := facts.Root(id); ok {
			return root, true
		}
	}
	return fact.Failure{}, false
}

// resolveImages fetches registry metadata for all pulls in parallel. If the
// network is down, that becomes one root cause rather than one error per
// image.
func resolveImages(ctx context.Context, res registry.Resolver, pulls []rules.Pull, facts *fact.Set) map[string]rules.ImageResult {
	out := map[string]rules.ImageResult{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)

	for _, p := range pulls {
		key := rules.ImageKey(p.Ref, p.Platform)
		mu.Lock()
		if _, dup := out[key]; dup {
			mu.Unlock()
			continue
		}
		out[key] = rules.ImageResult{}
		mu.Unlock()

		wg.Add(1)
		go func(p rules.Pull, key string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			img, err := res.Resolve(cctx, p.Ref, p.Platform)
			mu.Lock()
			out[key] = rules.ImageResult{Image: img, Err: err}
			mu.Unlock()
		}(p, key)
	}
	wg.Wait()

	for _, r := range out {
		if errors.Is(r.Err, registry.ErrNetwork) {
			facts.Fail(fact.Failure{
				Fact:     fact.Registry,
				Severity: fact.Warning,
				Summary:  "Container registries are unreachable",
				Detail:   r.Err.Error(),
				Fix:      "Check network/proxy settings. Pulls will fail unless images are already cached locally.",
			})
			break
		}
	}
	return out
}

func rank(s fact.Severity) int {
	switch s {
	case fact.Error:
		return 0
	case fact.Warning:
		return 1
	}
	return 2
}
