# Container Doctor 🩺

`container-doctor` predicts whether a container project will build and run on a machine **before you run it**, and explains each predicted failure once, at its root cause.

Linters look only at your Dockerfile. `docker info` looks only at your machine. Most "works on my machine" failures come from the mismatch between the two: an amd64-only image on an Apple silicon laptop, a Compose file that needs more memory than Docker Desktop's VM has, a `COPY` of a file your `.dockerignore` excludes, a port something else already holds. `container-doctor preflight` checks the project and the host together.

## Preflight

```bash
container-doctor preflight .
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
container-doctor snapshot -o ci-runner.json

# anywhere
container-doctor preflight . --host ci-runner.json
```

`examples/` contains a deliberately broken project and two example host snapshots. The same project gets different predictions on each:

```bash
container-doctor preflight examples/broken-shop --host examples/hosts/macbook-m2-desktop.json
container-doctor preflight examples/broken-shop --host examples/hosts/ubuntu-legacy-server.json
```

### Rules

| Rule | Predicts |
|---|---|
| `image.exists` | tag or repository missing from the registry |
| `image.platform` | no variant for the host's platform, emulation needed or unavailable (`exec format error`) |
| `registry.credentials` | configured credential helper not installed (`error getting credentials`) |
| `build.context` | `COPY`/`ADD` sources missing, excluded by `.dockerignore`, or outside the context; bad target or ARG |
| `build.buildkit` | `RUN --mount`, heredocs, `COPY --chmod/--link` without BuildKit |
| `compose.version` | Compose features newer than the installed Compose |
| `compose.env` | unset variables, required `${VAR:?}` without a value |
| `resources.memory` | known image minimums and total limits vs Docker's memory |
| `resources.disk` | image download size ×2.5 vs free disk |
| `resources.gpu` | GPU requests without the NVIDIA runtime, or on macOS |
| `kernel.sysctl` | `vm.max_map_count` for Elasticsearch/OpenSearch/SonarQube |
| `ports.conflict` | two services, or several replicas, on one host port |
| `ports.host` | host port already held by another process or container; privileged ports under rootless Docker |
| `mounts.bind` | missing bind sources that Docker will turn into directories, stale empty directories |
| `mounts.sharing` | paths outside Docker Desktop's shared folders |

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
go install github.com/raphgm/container-doctor@latest
```

### From Releases
Pre-compiled binaries for **macOS**, **Linux**, and **Windows** are automatically generated for every release. 
Head over to the [Releases page](https://github.com/raphgm/container-doctor/releases) to download the binary for your operating system.

---

## Usage

### Host health checks
Run the diagnostic engine to check your environment:
```bash
container-doctor check
```

You can export the results to different formats using the `--format` flag:
```bash
# JSON Output
container-doctor check --format json > report.json

# Markdown Output
container-doctor check --format markdown > report.md

# HTML Output (Standalone Webpage)
container-doctor check --format html > report.html
```

### Inspect a project
Statically analyze the configuration files in your current directory (or any specified path):
```bash
container-doctor inspect .
```

---

## Architecture & Extensibility

`container-doctor` is built with a highly decoupled, plugin-based architecture. 

- **Engine**: The core `internal/engine` runs the execution loop and aggregates results.
- **Providers**: Checks are grouped by Providers (e.g., `system`, `docker`, `compose`).
- **Renderers**: Custom output rendering is handled via the `Renderer` interface in `internal/renderer`.

Adding a new check is as simple as creating a struct that implements the `check.Check` interface and registering it with a Provider!

## License

This project is licensed under the MIT License.
