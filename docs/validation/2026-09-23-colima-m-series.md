# Validation run: Apple silicon Mac, Colima

Date: 2026-09-23
Host: MacBook (arm64, 8 GiB), macOS, Colima 0.10 (vz driver, no Rosetta), VM Ubuntu 24.04.4, 2 CPUs, 1.9 GiB memory.
Tooling: Docker CLI 29.8.1, docker-compose 5.5.1 (standalone), no buildx plugin.
Project: `examples/broken-shop`.

Each preflight prediction was checked by running the real Docker command. Where the real error was
worded differently from the predicted text, the rule was corrected and the corrected text is shown.

## Predictions vs. reality

| # | Rule | Prediction | Real outcome | Verdict |
|---|------|-----------|--------------|---------|
| 1 | `registry.credentials` | `error getting credentials - err: exec: "docker-credential-desktop": executable file not found in $PATH` | identical | ✅ exact |
| 2 | `image.exists` | `redis:7.9.99-alpine` missing | `failed to resolve reference "docker.io/library/redis:7.9.99-alpine": … not found` | ✅ (wording corrected) |
| 3 | `build.buildkit` | `the --mount option requires BuildKit` | identical | ✅ exact |
| 4 | `build.context` | `package-lock.json` missing | `COPY failed: file not found in build context or excluded by .dockerignore: stat package-lock.json: file does not exist` | ✅ (legacy-builder wording added) |
| 5 | `compose.env` | `required variable DB_PASSWORD is missing a value` | identical | ✅ exact |
| 6 | `ports.conflict` | two services on 8080 | `Bind for 0.0.0.0:18080 failed: port is already allocated` | ✅ exact |
| 7 | `ports.conflict` | 2 replicas on one host port | `Bind for 0.0.0.0:18081 failed: port is already allocated` | ✅ exact |
| 8 | `mounts.bind` | missing `nginx.conf` becomes a directory | `not a directory: Are you trying to mount a directory onto a file (or vice-versa)?` | ✅ exact |
| 9 | `image.platform` | first version: mssql "no emulation → exec format error" | amd64 runs via QEMU (`uname -m` → `x86_64`) | ❌ false positive, fixed: kernel binfmt is now read inside the VM |
| 10 | `image.platform` + `resources.memory` | mssql needs ≥2000 MB, VM has 1.9 GiB | crashed earlier under QEMU: `Invalid mapping of address … in reserved address space` | ⚠️ memory finding true but masked; new QEMU-crash knowledge added, now predicted exactly |
| 11 | `kernel.sysctl` | first version: "passed" without data | VM `vm.max_map_count` = 1048576 | ⚠️ silent pass fixed: value now read from VM; unknown → warning |

## Root-cause diagnosis (before Colima was started)

The daemon failure was first reported generically. On this machine the actual cause was a Docker
context (`desktop-linux`) still pointing at the socket of an uninstalled Docker Desktop, with Colima
installed but stopped. Preflight now reports:

```
✗ Docker context "desktop-linux" still points to Docker Desktop, which is not installed
    /Users/…/.docker/run/docker.sock is left over from it; nothing is listening there
    fix: Start Colima: `colima start`, then `docker context use colima`
    blocks 3 check(s): image.platform, resources.memory, resources.gpu
```

Following the fix started a working daemon. The first `colima start` failed because the Mac's disk had
118 MiB free; that is a host condition preflight does not yet diagnose.

## Observations for the evaluation

- Docker stops at the first failure: pulling surfaced only the credential error, not the missing tag;
  the build surfaced only the missing `COPY` source, not `RUN --mount`. Preflight reported all of them
  in one 4-second run.
- Of 11 predictions, 8 identified the real problem (6 word-for-word, 2 with different wording), 1 was a false
  positive, and 2 were imprecise. All three were caused by facts inside the VM that the first version
  could not see; reading the VM kernel fixed them.

## Learning from failures (`learn`)

1. `mongo:7` forced to `linux/amd64` under QEMU was expected to crash for lack of AVX. It ran,
   because this QEMU emulates AVX. Preflight had only warned about emulation, which was the
   right severity.
2. `postgres:16-alpine` without `POSTGRES_PASSWORD`. Preflight predicted nothing, because no
   rule knew this failure.
   - `learn -- docker-compose up` extracted
     `Error: Database is uninitialized and superuser password is not specified.` and created
     `learned.postgres` with conditions `image=postgres, memory=<2GiB`.
   - After a password was added, `learn --success -- docker-compose up -d` observed the working
     run. It added the distinguishing condition `env.POSTGRES_PASSWORD=unset`.
   - Preflight then reported nothing when the password was set. It predicted the failure when
     the password was removed on another tag (`postgres:17`).
   - `memory=<2GiB` remains a spurious condition until a failure on a larger VM generalizes it
     away. This bias of single-example learning should be discussed in the paper.

## Same Mac on Docker Desktop (2026-09-24)

Host: Docker Desktop, engine 29.8.0, VM 3.8 GiB, Rosetta enabled, buildx 0.37.1, Compose 5.5.1.
The same project (`broken-shop`) got different predictions than it did on Colima, and each
difference was confirmed:

| Check | Colima prediction | Desktop prediction | Real on Desktop |
|---|---|---|---|
| credential helper | error | passes (helper installed) | pulls work |
| `RUN --mount` | error (no buildx) | passes | builds |
| mssql | error: crashes under QEMU | warning: Rosetta emulation | `SQL Server is now ready for client connections` ✅ |
| bind outside shared paths | n/a | error (shared paths read from the VM) | `mounts denied: The path /opt/homebrew/etc is not shared from the host` ✅ exact |
| missing COPY source | legacy wording | BuildKit wording | `failed to compute cache key: failed to calculate checksum of ref …: "/missing.txt": not found` (text corrected) |

A new failure was found during setup and is now diagnosed. Docker Desktop had been started from
its installer disk image. That copy kept the socket and hung every command, while the copy in
/Applications attached to it. Preflight first reported only "accepts connections but does not
answer". It now names the installer copy as the root cause and adds a `host.installer` warning.
Probe commands also time out after 20s, where before they hung.

## Host disk pressure makes Docker Desktop's filesystem read-only (2026-09-24)

This failure happened on the author's machine and was not staged. Free space on the Mac's home
volume fell from about 7 GB to 3.6 GB while Docker Desktop ran a minikube cluster. Docker
Desktop keeps its VM disk as a sparse file on that volume (`Docker.raw`), so the VM's disk can
only grow as far as the host allows. After that:

1. `minikube stop` failed: `GUEST_STOP_TIMEOUT: Unable to stop VM ... Maximum number of retries (60) exceeded`.
2. `docker stop` / `docker kill minikube` failed. `docker inspect` reported `status=dead pid=0`
   while `docker ps` still listed the container as `Up`.
3. `minikube delete` and `docker rm -f minikube` failed with
   `write /var/lib/desktop-containerd/daemon/io.containerd.metadata.v1.bolt/meta.db: read-only file system`.

A failed write inside the VM had made its filesystem read-only. Restarting Docker Desktop
remounted it, after which the container, image (1.8 GB) and volume (790 MB) could be removed.
Host free space rose from 5.4 GB to 8.4 GB while the VM was stopped.

**Relevance.** None of the three error messages mentions disk space. Each one points at a
symptom: the VM, the container, the metadata database. The `host.disk` rule targets the
precondition, a VM-based engine with less than 10 GB free on the host volume, and warns before
the chain starts. Preflight reported `only 6.7 GiB free on this machine; Docker's VM disk grows
here` on this machine shortly before the incident. The rule predicts the precondition, not this
specific chain, so it counts as supporting evidence rather than a confirmed prediction.
