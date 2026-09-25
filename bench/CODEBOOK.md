# Labeling codebook

Version 2. It replaces the short guide used in the first rating round, which gave Cohen's κ = 0.02
for scope. Both raters apply this codebook independently, and discuss disagreements only after
both have finished.

For each issue answer **two questions**, in order. Judge only the **original reporter's
failure**, not other problems raised in the comments. Use the excerpt and the GitHub thread; do
not run anything.

## Question 1: Is it in scope?

Work through the steps. The first step that says OUT ends the question.

1. **Is it a failure report?** Someone tried to build, pull, create, start or run a container
   and it did not work, and the issue says what went wrong.
   - Feature requests, documentation requests, questions, design discussions and release
     notes → **OUT**, even if they mention an error message in passing.
     *Example: "Support ARM64", which asks maintainers to publish an arm64 image.*
   - A report whose failure was never described (for example only "it doesn't work") → **OUT**.

2. **Does it use Docker?** That means Docker Engine, Docker Desktop, Colima, OrbStack or
   Rancher Desktop (dockerd), driven by the `docker` CLI, Docker Compose or BuildKit, on one
   machine.
   - Kubernetes (including kind or minikube workloads), Podman, Swarm `stack deploy`, cloud
     builders (ACR, Cloud Build), Singularity or Enroot → **OUT**.
   - A tool that runs Docker for you (Lando, devcontainers, Home Assistant add-ons, CI jobs
     calling `docker`) → **IN**. Question 2 decides whether preflight could have seen it.

3. **Is the failure about the container setup rather than the application's own code?** The
   *container setup* is the Dockerfile, the Compose file or `docker run` command, images,
   registries, and the host: architecture, memory, disk, ports, files, kernel, and the engine.
   - Caused by the setup → **IN**. This includes failures that *surface* as an application or
     database error but whose cause is the setup: a missing environment variable, a missing
     password, a low `vm.max_map_count`, an image built for the wrong architecture, too little
     memory, a missing mounted file.
     *Example: Postgres exits with "superuser password is not specified". The cause is a
     variable missing from the Compose file, so it is IN.*
   - A bug in the application itself, whatever the setup (an exception in the app's logic, a
     wrong query, a dependency version clash inside the app, data loss after an upgrade) →
     **OUT**.
   - Runtime networking or health problems of the containers (DNS inside the container,
     service names not resolving, health checks failing) → **IN**. They are Docker setup
     failures; Question 2 will usually answer "no".

## Question 2: Would preflight have predicted it?

Only for issues that are IN. Imagine running `container-preflight` **before** the failing
command, on the reporter's project files and machine as the issue describes them, with the rules
listed below and nothing else.

- **Yes:** one listed rule would report **this exact cause** as an error, or as a warning when
  the real outcome was only a warning.
- **Partial:** a listed rule addresses the cause, but one of these holds:
  - it would only **warn**, while the real outcome was a failure;
  - it applies to **another OS or engine** than the reporter's;
  - it would fire only **after `learn`** had recorded the same failure once;
  - it depends on information preflight **cannot see**, such as a command typed in a CI
    script that is not in the repository.
- **No:** no listed rule addresses the cause, or the issue does not reveal the cause at all.

**When unsure,** choose between yes and partial → *partial*; between partial and no → *no*.
Plausibility is not enough: a check that preflight *could* have does not count unless it is in
the list.

## Rules (frozen at tag `eval-frozen`)

| Rule | What it catches before running |
|---|---|
| `image.exists` | image tag or repository missing, private or renamed |
| `image.platform` | image has no build for the host's CPU architecture; no emulation; known crashes under QEMU. Warns when the image will run under emulation |
| `registry.credentials` | configured credential helper not installed |
| `build.context` | `COPY`/`ADD` source missing, excluded by `.dockerignore`, or outside the build context; build target or `ARG` problems |
| `build.buildkit` | BuildKit-only Dockerfile syntax with the legacy builder |
| `compose.version` | Compose file features newer than the installed Compose |
| `compose.env` | unset variables, required `${VAR:?}` without a value |
| `resources.memory` | known image memory minimums; total limits vs Docker's memory |
| `resources.disk`, `host.disk` | images too large for free disk; host too full for Docker's VM |
| `resources.gpu` | GPU requested without the NVIDIA runtime, or on macOS |
| `kernel.sysctl` | `vm.max_map_count` too low for Elasticsearch, OpenSearch, SonarQube |
| `ports.conflict`, `ports.host` | two services or replicas on one host port; port held by another process or container; privileged ports under rootless Docker |
| `mounts.bind` | bind-mount source missing (Docker makes a directory where a file was expected) |
| `mounts.permissions` | non-root container writing to a root-owned or foreign-owned bind mount; SELinux without `:z`; Postgres data folder on Windows |
| `mounts.sharing` | path outside Docker Desktop's shared folders |
| root causes | Docker not installed or not running, stale context, invalid Compose file |

Input can be a Compose project, a Dockerfile, or a `docker run`/`create`/`pull` command.

## Worked examples (from the development sample)

| Issue | Q1 | Q2 | Why |
|---|---|---|---|
| Postgres container exits: "superuser password is not specified" | IN | partial | Setup cause (missing variable). No built-in rule knows Postgres needs it; `learn` would catch it after one failure |
| `COPY failed: file not found in build context` | IN | yes | `build.context` |
| "Please publish an arm64 image" | OUT | – | Feature request |
| Elasticsearch exits: `max virtual memory areas vm.max_map_count [65530] is too low` | IN | yes | `kernel.sysctl` |
| Container reports "unhealthy", dependency failed to start | IN | no | Health checks are not a rule |
| App throws `NullPointerException` after start | OUT | – | Application bug |
| `could not select device driver "" with capabilities: [[gpu]]` on Linux without the NVIDIA toolkit | IN | yes | `resources.gpu` |
| Pod stuck in `ImagePullBackOff` on a Kubernetes cluster | OUT | – | Kubernetes |
