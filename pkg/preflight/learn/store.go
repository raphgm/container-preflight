package learn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/raphgm/container-doctor/pkg/preflight/fact"
	"github.com/raphgm/container-doctor/pkg/preflight/rules"
)

// File is where a project's learned rules live. It is meant to be committed,
// so a failure one teammate hits is predicted for everyone.
const File = ".container-doctor/learned.yaml"

// relevant lists the features a new rule starts with. Host details that
// rarely cause failures (engine, Compose version) are recorded but not used
// as conditions until evidence requires them.
var relevant = []string{"image", "run.platform", "emulation", "memory", "buildkit"}

// applies drops starting conditions that cannot be the cause: the builder
// for a service that is not built, or the platform when it is native.
func applies(k, v string, features map[string]string) bool {
	switch k {
	case "buildkit":
		return features["build"] == "yes"
	case "run.platform":
		return v != features["host.platform"]
	}
	return true
}

// normal values are the unremarkable state of a feature; a single failure
// does not make them part of a rule.
var normal = map[string]string{"emulation": "none", "buildkit": "yes", "memory": ">=8GiB"}

type Store struct {
	path  string
	Rules []*Learned `yaml:"rules"`
}

type Learned struct {
	ID        string            `yaml:"id"`
	Signature string            `yaml:"signature"`
	Example   string            `yaml:"example"`
	When      map[string]string `yaml:"when"`
	Fix       string            `yaml:"fix,omitempty"`
	Status    string            `yaml:"status"`
	Failures  []Observation     `yaml:"failures"`
	Successes []Observation     `yaml:"successes,omitempty"`
}

type Observation struct {
	At       time.Time         `yaml:"at"`
	Host     string            `yaml:"host"`
	Service  string            `yaml:"service,omitempty"`
	ExitCode int               `yaml:"exitCode,omitempty"`
	Features map[string]string `yaml:"features"`
}

const (
	Active     = "active"
	Unreliable = "unreliable"
)

func Load(dir string) (*Store, error) {
	s := &Store{path: filepath.Join(dir, File)}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("%s: %w", s.path, err)
	}
	return s, nil
}

func (s *Store) Save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	header := "# Rules learned from real failures by `container-doctor learn`.\n# Commit this file so preflight predicts these failures for everyone.\n"
	return os.WriteFile(s.path, append([]byte(header), b...), 0o644)
}

func (s *Store) Path() string { return s.path }

// Outcome says what an observation did to the store.
type Outcome struct {
	Rule    *Learned
	Created bool
	// Dropped lists conditions removed because a new failure did not share
	// them (the rule became more general).
	Dropped []string
}

// ObserveFailure records a failure. A failure whose signature matches an
// existing rule generalizes it: conditions the two failures disagree on are
// dropped. Otherwise a new rule is created.
func (s *Store) ObserveFailure(f Failure, features map[string]string, hostName string) Outcome {
	obs := Observation{At: time.Now().UTC(), Host: hostName, Service: f.Service, ExitCode: f.ExitCode, Features: features}

	for _, r := range s.Rules {
		if !r.matchesLine(f.Line) {
			continue
		}
		r.Failures = append(r.Failures, obs)
		var dropped []string
		for k, v := range r.When {
			if value(features, k) != v {
				delete(r.When, k)
				dropped = append(dropped, k+"="+v)
			}
		}
		sort.Strings(dropped)
		r.Status = r.recomputeStatus()
		return Outcome{Rule: r, Dropped: dropped}
	}

	when := map[string]string{}
	for _, k := range relevant {
		v, ok := features[k]
		if !ok || v == "" || v == "unknown" || normal[k] == v || !applies(k, v, features) {
			continue
		}
		when[k] = v
	}
	r := &Learned{
		ID:        s.nextID(features["image"]),
		Signature: Signature(f.Line),
		Example:   f.Line,
		When:      when,
		Status:    Active,
		Failures:  []Observation{obs},
	}
	s.Rules = append(s.Rules, r)
	return Outcome{Rule: r, Created: true}
}

// ObserveSuccess records a run that worked. Any active rule whose conditions
// all hold for it is contradicted: its conditions are too general. If the
// failures agree on a feature where the success differs, that feature is
// added as a condition (the rule is specialized); otherwise the rule is
// marked unreliable.
func (s *Store) ObserveSuccess(service string, features map[string]string, hostName string) []*Learned {
	var touched []*Learned
	for _, r := range s.Rules {
		if !r.holds(features) {
			continue
		}
		r.Successes = append(r.Successes, Observation{At: time.Now().UTC(), Host: hostName, Service: service, Features: features})
		if k, v, ok := r.distinguishing(features); ok {
			r.When[k] = v
		}
		r.Status = r.recomputeStatus()
		touched = append(touched, r)
	}
	return touched
}

// distinguishing finds a feature on which every recorded failure agrees and
// the success differs.
func (r *Learned) distinguishing(success map[string]string) (string, string, bool) {
	// Candidates include features only the success has: a variable the
	// failures lacked is a condition "unset".
	keys := map[string]bool{}
	for k := range r.Failures[0].Features {
		keys[k] = true
	}
	for k := range success {
		keys[k] = true
	}
	var sorted []string
	for k := range keys {
		sorted = append(sorted, k)
	}
	// Prefer the curated features, then the rest alphabetically.
	sort.SliceStable(sorted, func(i, j int) bool {
		return rank(sorted[i]) < rank(sorted[j]) || (rank(sorted[i]) == rank(sorted[j]) && sorted[i] < sorted[j])
	})
	for _, k := range sorted {
		if _, already := r.When[k]; already {
			continue
		}
		v := value(r.Failures[0].Features, k)
		agree := true
		for _, f := range r.Failures[1:] {
			if value(f.Features, k) != v {
				agree = false
			}
		}
		if agree && value(success, k) != v {
			return k, v, true
		}
	}
	return "", "", false
}

func rank(k string) int {
	for i, r := range relevant {
		if r == k {
			return i
		}
	}
	return len(relevant)
}

// recomputeStatus is unreliable while some success still satisfies every
// condition.
func (r *Learned) recomputeStatus() string {
	for _, s := range r.Successes {
		if r.holds(s.Features) {
			return Unreliable
		}
	}
	return Active
}

func (r *Learned) holds(features map[string]string) bool {
	for k, v := range r.When {
		if value(features, k) != v {
			return false
		}
	}
	return true
}

// unset is the value of a feature that was not observed, e.g. an
// environment variable the service does not define.
const unset = "unset"

func value(features map[string]string, k string) string {
	if v, ok := features[k]; ok && v != "" {
		return v
	}
	return unset
}

func (r *Learned) matchesLine(line string) bool {
	re, err := regexp.Compile(r.Signature)
	return err == nil && re.MatchString(line)
}

// Matches reports whether line is this rule's failure.
func (r *Learned) Matches(line string) bool { return r.matchesLine(line) }

func (s *Store) nextID(image string) string {
	base := "learned"
	if image != "" {
		base += "." + strings.ReplaceAll(filepath.Base(image), "_", "-")
	}
	id := base
	for n := 2; s.byID(id) != nil; n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	return id
}

func (s *Store) byID(id string) *Learned {
	for _, r := range s.Rules {
		if r.ID == id {
			return r
		}
	}
	return nil
}

// Rules exposes the active learned rules to the preflight engine.
func (s *Store) Active() []rules.Rule {
	var out []rules.Rule
	for _, r := range s.Rules {
		if r.Status == Active {
			out = append(out, learnedRule{r})
		}
	}
	return out
}

type learnedRule struct{ l *Learned }

func (r learnedRule) ID() string    { return r.l.ID }
func (r learnedRule) Title() string { return "Learned: " + r.l.Example }
func (r learnedRule) Needs() []fact.ID {
	return []fact.ID{fact.Daemon, fact.ComposeFile}
}

func (r learnedRule) Check(_ context.Context, env *rules.Env) []rules.Finding {
	var out []rules.Finding
	for _, svc := range env.Project.Services {
		features := rules.Features(env, svc)
		if len(r.l.When) == 0 || !r.l.holds(features) {
			continue
		}
		var conds []string
		for k, v := range r.l.When {
			conds = append(conds, k+"="+v)
		}
		sort.Strings(conds)
		first := r.l.Failures[0]
		evidence := []string{
			fmt.Sprintf("learned: failed %d time(s), first on %s (%s)", len(r.l.Failures), first.Host, first.At.Format("2006-01-02")),
			"conditions now true here: " + strings.Join(conds, ", "),
		}
		if n := len(r.l.Successes); n > 0 {
			evidence = append(evidence, fmt.Sprintf("refined by %d successful run(s)", n))
		}
		fix := r.l.Fix
		if fix == "" {
			fix = "Seen before under the same conditions. Change one of them, or add a fix to " + File + "."
		}
		out = append(out, rules.Finding{
			Rule:     r.l.ID,
			Severity: fact.Error,
			Service:  svc.Name,
			Title:    "this failed before under the same conditions: " + r.l.Example,
			Evidence: evidence,
			Fix:      fix,
			Location: svc.Location.String(),
			Predicts: r.l.Example,
		})
	}
	return out
}
