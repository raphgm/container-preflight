package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"strings"
	"time"

	"github.com/raphgm/container-preflight/internal/executor"
	"github.com/raphgm/container-preflight/pkg/preflight/fact"
	"github.com/raphgm/container-preflight/pkg/preflight/registry"
)

// Cluster is what a specific cluster offers workloads.
type Cluster struct {
	Context       string
	ServerVersion string
	Nodes         []Node
	// Kinds maps "group/Kind" (core group: "/Kind") to true when served.
	Kinds map[string]bool
	// Versions lists served group/versions ("v1", "apps/v1", ...).
	Versions map[string]bool
	// Preferred maps a Kind to the apiVersion the cluster prefers for it.
	Preferred      map[string]string
	StorageClasses []string
	DefaultClass   string
}

type Node struct {
	Name          string
	Labels        map[string]string
	Taints        []Taint
	CPU           int64 // allocatable millicores
	Memory        int64 // allocatable bytes
	Extended      map[string]int64
	Platform      string // os/arch
	Unschedulable bool
	Ready         bool
}

type Taint struct{ Key, Value, Effect string }

// commandTimeout bounds each kubectl call; an unreachable API server can
// otherwise hang for minutes.
const commandTimeout = 20 * time.Second

type timeoutRunner struct{ executor.Runner }

func (t timeoutRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	out, err := t.Runner.Run(cctx, name, args...)
	if err != nil && cctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("`%s %s` did not answer within %s", name, strings.Join(args, " "), commandTimeout)
	}
	return out, err
}

// Probe reads the cluster with get/list calls only. Anything it cannot read
// becomes a fact failure, so dependent rules report the root cause instead
// of failing individually.
func Probe(ctx context.Context, run executor.Runner, kubeContext string, facts *fact.Set) *Cluster {
	run = timeoutRunner{run}
	c := &Cluster{Kinds: map[string]bool{}, Versions: map[string]bool{}, Preferred: map[string]string{}}
	kc := func(args ...string) []string {
		if kubeContext != "" {
			return append([]string{"--context", kubeContext}, args...)
		}
		return args
	}

	if _, err := run.Run(ctx, "kubectl", "version", "--client", "-o", "json"); err != nil {
		facts.Fail(fact.Failure{Fact: fact.Kubectl, Severity: fact.Error, Summary: "kubectl not found",
			Detail: errText(err), Fix: "Install kubectl and make sure it is on PATH."})
		return c
	}
	c.Context = kubeContext
	if c.Context == "" {
		c.Context, _ = run.Run(ctx, "kubectl", "config", "current-context")
	}

	out, err := run.Run(ctx, "kubectl", kc("version", "-o", "json")...)
	var ver struct {
		ServerVersion struct{ GitVersion string } `json:"serverVersion"`
	}
	if err == nil {
		_ = json.Unmarshal([]byte(out), &ver)
	}
	if ver.ServerVersion.GitVersion == "" {
		summary := "Kubernetes API server is not reachable"
		if c.Context == "" {
			summary = "kubectl has no current context"
		} else {
			summary += " (context " + c.Context + ")"
		}
		facts.Fail(fact.Failure{Fact: fact.Cluster, Severity: fact.Error, Summary: summary, Detail: errText(err),
			Fix: "Check `kubectl config get-contexts`, start the cluster (e.g. `kind create cluster`), or pass --context."})
		return c
	}
	c.ServerVersion = ver.ServerVersion.GitVersion

	if out, err := run.Run(ctx, "kubectl", kc("api-versions")...); err == nil {
		for _, v := range strings.Fields(out) {
			c.Versions[v] = true
		}
	}
	if out, err := run.Run(ctx, "kubectl", kc("api-resources", "--no-headers")...); err != nil {
		facts.Fail(fact.Failure{Fact: fact.ClusterAPIs, Severity: fact.Warning, Summary: "cannot list the cluster's API resources", Detail: errText(err)})
	} else {
		for _, line := range strings.Split(out, "\n") {
			f := strings.Fields(line)
			if len(f) < 4 {
				continue
			}
			// NAME [SHORTNAMES] APIVERSION NAMESPACED KIND
			gv, kind := f[len(f)-3], f[len(f)-1]
			c.Kinds[group(gv)+"/"+kind] = true
			if _, dup := c.Preferred[kind]; !dup {
				c.Preferred[kind] = gv
			}
		}
	}

	if out, err := run.Run(ctx, "kubectl", kc("get", "nodes", "-o", "json")...); err != nil {
		facts.Fail(fact.Failure{Fact: fact.Nodes, Severity: fact.Warning, Summary: "cannot list nodes", Detail: errText(err),
			Fix: "Scheduling and platform checks need `get nodes` permission (cluster-scoped)."})
	} else if err := c.parseNodes(out); err != nil {
		facts.Fail(fact.Failure{Fact: fact.Nodes, Severity: fact.Warning, Summary: "cannot read node list", Detail: err.Error()})
	}

	if out, err := run.Run(ctx, "kubectl", kc("get", "storageclasses", "-o", "json")...); err != nil {
		facts.Fail(fact.Failure{Fact: fact.Storage, Severity: fact.Warning, Summary: "cannot list StorageClasses", Detail: errText(err)})
	} else {
		var sc struct {
			Items []struct {
				Metadata struct {
					Name        string
					Annotations map[string]string
				}
			}
		}
		if json.Unmarshal([]byte(out), &sc) == nil {
			for _, it := range sc.Items {
				c.StorageClasses = append(c.StorageClasses, it.Metadata.Name)
				if it.Metadata.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
					c.DefaultClass = it.Metadata.Name
				}
			}
		}
	}
	return c
}

func (c *Cluster) parseNodes(out string) error {
	var nl struct {
		Items []struct {
			Metadata struct {
				Name   string
				Labels map[string]string
			}
			Spec struct {
				Unschedulable bool
				Taints        []Taint
			}
			Status struct {
				Allocatable map[string]string
				NodeInfo    struct {
					Architecture    string
					OperatingSystem string
				}
				Conditions []struct{ Type, Status string }
			}
		}
	}
	if err := json.Unmarshal([]byte(out), &nl); err != nil {
		return err
	}
	for _, it := range nl.Items {
		n := Node{Name: it.Metadata.Name, Labels: it.Metadata.Labels, Taints: it.Spec.Taints,
			Unschedulable: it.Spec.Unschedulable, Extended: map[string]int64{}}
		n.CPU, _ = quantity(it.Status.Allocatable["cpu"], true)
		n.Memory, _ = quantity(it.Status.Allocatable["memory"], false)
		for k, v := range it.Status.Allocatable {
			if strings.Contains(k, "/") {
				n.Extended[k], _ = quantity(v, false)
			}
		}
		n.Platform = registry.Normalize(it.Status.NodeInfo.OperatingSystem + "/" + it.Status.NodeInfo.Architecture)
		for _, cond := range it.Status.Conditions {
			if cond.Type == "Ready" {
				n.Ready = cond.Status == "True"
			}
		}
		c.Nodes = append(c.Nodes, n)
	}
	return nil
}

// Local reports whether the cluster runs inside a developer's Docker engine
// (minikube, kind, Docker Desktop, k3d, Rancher Desktop, OrbStack). Their
// nodes inherit the engine VM's emulation, so foreign-architecture images
// usually run, slowly; production nodes usually cannot run them at all.
func (c *Cluster) Local() bool {
	ctx := strings.ToLower(c.Context)
	for _, p := range []string{"minikube", "kind-", "docker-desktop", "k3d-", "rancher-desktop", "orbstack", "colima"} {
		if strings.HasPrefix(ctx, p) {
			return true
		}
	}
	return false
}

// Platforms lists the distinct platforms of schedulable nodes.
func (c *Cluster) Platforms() []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range c.Nodes {
		if n.Ready && !n.Unschedulable && !seen[n.Platform] {
			seen[n.Platform] = true
			out = append(out, n.Platform)
		}
	}
	return out
}

// Serves reports whether apiVersion/kind is available.
func (c *Cluster) Serves(apiVersion, kind string) (servedKind, servedVersion bool) {
	return c.Kinds[group(apiVersion)+"/"+kind], c.Versions[apiVersion]
}

// ServedAs returns the apiVersion under which the cluster serves kind, when
// a manifest used another group or version for it.
func (c *Cluster) ServedAs(kind string) string {
	return c.Preferred[kind]
}

func group(apiVersion string) string {
	if g, _, ok := strings.Cut(apiVersion, "/"); ok {
		return g
	}
	return ""
}

// Snapshot is a portable cluster profile.
type Snapshot struct {
	Version    int            `json:"version"`
	CapturedAt time.Time      `json:"capturedAt"`
	Cluster    *Cluster       `json:"cluster"`
	Failures   []fact.Failure `json:"failures,omitempty"`
}

func Capture(ctx context.Context, run executor.Runner, kubeContext string) *Snapshot {
	facts := fact.NewSet()
	c := Probe(ctx, run, kubeContext, facts)
	return &Snapshot{Version: 1, CapturedAt: time.Now().UTC(), Cluster: c, Failures: facts.All()}
}

func LoadSnapshot(path string) (*Snapshot, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil || s.Cluster == nil {
		return nil, fmt.Errorf("%s: not a cluster snapshot", path)
	}
	return &s, nil
}

func (s *Snapshot) Restore() (*Cluster, *fact.Set) {
	facts := fact.NewSet()
	for _, f := range s.Failures {
		facts.Fail(f)
	}
	return s.Cluster, facts
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, osexec.ErrNotFound) {
		return "`kubectl` executable not found on PATH"
	}
	return strings.TrimPrefix(strings.TrimSpace(err.Error()), "exit status 1: ")
}
