# Validation run: Kubernetes rules on minikube

Date: 2026-09-24
Cluster: minikube (Docker driver on Docker Desktop, Apple silicon), Kubernetes v1.37.0, one
`linux/arm64` node, default StorageClass `standard`.
Manifests: `examples/k8s/app.yaml`.

`container-preflight k8s examples/k8s` was run first. The manifests were then applied to a
throw-away namespace, and the events Kubernetes reported were compared with the predictions.

| Object | Prediction | Real outcome | Verdict |
|---|---|---|---|
| `Ingress/old-ingress` | `no matches for kind "Ingress" in version "extensions/v1beta1"` | `kubectl apply`: identical | ✅ exact |
| `Pod/typo` (`nginx:9.99.99`) | `ErrImagePull` | `ErrImagePull`, then `ImagePullBackOff` (`failed to resolve reference … not found`) | ✅ |
| `Deployment/huge` (64 GiB request) | `FailedScheduling: 0/1 nodes are available: 1 Insufficient memory.` | identical, followed by a preemption note | ✅ exact |
| `StatefulSet/db` (StorageClass `gp3`) | PVC Pending: `storageclass.storage.k8s.io "gp3" not found` | `ProvisioningFailed` with that exact text; PVC `Pending`; pod `FailedScheduling: … unbound immediate PersistentVolumeClaims` | ✅ exact |
| `Deployment/web` (`pause-amd64`, amd64 only) | first version: cannot run on any node → `exec format error` | pods `Running` | ❌ false positive |

The false positive has the same cause as the Colima case. minikube's node runs inside Docker
Desktop's VM, which runs amd64 binaries under Rosetta, so the node inherits emulation that the
cluster API does not report. Local clusters (minikube, kind, Docker Desktop, k3d, Rancher
Desktop, OrbStack, Colima) now get a warning: "will only run under emulation". Other clusters keep
the error, because production arm64 nodes normally have no emulation. The Ingress fix also names
the served version (`networking.k8s.io/v1`) instead of suggesting a CRD.

Result: 4 of 5 predictions confirmed, 3 word for word. After the fix the tool's output matches
what the cluster did.

## Second round: scheduling reasons, snapshots, root cause

| Pod | Predicted | Real `FailedScheduling` event |
|---|---|---|
| `wrong-pool` (`nodeSelector: pool=gpu`) | `0/1 nodes are available: 1 node(s) didn't match Pod's node affinity/selector.` | identical ✅ |
| `wants-gpu` (`nvidia.com/gpu: 1`) | `0/1 nodes are available: 1 Insufficient nvidia.com/gpu.` | identical ✅ |
| `arm-only-affinity` (required affinity `arch In [amd64]`) | `0/1 nodes are available: 1 node(s) didn't match Pod's node affinity/selector.` | identical ✅ |

- **Snapshot.** `--save-cluster` wrote an 8 KB profile. Checking against it with
  `--cluster`, and no cluster access, produced the same three predictions.
- **Unreachable cluster.** A context pointing at an address that does not answer was reported
  once, as "Kubernetes API server is not reachable (context ghost)", blocking `k8s.api`,
  `k8s.platform`, `k8s.schedulable` and `k8s.storage`. `k8s.image` still ran because it needs
  only the registry.

Combined result on minikube: 7 of 8 predictions confirmed word for word. The remaining one is
the emulation case, fixed above.
