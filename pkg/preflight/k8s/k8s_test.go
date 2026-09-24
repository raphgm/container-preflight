package k8s

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/raphgm/container-preflight/pkg/preflight/fact"
	"github.com/raphgm/container-preflight/pkg/preflight/registry"
	"github.com/raphgm/container-preflight/pkg/preflight/rules"
)

type fakeKubectl map[string]string

func (f fakeKubectl) Run(_ context.Context, name string, args ...string) (string, error) {
	key := strings.Join(args, " ")
	for prefix, out := range f {
		if strings.HasPrefix(key, prefix) {
			if strings.HasPrefix(out, "ERR:") {
				return "", errors.New(strings.TrimPrefix(out, "ERR:"))
			}
			return out, nil
		}
	}
	return "", fmt.Errorf("unexpected: kubectl %s", key)
}

const nodesJSON = `{"items":[
 {"metadata":{"name":"amd-1","labels":{"kubernetes.io/arch":"amd64","pool":"general"}},
  "spec":{},
  "status":{"allocatable":{"cpu":"4","memory":"16Gi"},"nodeInfo":{"architecture":"amd64","operatingSystem":"linux"},"conditions":[{"type":"Ready","status":"True"}]}},
 {"metadata":{"name":"arm-1","labels":{"kubernetes.io/arch":"arm64","pool":"general"}},
  "spec":{},
  "status":{"allocatable":{"cpu":"2","memory":"8Gi"},"nodeInfo":{"architecture":"arm64","operatingSystem":"linux"},"conditions":[{"type":"Ready","status":"True"}]}},
 {"metadata":{"name":"gpu-1","labels":{"kubernetes.io/arch":"amd64","pool":"gpu"}},
  "spec":{"taints":[{"key":"nvidia.com/gpu","value":"present","effect":"NoSchedule"}]},
  "status":{"allocatable":{"cpu":"8","memory":"32Gi","nvidia.com/gpu":"1"},"nodeInfo":{"architecture":"amd64","operatingSystem":"linux"},"conditions":[{"type":"Ready","status":"True"}]}}
]}`

func liveCluster() fakeKubectl {
	return fakeKubectl{
		"version --client":       `{"clientVersion":{}}`,
		"config current-context": "prod-eks",
		"version -o json":        `{"serverVersion":{"gitVersion":"v1.31.0"}}`,
		"api-versions":           "v1\napps/v1\nbatch/v1\nnetworking.k8s.io/v1",
		"api-resources":          "pods po v1 true Pod\ndeployments deploy apps/v1 true Deployment\nstatefulsets sts apps/v1 true StatefulSet\njobs batch/v1 true Job\ningresses ing networking.k8s.io/v1 true Ingress\npersistentvolumeclaims pvc v1 true PersistentVolumeClaim\nservices svc v1 true Service",
		"get nodes":              nodesJSON,
		"get storageclasses":     `{"items":[{"metadata":{"name":"standard","annotations":{"storageclass.kubernetes.io/is-default-class":"true"}}}]}`,
	}
}

const manifests = `apiVersion: apps/v1
kind: Deployment
metadata: {name: web}
spec:
  replicas: 3
  template:
    spec:
      containers:
      - name: web
        image: acme/web-amd64:1
        resources: {requests: {cpu: 500m, memory: 256Mi}}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: pinned}
spec:
  template:
    spec:
      nodeSelector: {kubernetes.io/arch: arm64}
      containers:
      - {name: app, image: acme/web-amd64:1}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: trainer}
spec:
  template:
    spec:
      containers:
      - name: t
        image: pytorch/pytorch:2
        resources: {limits: {nvidia.com/gpu: 1}}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: huge}
spec:
  template:
    spec:
      containers:
      - {name: h, image: nginx:1.27, resources: {requests: {memory: 64Gi}}}
---
apiVersion: extensions/v1beta1
kind: Ingress
metadata: {name: old}
---
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata: {name: sm}
---
apiVersion: apps/v1
kind: StatefulSet
metadata: {name: db}
spec:
  template:
    spec:
      containers: [{name: db, image: postgres:16}]
  volumeClaimTemplates:
  - metadata: {name: data}
    spec: {storageClassName: gp3}
---
apiVersion: v1
kind: Pod
metadata: {name: typo}
spec:
  containers: [{name: x, image: nginx:9.99.99}]
`

type fakeRegistry map[string][]string

func (f fakeRegistry) Resolve(_ context.Context, ref, _ string) (*registry.Image, error) {
	ps, ok := f[ref]
	if !ok {
		return nil, fmt.Errorf("%w: %s", registry.ErrNotFound, ref)
	}
	return &registry.Image{Ref: ref, Index: len(ps) > 1, Platforms: ps}, nil
}

func setup(t *testing.T) *Env {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "all.yaml"), []byte(manifests), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Load([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	facts := fact.NewSet()
	c := Probe(context.Background(), liveCluster(), "", facts)
	if len(facts.Roots()) != 0 {
		t.Fatalf("probe failures: %+v", facts.Roots())
	}
	reg := fakeRegistry{
		"acme/web-amd64:1":  {"linux/amd64"},
		"pytorch/pytorch:2": {"linux/amd64"},
		"nginx:1.27":        {"linux/amd64", "linux/arm64"},
		"postgres:16":       {"linux/amd64", "linux/arm64"},
	}
	env := &Env{Manifests: m, Cluster: c, Facts: facts, Images: map[string]rules.ImageResult{}}
	for _, w := range m.Workloads {
		for _, ct := range allContainers(w.Pod) {
			img, err := reg.Resolve(context.Background(), ct.Image, "")
			env.Images[ct.Image] = rules.ImageResult{Image: img, Err: err}
		}
	}
	return env
}

func titles(fs []rules.Finding) string {
	var out []string
	for _, f := range fs {
		out = append(out, string(f.Severity)+" "+f.Service+": "+f.Title)
	}
	return strings.Join(out, "\n")
}

func expect(t *testing.T, fs []rules.Finding, sev fact.Severity, substr string) rules.Finding {
	t.Helper()
	for _, f := range fs {
		if f.Severity == sev && strings.Contains(f.Service+": "+f.Title, substr) {
			return f
		}
	}
	t.Errorf("want %s containing %q in:\n%s", sev, substr, titles(fs))
	return rules.Finding{}
}

func TestLoadManifests(t *testing.T) {
	env := setup(t)
	if len(env.Manifests.Objects) != 8 || len(env.Manifests.Workloads) != 6 {
		t.Fatalf("objects %d workloads %d", len(env.Manifests.Objects), len(env.Manifests.Workloads))
	}
	w := env.Manifests.Workloads[2]
	if _, _, ext := w.Pod.Requests(); ext["nvidia.com/gpu"] != 1 {
		t.Errorf("GPU limit should imply a request: %+v", ext)
	}
	if env.Cluster.DefaultClass != "standard" || len(env.Cluster.Nodes) != 3 {
		t.Errorf("cluster = %+v", env.Cluster)
	}
}

func TestAPIRule(t *testing.T) {
	fs := apiRule.Check(context.Background(), setup(t))
	f := expect(t, fs, fact.Error, "Ingress/old: extensions/v1beta1 Ingress is not available")
	if f.Fix != "Change apiVersion to networking.k8s.io/v1." {
		t.Errorf("fix %q", f.Fix)
	}
	if f.Predicts != `no matches for kind "Ingress" in version "extensions/v1beta1"` {
		t.Errorf("predicts %q", f.Predicts)
	}
	expect(t, fs, fact.Error, "ServiceMonitor/sm")
	if len(fs) != 2 {
		t.Errorf("only two unserved objects:\n%s", titles(fs))
	}
}

func TestImageRule(t *testing.T) {
	fs := imageRule.Check(context.Background(), setup(t))
	expect(t, fs, fact.Error, "Pod/typo: image nginx:9.99.99 does not exist")
	if len(fs) != 1 {
		t.Errorf("\n%s", titles(fs))
	}
}

func TestPlatformRule(t *testing.T) {
	fs := platformRule.Check(context.Background(), setup(t))
	f := expect(t, fs, fact.Warning, "Deployment/web: image acme/web-amd64:1 fails on 1 of 2 eligible nodes")
	if !strings.Contains(strings.Join(f.Evidence, " "), "arm-1 (linux/arm64)") {
		t.Errorf("evidence should name the arm node: %v", f.Evidence)
	}
	expect(t, fs, fact.Error, "Deployment/pinned: image acme/web-amd64:1 cannot run on any node")

	local := setup(t)
	local.Cluster.Context = "minikube"
	expect(t, platformRule.Check(context.Background(), local), fact.Warning, "Deployment/pinned: image acme/web-amd64:1 has no build for this cluster's nodes and will only run under emulation")
	if strings.Contains(titles(fs), "trainer") || strings.Contains(titles(fs), "huge") {
		t.Errorf("GPU pod is unschedulable (reported elsewhere); multi-arch nginx fits:\n%s", titles(fs))
	}
}

func TestSchedulableRule(t *testing.T) {
	fs := schedulableRule.Check(context.Background(), setup(t))
	gpu := expect(t, fs, fact.Error, "Deployment/trainer: no node can run this pod")
	want := "FailedScheduling: 0/3 nodes are available: 1 node(s) had untolerated taint {nvidia.com/gpu: present}, 2 Insufficient nvidia.com/gpu."
	if gpu.Predicts != want {
		t.Errorf("predicts\n %q\nwant\n %q", gpu.Predicts, want)
	}
	expect(t, fs, fact.Error, "Deployment/huge")
	if len(fs) != 2 {
		t.Errorf("\n%s", titles(fs))
	}
}

func TestStorageRule(t *testing.T) {
	env := setup(t)
	fs := storageRule.Check(context.Background(), env)
	f := expect(t, fs, fact.Error, `StatefulSet/db: claim data uses StorageClass "gp3", which does not exist`)
	if !strings.Contains(f.Predicts, `storageclass.storage.k8s.io "gp3" not found`) {
		t.Errorf("predicts %q", f.Predicts)
	}
	env.Cluster.DefaultClass = ""
	env.Manifests.Claims = append(env.Manifests.Claims, Claim{Name: "logs"})
	expect(t, storageRule.Check(context.Background(), env), fact.Error, "claim logs sets no StorageClass and the cluster has no default")
}

func TestUnreachableClusterIsOneRootCause(t *testing.T) {
	facts := fact.NewSet()
	Probe(context.Background(), fakeKubectl{
		"version --client":       `{}`,
		"config current-context": "prod",
		"version -o json":        "ERR:Unable to connect to the server: dial tcp 10.0.0.1:6443: i/o timeout",
	}, "", facts)
	roots := facts.Roots()
	if len(roots) != 1 || roots[0].Fact != fact.Cluster || !strings.Contains(roots[0].Summary, "context prod") {
		t.Fatalf("roots = %+v", roots)
	}
	for _, id := range []fact.ID{fact.Nodes, fact.ClusterAPIs, fact.Storage} {
		if r, _ := facts.Root(id); r.Fact != fact.Cluster {
			t.Errorf("%s should be blocked by the cluster", id)
		}
	}
}

func TestQuantity(t *testing.T) {
	cases := []struct {
		in   string
		cpu  bool
		want int64
	}{{"500m", true, 500}, {"2", true, 2000}, {"0.25", true, 250}, {"1Gi", false, 1 << 30}, {"128Mi", false, 128 << 20}, {"1G", false, 1e9}, {"1e3", false, 1000}}
	for _, c := range cases {
		if got, err := quantity(c.in, c.cpu); err != nil || got != c.want {
			t.Errorf("quantity(%q) = %d, %v; want %d", c.in, got, err, c.want)
		}
	}
}
