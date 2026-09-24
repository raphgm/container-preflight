// Package k8s predicts Kubernetes deployment failures before `kubectl apply`
// by joining what manifests require with what a specific cluster offers and
// what registries publish.
package k8s

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Object is any manifest document, kept for API checks.
type Object struct {
	APIVersion string
	Kind       string
	Name       string
	Namespace  string
	Location   string
}

// Workload is an object that creates pods.
type Workload struct {
	Object
	Replicas int
	Pod      PodSpec
	Claims   []Claim // volumeClaimTemplates
}

type PodSpec struct {
	NodeSelector     map[string]string
	Required         []NodeSelectorTerm // requiredDuringScheduling node affinity (ORed)
	Tolerations      []Toleration
	Containers       []Container // app containers
	InitContainers   []Container
	ImagePullSecrets []string
	ServiceAccount   string
}

type Container struct {
	Name     string
	Image    string
	CPU      int64 // requested millicores
	Memory   int64 // requested bytes
	GPU      int64 // nvidia.com/gpu
	Extended map[string]int64
}

type NodeSelectorTerm struct {
	Expressions []Requirement
}

type Requirement struct {
	Key      string
	Operator string
	Values   []string
}

type Toleration struct {
	Key, Operator, Value, Effect string
}

type Claim struct {
	Name         string
	StorageClass *string // nil: not set, "" explicitly empty
	Location     string
}

// Manifests is everything read from the given files.
type Manifests struct {
	Objects   []Object
	Workloads []Workload
	Claims    []Claim // standalone PersistentVolumeClaims
}

// Load reads YAML manifests from files, directories (recursively, *.yaml and
// *.yml) or "-" for stdin, such as `helm template` or `kustomize build`
// output.
func Load(paths []string) (*Manifests, error) {
	m := &Manifests{}
	for _, p := range paths {
		if p == "-" {
			b, err := io.ReadAll(os.Stdin)
			if err != nil {
				return nil, err
			}
			if err := m.parse("stdin", b); err != nil {
				return nil, err
			}
			continue
		}
		st, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		var files []string
		if st.IsDir() {
			err = filepath.WalkDir(p, func(f string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() && (strings.HasSuffix(f, ".yaml") || strings.HasSuffix(f, ".yml")) {
					files = append(files, f)
				}
				return err
			})
			if err != nil {
				return nil, err
			}
			sort.Strings(files)
		} else {
			files = []string{p}
		}
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				return nil, err
			}
			if err := m.parse(f, b); err != nil {
				return nil, err
			}
		}
	}
	if len(m.Objects) == 0 {
		return nil, errors.New("no Kubernetes objects found in the given manifests")
	}
	return m, nil
}

func (m *Manifests) parse(file string, b []byte) error {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	for {
		var n yaml.Node
		err := dec.Decode(&n)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		if len(n.Content) == 0 {
			continue
		}
		var doc map[string]any
		if err := n.Decode(&doc); err != nil || doc == nil {
			continue
		}
		m.add(doc, fmt.Sprintf("%s:%d", filepath.Base(file), n.Content[0].Line))
	}
}

func (m *Manifests) add(doc map[string]any, loc string) {
	kind := str(doc["kind"])
	if kind == "" {
		return
	}
	if strings.HasSuffix(kind, "List") {
		for _, it := range list(doc["items"]) {
			if d, ok := it.(map[string]any); ok {
				m.add(d, loc)
			}
		}
		return
	}
	meta := mp(doc["metadata"])
	obj := Object{APIVersion: str(doc["apiVersion"]), Kind: kind, Name: str(meta["name"]), Namespace: str(meta["namespace"]), Location: loc}
	m.Objects = append(m.Objects, obj)

	spec := mp(doc["spec"])
	var tmpl map[string]any
	replicas := 1
	switch kind {
	case "Pod":
		tmpl = map[string]any{"spec": spec}
	case "Deployment", "ReplicaSet", "StatefulSet", "DaemonSet", "ReplicationController":
		tmpl = mp(spec["template"])
		if r, ok := spec["replicas"].(int); ok {
			replicas = r
		}
	case "Job":
		tmpl = mp(spec["template"])
	case "CronJob":
		tmpl = mp(mp(mp(spec["jobTemplate"])["spec"])["template"])
	case "PersistentVolumeClaim":
		m.Claims = append(m.Claims, claim(obj.Name, spec, loc))
		return
	default:
		return
	}
	w := Workload{Object: obj, Replicas: replicas, Pod: podSpec(mp(tmpl["spec"]))}
	for _, c := range list(spec["volumeClaimTemplates"]) {
		cm := mp(c)
		w.Claims = append(w.Claims, claim(str(mp(cm["metadata"])["name"]), mp(cm["spec"]), loc))
	}
	m.Workloads = append(m.Workloads, w)
}

func claim(name string, spec map[string]any, loc string) Claim {
	c := Claim{Name: name, Location: loc}
	if v, ok := spec["storageClassName"]; ok {
		s := str(v)
		c.StorageClass = &s
	}
	return c
}

func podSpec(s map[string]any) PodSpec {
	p := PodSpec{NodeSelector: map[string]string{}, ServiceAccount: str(s["serviceAccountName"])}
	for k, v := range mp(s["nodeSelector"]) {
		p.NodeSelector[k] = str(v)
	}
	req := mp(mp(mp(s["affinity"])["nodeAffinity"])["requiredDuringSchedulingIgnoredDuringExecution"])
	for _, t := range list(req["nodeSelectorTerms"]) {
		var term NodeSelectorTerm
		for _, e := range list(mp(t)["matchExpressions"]) {
			em := mp(e)
			r := Requirement{Key: str(em["key"]), Operator: str(em["operator"])}
			for _, v := range list(em["values"]) {
				r.Values = append(r.Values, str(v))
			}
			term.Expressions = append(term.Expressions, r)
		}
		p.Required = append(p.Required, term)
	}
	for _, t := range list(s["tolerations"]) {
		tm := mp(t)
		p.Tolerations = append(p.Tolerations, Toleration{Key: str(tm["key"]), Operator: str(tm["operator"]), Value: str(tm["value"]), Effect: str(tm["effect"])})
	}
	for _, sec := range list(s["imagePullSecrets"]) {
		p.ImagePullSecrets = append(p.ImagePullSecrets, str(mp(sec)["name"]))
	}
	p.Containers = containers(list(s["containers"]))
	p.InitContainers = containers(list(s["initContainers"]))
	return p
}

func containers(items []any) []Container {
	var out []Container
	for _, c := range items {
		cm := mp(c)
		ct := Container{Name: str(cm["name"]), Image: str(cm["image"]), Extended: map[string]int64{}}
		res := mp(cm["resources"])
		req, lim := mp(res["requests"]), mp(res["limits"])
		// A limit without a request implies an equal request.
		get := func(k string) string {
			if v, ok := req[k]; ok {
				return str(v)
			}
			return str(lim[k])
		}
		ct.CPU, _ = quantity(get("cpu"), true)
		ct.Memory, _ = quantity(get("memory"), false)
		for _, src := range []map[string]any{lim, req} {
			for k, v := range src {
				if strings.Contains(k, "/") {
					n, _ := quantity(str(v), false)
					ct.Extended[k] = n
				}
			}
		}
		ct.GPU = ct.Extended["nvidia.com/gpu"]
		out = append(out, ct)
	}
	return out
}

// Requests are what the scheduler reserves: the larger of the sum of app
// containers and the largest init container.
func (p PodSpec) Requests() (cpu, mem int64, extended map[string]int64) {
	extended = map[string]int64{}
	for _, c := range p.Containers {
		cpu += c.CPU
		mem += c.Memory
		for k, v := range c.Extended {
			extended[k] += v
		}
	}
	for _, c := range p.InitContainers {
		cpu = max(cpu, c.CPU)
		mem = max(mem, c.Memory)
		for k, v := range c.Extended {
			extended[k] = max(extended[k], v)
		}
	}
	return cpu, mem, extended
}

func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}

func mp(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func list(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}
