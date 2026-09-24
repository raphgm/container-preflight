# Container Preflight ✈️

[![DOI](https://zenodo.org/badge/DOI/10.5281/zenodo.22941382.svg)](https://doi.org/10.5281/zenodo.22941382)

**Predict container failures before you run, and diagnose them at the root cause when they happen.**

`container-preflight` predicts whether a container project will build and run on a machine **before you run it**, and explains each predicted failure once, at its root cause.

Linters look only at your Dockerfile. `docker info` looks only at your machine. Most "works on my machine" failures come from the mismatch between the two: an amd64-only image on an Apple silicon laptop, a Compose file that needs more memory than Docker Desktop's VM has, a `COPY` of a file your `.dockerignore` excludes, a port something else already holds. `container-preflight check` checks the project and the host together.

## Check a project

```bash
container-preflight check .
```

Preflight builds three views and joins them:

1. **What the project needs.** The Compose file and every Dockerfile are parsed with Docker's own parsers (`compose-go`, BuildKit's Dockerfile frontend). This covers platforms, memory limits and replicas, published ports, bind mounts, `COPY` sources (only in stages BuildKit will actually build), BuildKit-only syntax, Compose features and interpolated variables.
2. **What the host offers.** The daemon's platform and memory (on macOS and Windows these are the VM's, not the laptop's), emulation, Compose/buildx versions, kernel settings, local images, credential helpers and live port usage.
3. **What registries publish.** Each image's platforms and download size, read from the registry without pulling.

Each finding quotes its evidence from both sides and the error Docker would print:

```
✗ [web] COPY source ".env.production" is excluded by .dockerignore  Dockerfile:11
    web/.dockerignore: pattern ".env*"
    would fail with: "failed to compute cache key: \"/.env.production\": not found"
    fix: Remove or negate the pattern ".env*" in web/.dockerignore.
```

### Root causes, not symptom lists

Checks depend on facts: the Docker CLI → the daemon → its platform and memory, and so on. When a fact cannot be established, every check that needs it is grouped under that single root cause instead of failing separately. Checks that do not depend on it keep running.

```
ROOT CAUSES
  ✗ Docker daemon is not reachable
      fix: Start Docker Desktop (or `colima start`, `sudo systemctl start docker`) …
      blocks 5 check(s): image.platform, resources.memory, resources.gpu, …
```

### Predict for another machine

```bash
# on the target machine (a teammate's laptop, a CI runner, a server)
container-preflight snapshot -o ci-runner.json

# anywhere
container-preflight check . --host ci-runner.json
```

`examples/` contains a deliberately broken project and two example host snapshots. The same project gets different predictions on each:

```bash
container-preflight check examples/broken-shop --host examples/hosts/macbook-m2-desktop.json
container-preflight check examples/broken-shop --host examples/hosts/ubuntu-legacy-server.json
```

### Learning from real failures

Built-in rules cover common failures. `learn` turns the failures your project actually hits into new rules:

```bash
container-preflight learn -- docker compose up db
```

It runs the command and extracts the decisive error line from the output. It turns that line into a signature, with addresses, IDs, paths and numbers generalized. It then records the conditions at the time: image, platform, emulation (QEMU/Rosetta), memory, builder, and which environment variables were set (never their values). The rule goes to `.container-preflight/learned.yaml`; commit it, and preflight predicts the failure for everyone on the team.

Rules improve with evidence:

- **Another failure with the same error** drops the conditions the two runs disagree on, which makes the rule more general.
- **A working run** (`learn --success -- docker compose up -d`) that satisfies a rule adds the condition that tells it apart from the failures, which makes the rule more specific. If no such condition exists, the rule is marked unreliable and no longer reported.
- **A failure a built-in rule already predicted** is reported as such and is not learned twice.

Example from a real run: Postgres started without a password failed. `learn` created a rule for `image=postgres`. After a password was added, the successful run refined the rule to `env.POSTGRES_PASSWORD=unset`, and preflight now flags any Compose file that uses Postgres without one.

### Rules

| Rule | Predicts |
|---|---|
| `image.exists` | tag or repository missing from the registry |
| `image.platform` | no variant for the host's platform, emulation needed or unavailable (`exec format error`) |
| `registry.credentials` | configured credential helper not installed (`error getting credentials`) |
| `build.context` | `COPY`/`ADD` sources missing, excluded by `.dockerignore`, or outside the context; bad target or ARG |
| `build.buildkit` | `RUN --mount`, heredocs, `COPY --chmod/--link` without BuildKit |
| `compose.version` | Compose features newer than the installed Compose (`include`, `name`, `develop`, hooks, `gpus`, `models`) |
| `compose.env` | unset variables, required `${VAR:?}` without a value |
| `resources.memory` | known image minimums and total limits vs Docker's memory |
| `resources.disk` | image download size ×2.5 vs free disk (the smaller of the VM disk and the host volume it lives on) |
| `host.disk` | too little space on this machine for Docker Desktop/Colima/OrbStack to start or grow their VM |
| `resources.gpu` | GPU requests without the NVIDIA runtime, or on macOS |
| `kernel.sysctl` | `vm.max_map_count` for Elasticsearch/OpenSearch/SonarQube |
| `ports.conflict` | two services, or several replicas, on one host port |
| `ports.host` | host port already held by another process or container; privileged ports under rootless Docker |
| `mounts.bind` | missing bind sources that Docker will turn into directories, stale empty directories |
| `mounts.permissions` | non-root containers writing to bind mounts Docker creates as root or that another uid owns; SELinux without `:z`; Postgres data folders bind-mounted from Windows |
| `mounts.sharing` | paths outside Docker Desktop's shared folders (read from the running VM on macOS; home only on Linux) |

Containers started without Compose are checked the same way:

```bash
container-preflight check --run "docker run -p 8080:80 -v ./site:/usr/share/nginx/html nginx:1.27"
```

Use `--format json` for machine-readable output and `--offline` to skip registry lookups. The exit code is 1 when a blocking problem is predicted.

## Other commands

- **System Diagnostics**: Checks your operating system, CPU architecture, memory (RAM), and disk space to ensure you meet minimum requirements.
- **Docker Health**: Validates that Docker is installed, the daemon is running, the socket is accessible, and client/server versions are compatible.
- **Docker Compose & Buildx**: Ensures crucial container plugins are installed and ready.
- **Static Inspection**: The `inspect` command statically analyzes your `Dockerfile` and `docker-compose.yml` for common anti-patterns (like using the `:latest` tag in production).
- **Multiple Output Formats**: View reports natively in your terminal, or export them to JSON, Markdown, or HTML.

---

## Installation

### Using Go
If you have Go installed, you can install the CLI directly:
```bash
go install github.com/raphgm/container-preflight@latest
```

### From Releases
Pre-compiled binaries for **macOS**, **Linux**, and **Windows** are automatically generated for every release. 
Head over to the [Releases page](https://github.com/raphgm/container-preflight/releases) to download the binary for your operating system.

---

## Usage

### Host health checks
Run the diagnostic engine to check your environment:
```bash
container-preflight host
```

You can export the results to different formats using the `--format` flag:
```bash
# JSON Output
container-preflight host --format json > report.json

# Markdown Output
container-preflight host --format markdown > report.md

# HTML Output (Standalone Webpage)
container-preflight host --format html > report.html
```

### Inspect a project
Statically analyze the configuration files in your current directory (or any specified path):
```bash
container-preflight inspect .
```

---

## Architecture & Extensibility

`container-preflight` is built with a highly decoupled, plugin-based architecture. 

- **Engine**: The core `internal/engine` runs the execution loop and aggregates results.
- **Providers**: Checks are grouped by Providers (e.g., `system`, `docker`, `compose`).
- **Renderers**: Custom output rendering is handled via the `Renderer` interface in `internal/renderer`.

Adding a new check is as simple as creating a struct that implements the `check.Check` interface and registering it with a Provider!

## License

This project is licensed under the MIT License.
