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
