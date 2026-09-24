package k8s

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/raphgm/container-preflight/pkg/preflight/fact"
	"github.com/raphgm/container-preflight/pkg/preflight/registry"
	"github.com/raphgm/container-preflight/pkg/preflight/rules"
)

// Env is everything a Kubernetes rule may read.
type Env struct {
	Manifests *Manifests
	Cluster   *Cluster
	Facts     *fact.Set
	// Images maps an image reference to its registry lookup.
	Images map[string]rules.ImageResult
}

type Rule struct {
	ID    string
	Needs []fact.ID
	Check func(ctx context.Context, env *Env) []rules.Finding
}

func All() []Rule {
	return []Rule{apiRule, imageRule, platformRule, schedulableRule, storageRule}
}

var apiRule = Rule{
	ID:    "k8s.api",
	Needs: []fact.ID{fact.ClusterAPIs},
	Check: func(_ context.Context, env *Env) []rules.Finding {
		var out []rules.Finding
		seen := map[string]bool{}
		for _, o := range env.Manifests.Objects {
			key := o.APIVersion + " " + o.Kind
			if seen[key] {
				continue
			}
			kind, version := env.Cluster.Serves(o.APIVersion, o.Kind)
			if kind && version {
				continue
			}
			seen[key] = true
			why := fmt.Sprintf("cluster: %s/%s is not served", o.APIVersion, o.Kind)
			fix := "Install the CRD or operator that provides " + o.Kind + " before applying."
			if kind {
				why = fmt.Sprintf("cluster: %s is served, but not in version %s", o.Kind, o.APIVersion)
				fix = "Use an API version this cluster serves (`kubectl api-resources | grep " + o.Kind + "`)."
			}
			out = append(out, rules.Finding{
				Rule: "k8s.api", Severity: fact.Error, Service: o.Kind + "/" + o.Name,
				Title:    fmt.Sprintf("%s %s is not available on this cluster", o.APIVersion, o.Kind),
				Evidence: []string{why, "cluster: Kubernetes " + env.Cluster.ServerVersion},
				Fix:      fix, Location: o.Location,
				Predicts: fmt.Sprintf(`no matches for kind "%s" in version "%s"`, o.Kind, o.APIVersion),
			})
		}
		return out
	},
}

var imageRule = Rule{
	ID:    "k8s.image",
	Needs: []fact.ID{fact.Registry},
	Check: func(_ context.Context, env *Env) []rules.Finding {
		var out []rules.Finding
		for _, w := range env.Manifests.Workloads {
			for _, c := range allContainers(w.Pod) {
				res, ok := env.Images[c.Image]
				if !ok || res.Err == nil {
					continue
				}
				f := rules.Finding{Rule: "k8s.image", Service: w.Kind + "/" + w.Name, Location: w.Location}
				switch {
				case errors.Is(res.Err, registry.ErrNotFound):
					f.Severity, f.Title = fact.Error, fmt.Sprintf("image %s does not exist", c.Image)
					f.Evidence = []string{"registry: " + firstLine(res.Err)}
					f.Fix = "Fix the tag; check it on the registry's web page."
					f.Predicts = "ErrImagePull: manifest unknown / not found"
				case errors.Is(res.Err, registry.ErrUnauthorized) && len(w.Pod.ImagePullSecrets) == 0:
					f.Severity, f.Title = fact.Warning, fmt.Sprintf("image %s is private or missing, and the pod has no imagePullSecrets", c.Image)
					f.Evidence = []string{"registry: " + firstLine(res.Err), "project: no imagePullSecrets on the pod (the ServiceAccount's are not checked)"}
					f.Fix = "Add an imagePullSecret with registry credentials, or check the image name."
					f.Predicts = "ImagePullBackOff: pull access denied"
				default:
					continue
				}
				out = append(out, f)
			}
		}
		return out
	},
}

var platformRule = Rule{
	ID:    "k8s.platform",
	Needs: []fact.ID{fact.Nodes, fact.Registry},
	Check: func(_ context.Context, env *Env) []rules.Finding {
		var out []rules.Finding
		for _, w := range env.Manifests.Workloads {
			// Nodes the pod may land on: placement rules, plus the extended
			// resources (GPUs) it requests. CPU and memory are left to
			// k8s.schedulable because they depend on what else runs there.
			_, _, ext := w.Pod.Requests()
			var eligible []Node
			for _, n := range env.Cluster.Nodes {
				if len(placementReasons(w.Pod, n)) > 0 {
					continue
				}
				has := true
				for k, v := range ext {
					if n.Extended[k] < v {
						has = false
					}
				}
				if has {
					eligible = append(eligible, n)
				}
			}
			if len(eligible) == 0 {
				continue // k8s.schedulable reports it
			}
			for _, c := range allContainers(w.Pod) {
				res, ok := env.Images[c.Image]
				if !ok || res.Image == nil {
					continue
				}
				var bad, good []string
				for _, n := range eligible {
					if supports(res.Image, n.Platform) {
						good = append(good, n.Name)
					} else {
						bad = append(bad, n.Name+" ("+n.Platform+")")
					}
				}
				if len(bad) == 0 {
					continue
				}
				f := rules.Finding{
					Rule: "k8s.platform", Service: w.Kind + "/" + w.Name, Location: w.Location,
					Evidence: []string{
						"registry: " + c.Image + " ships " + strings.Join(res.Image.Platforms, ", "),
						"cluster: eligible nodes without a matching platform: " + strings.Join(bad, ", "),
					},
				}
				predicts := "exec format error / CrashLoopBackOff"
				if res.Image.Index {
					predicts = "ErrImagePull: no matching manifest for the node's platform"
				}
				f.Predicts = predicts
				if len(good) == 0 {
					f.Severity = fact.Error
					f.Title = fmt.Sprintf("image %s cannot run on any node this pod can be scheduled to", c.Image)
					f.Fix = "Build a multi-arch image (docker buildx build --platform …) or add nodes of a supported architecture."
				} else {
					f.Severity = fact.Warning
					f.Title = fmt.Sprintf("image %s fails on %d of %d eligible nodes (mixed architectures)", c.Image, len(bad), len(eligible))
					f.Fix = fmt.Sprintf("Pin the pod with nodeSelector kubernetes.io/arch, or publish a multi-arch image. Nodes that work: %s.", strings.Join(good, ", "))
				}
				out = append(out, f)
			}
		}
		return out
	},
}

var schedulableRule = Rule{
	ID:    "k8s.schedulable",
	Needs: []fact.ID{fact.Nodes},
	Check: func(_ context.Context, env *Env) []rules.Finding {
		var out []rules.Finding
		nodes := env.Cluster.Nodes
		for _, w := range env.Manifests.Workloads {
			if w.Kind == "DaemonSet" {
				continue // zero matching nodes means zero pods, not a failure
			}
			cpu, mem, ext := w.Pod.Requests()
			counts := map[string]int{}
			fits := 0
			for _, n := range nodes {
				reasons := placementReasons(w.Pod, n)
				if n.CPU > 0 && cpu > n.CPU {
					reasons = append(reasons, "Insufficient cpu")
				}
				if n.Memory > 0 && mem > n.Memory {
					reasons = append(reasons, "Insufficient memory")
				}
				for k, v := range ext {
					if v > n.Extended[k] {
						reasons = append(reasons, "Insufficient "+k)
					}
				}
				if len(reasons) == 0 {
					fits++
				}
				for _, r := range dedupe(reasons) {
					counts[r]++
				}
			}
			if fits > 0 || len(nodes) == 0 {
				continue
			}
			var parts []string
			for r, n := range counts {
				parts = append(parts, fmt.Sprintf("%d %s", n, r))
			}
			sort.Strings(parts)
			msg := fmt.Sprintf("0/%d nodes are available: %s.", len(nodes), strings.Join(parts, ", "))
			out = append(out, rules.Finding{
				Rule: "k8s.schedulable", Severity: fact.Error, Service: w.Kind + "/" + w.Name, Location: w.Location,
				Title: "no node can run this pod",
				Evidence: []string{
					fmt.Sprintf("project: requests cpu %s, memory %s%s", formatCPU(cpu), formatBytes(mem), extendedText(ext)),
					"cluster: " + msg,
					"capacity is compared with node allocatable; pods already running are not counted",
				},
				Fix:      "Relax the selector/affinity, add a toleration, lower the requests, or add a matching node.",
				Predicts: "FailedScheduling: " + msg,
			})
		}
		return out
	},
}

var storageRule = Rule{
	ID:    "k8s.storage",
	Needs: []fact.ID{fact.Storage},
	Check: func(_ context.Context, env *Env) []rules.Finding {
		c := env.Cluster
		known := map[string]bool{}
		for _, s := range c.StorageClasses {
			known[s] = true
		}
		var out []rules.Finding
		check := func(owner string, cl Claim) {
			f := rules.Finding{Rule: "k8s.storage", Severity: fact.Error, Service: owner, Location: cl.Location}
			switch {
			case cl.StorageClass == nil && c.DefaultClass == "":
				f.Title = fmt.Sprintf("claim %s sets no StorageClass and the cluster has no default", cl.Name)
				f.Evidence = []string{"cluster: StorageClasses " + listOrNone(c.StorageClasses) + ", none marked default"}
				f.Fix = "Set storageClassName, or mark a StorageClass as default."
				f.Predicts = "PersistentVolumeClaim Pending: no persistent volumes available for this claim and no storage class is set"
			case cl.StorageClass != nil && *cl.StorageClass != "" && !known[*cl.StorageClass]:
				f.Title = fmt.Sprintf("claim %s uses StorageClass %q, which does not exist", cl.Name, *cl.StorageClass)
				f.Evidence = []string{"cluster: StorageClasses " + listOrNone(c.StorageClasses)}
				f.Fix = "Use one of the cluster's StorageClasses, or install the provisioner."
				f.Predicts = fmt.Sprintf(`PersistentVolumeClaim Pending: storageclass.storage.k8s.io "%s" not found`, *cl.StorageClass)
			default:
				return
			}
			out = append(out, f)
		}
		for _, cl := range env.Manifests.Claims {
			check("PersistentVolumeClaim/"+cl.Name, cl)
		}
		for _, w := range env.Manifests.Workloads {
			for _, cl := range w.Claims {
				check(w.Kind+"/"+w.Name, cl)
			}
		}
		return out
	},
}

// placementReasons lists why the scheduler would skip node n for this pod,
// ignoring resources, in the scheduler's own wording.
func placementReasons(p PodSpec, n Node) []string {
	var r []string
	if !n.Ready {
		r = append(r, "node(s) were not ready")
	}
	if n.Unschedulable {
		r = append(r, "node(s) were unschedulable")
	}
	if !matchesSelector(p, n) {
		r = append(r, "node(s) didn't match Pod's node affinity/selector")
	}
	for _, t := range n.Taints {
		if (t.Effect == "NoSchedule" || t.Effect == "NoExecute") && !tolerates(p.Tolerations, t) {
			r = append(r, fmt.Sprintf("node(s) had untolerated taint {%s: %s}", t.Key, t.Value))
		}
	}
	return r
}

func matchesSelector(p PodSpec, n Node) bool {
	for k, v := range p.NodeSelector {
		if n.Labels[k] != v {
			return false
		}
	}
	if len(p.Required) == 0 {
		return true
	}
	for _, term := range p.Required { // terms are ORed
		ok := true
		for _, e := range term.Expressions { // expressions are ANDed
			v, has := n.Labels[e.Key]
			switch e.Operator {
			case "In":
				ok = ok && has && contains(e.Values, v)
			case "NotIn":
				ok = ok && !(has && contains(e.Values, v))
			case "Exists":
				ok = ok && has
			case "DoesNotExist":
				ok = ok && !has
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func tolerates(ts []Toleration, t Taint) bool {
	for _, tol := range ts {
		if tol.Effect != "" && tol.Effect != t.Effect {
			continue
		}
		switch {
		case tol.Operator == "Exists" && (tol.Key == "" || tol.Key == t.Key):
			return true
		case (tol.Operator == "" || tol.Operator == "Equal") && tol.Key == t.Key && tol.Value == t.Value:
			return true
		}
	}
	return false
}

func supports(img *registry.Image, platform string) bool {
	for _, p := range img.Platforms {
		if registry.Compatible(p, platform) {
			return true
		}
	}
	return false
}

func allContainers(p PodSpec) []Container {
	return append(append([]Container{}, p.InitContainers...), p.Containers...)
}

func extendedText(ext map[string]int64) string {
	var parts []string
	for k, v := range ext {
		parts = append(parts, fmt.Sprintf(", %s %d", k, v))
	}
	sort.Strings(parts)
	return strings.Join(parts, "")
}

func listOrNone(xs []string) string {
	if len(xs) == 0 {
		return "(none)"
	}
	return strings.Join(xs, ", ")
}

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func firstLine(err error) string {
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
