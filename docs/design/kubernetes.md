# Preflight for Kubernetes: design sketch

Status: proposal. Nothing here is implemented.

## Idea

Apply the same three-view join to Kubernetes. Predict whether a set of manifests (plain YAML,
Kustomize output or a rendered Helm chart) will schedule and start on a **specific cluster**,
before `kubectl apply`:

| View | Docker today | Kubernetes |
|---|---|---|
| Requirements | Compose, Dockerfiles, `docker run` | Pods (from Deployments, StatefulSets, Jobs…), requests/limits, node selectors, affinity, tolerations, PVCs, Services, Ingress, ServiceAccounts |
| Capabilities | daemon, VM, ports, kernel | nodes (arch, OS, allocatable, taints, labels), quotas and LimitRanges, StorageClasses, IngressClasses, CRDs, admission policies, API versions |
| Registry | platforms, size, user | same, per container image, including image pull secrets |

The cluster profile comes from read-only API calls (`get`/`list` only) or from a saved snapshot,
so the check can run from CI against a cluster it cannot reach, just as `snapshot` works for
Docker hosts.

## Candidate rules

Each rule predicts a concrete event or status that Kubernetes would report.

| Rule | Predicts |
|---|---|
| `k8s.platform` | image has no variant for any schedulable node's arch → `exec format error`, `CrashLoopBackOff`, or no node can run it |
| `k8s.schedulable` | no node satisfies selectors, affinity, tolerations and requests together → `FailedScheduling: 0/N nodes are available` (with the per-reason breakdown the scheduler would give) |
| `k8s.capacity` | requests exceed allocatable, or sum exceeds free capacity → `Insufficient cpu/memory` |
| `k8s.quota` | requests exceed ResourceQuota or violate LimitRange → `exceeded quota` / `forbidden` |
| `k8s.storage` | PVC names a StorageClass that doesn't exist, access mode the class can't provide, or zone-bound volume vs node zones → PVC `Pending` |
| `k8s.api` | manifest uses an API version the cluster doesn't serve, or a CRD that isn't installed → `no matches for kind` |
| `k8s.image` | tag missing, private image without an `imagePullSecret` → `ErrImagePull` / `ImagePullBackOff` |
| `k8s.gpu` | `nvidia.com/gpu` requested but no node advertises it → `FailedScheduling … Insufficient nvidia.com/gpu` |
| `k8s.security` | Pod Security Admission level rejects `runAsRoot`, `hostPath`, privileged, etc. → `violates PodSecurity` |
| `k8s.memory-floor` | image with a known memory floor (SQL Server, Elasticsearch) under a limit below it → `OOMKilled` |
| `k8s.ports` | Service NodePort already used, `hostPort` conflicts on every eligible node |

## What carries over

- **Root-cause grouping.** Unreachable cluster, missing RBAC to list nodes, or an unknown CRD
  becomes one root cause that blocks the rules that depend on it. It does not show up as a
  dozen failures.
- **Learned rules.** `learn -- kubectl rollout status …` captures the failing event, for example
  a `FailedScheduling` message or container exit reason, and records it with the pod's features.
  Generalization and specialization work unchanged.
- **Registry metadata.** Reused as-is.

## Related tools to position against

- Manifest validators and linters (kubeconform, kube-score, Polaris) check manifests mostly in
  isolation from a specific cluster.
- Replicated's troubleshoot.sh ships *preflight* checks that an application author writes by
  hand for their own app. This proposal derives checks from the manifests themselves. The
  name overlap needs a decision before any public release.
- openshift-preflight certifies operators and images for OpenShift. That is certification,
  not deployment-failure prediction.

## Evaluation plan

Reuse the mining pipeline with Kubernetes error texts: `FailedScheduling`, `ImagePullBackOff`,
`no matches for kind`, `exceeded quota`, `violates PodSecurity`, `OOMKilled`, PVC `Pending`.
Stratify by category, freeze the rules before drawing a held-out sample, and validate in the
field on kind or minikube plus one managed cluster with mixed architectures (for example EKS
with Graviton and x86 node groups).
