package rules

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/raphgm/container-doctor/pkg/preflight/fact"
	"github.com/raphgm/container-doctor/pkg/preflight/project"
)

const mib = 1024 * 1024

// knownImage records hard host requirements of popular images that are not
// visible in their manifests. Each entry is tied to the error the image
// prints when the requirement is not met.
type knownImage struct {
	match       string
	minMemory   int64
	maxMapCount int64

	// qemuCrash is the error printed when the image runs under QEMU
	// user-mode emulation, which it does not support.
	qemuCrash string

	skip     func(env map[string]string) bool
	predicts string
}

var knownImages = []knownImage{
	{
		match:     "mssql/server",
		minMemory: 2000 * mib,
		qemuCrash: "sqlservr: Invalid mapping of address … in reserved address space below 0x400000000000",
		predicts:  "sqlservr: This program requires a machine with at least 2000 megabytes of memory.",
	},
	{
		match:       "elasticsearch",
		maxMapCount: 262144,
		skip:        singleNode,
		predicts:    "max virtual memory areas vm.max_map_count [65530] is too low, increase to at least [262144]",
	},
	{
		match:       "opensearch",
		maxMapCount: 262144,
		skip:        singleNode,
		predicts:    "max virtual memory areas vm.max_map_count [65530] is too low, increase to at least [262144]",
	},
	{
		match:       "sonarqube",
		maxMapCount: 262144,
		predicts:    "max virtual memory areas vm.max_map_count [65530] is too low, increase to at least [262144]",
	},
}

// singleNode: Elasticsearch skips its bootstrap checks in single-node mode.
func singleNode(env map[string]string) bool {
	return env["discovery.type"] == "single-node"
}

// imagesOf returns the service image plus the external base images its
// build uses.
func imagesOf(svc *project.Service) []string {
	var refs []string
	if svc.Image != "" {
		refs = append(refs, svc.Image)
	}
	if svc.Build != nil && svc.Build.Dockerfile != nil {
		for _, st := range svc.Build.Dockerfile.Stages {
			if st.BaseStage < 0 {
				refs = append(refs, st.Base)
			}
		}
	}
	return refs
}

func knownFor(ref string) []knownImage {
	var out []knownImage
	for _, k := range knownImages {
		if strings.Contains(ref, k.match) {
			out = append(out, k)
		}
	}
	return out
}

func known(svc *project.Service) []knownImage {
	var out []knownImage
	for _, ref := range imagesOf(svc) {
		for _, k := range knownImages {
			if strings.Contains(ref, k.match) && (k.skip == nil || !k.skip(svc.Environment)) {
				out = append(out, k)
			}
		}
	}
	return out
}

var memoryRule = rule{
	id:    "resources.memory",
	title: "Docker has enough memory for the services",
	needs: []fact.ID{fact.Daemon, fact.ComposeFile},
	check: func(_ context.Context, env *Env) []Finding {
		h := env.Host
		avail := h.MemTotal
		where := fmt.Sprintf("host: Docker daemon has %s of memory", humanBytes(avail))
		if h.IsDesktop() && h.ClientMem > 0 {
			where = fmt.Sprintf("host: Docker Desktop VM has %s (this machine has %s; containers only see the VM)", humanBytes(avail), humanBytes(h.ClientMem))
		}
		var out []Finding
		var total int64
		var parts []string
		for _, svc := range env.Project.Services {
			need := max(svc.MemReservation, svc.MemLimit)
			reason := "limit/reservation"
			for _, k := range known(svc) {
				if k.minMemory > avail {
					fix := "Give Docker more memory."
					if h.IsDesktop() {
						fix = "Raise Docker Desktop's memory (Settings → Resources) above " + humanBytes(k.minMemory) + "."
					}
					out = append(out, Finding{
						Rule:     "resources.memory",
						Severity: fact.Error,
						Service:  svc.Name,
						Title:    fmt.Sprintf("%s needs at least %s of memory", k.match, humanBytes(k.minMemory)),
						Evidence: []string{where},
						Fix:      fix,
						Location: svc.Location.String(),
						Predicts: k.predicts,
					})
				}
				if k.minMemory > need {
					need, reason = k.minMemory, "known minimum for "+k.match
				}
			}
			if svc.MemLimit > avail {
				out = append(out, Finding{
					Rule:     "resources.memory",
					Severity: fact.Warning,
					Service:  svc.Name,
					Title:    fmt.Sprintf("memory limit %s is larger than all memory Docker has", humanBytes(svc.MemLimit)),
					Evidence: []string{where},
					Fix:      "The limit cannot be reached; the kernel OOM-kills the container first. Lower the limit or give Docker more memory.",
					Location: svc.Location.String(),
					Predicts: "OOMKilled",
				})
			}
			if need > 0 {
				n := int64(max(svc.Replicas, 1))
				total += need * n
				parts = append(parts, fmt.Sprintf("%s %s×%d (%s)", svc.Name, humanBytes(need), n, reason))
			}
		}
		if total > avail {
			sort.Strings(parts)
			out = append(out, Finding{
				Rule:     "resources.memory",
				Severity: fact.Warning,
				Title:    fmt.Sprintf("services need %s together; Docker has %s", humanBytes(total), humanBytes(avail)),
				Evidence: append([]string{where}, "project: "+strings.Join(parts, ", ")),
				Fix:      "Expect OOM kills under load. Give Docker more memory or run fewer services/replicas.",
				Predicts: "OOMKilled / exit code 137",
			})
		}
		return out
	},
}

// A registry reports compressed layer sizes; unpacked images are typically
// 2–3× larger.
const unpackFactor = 2.5

// VM engines need room on the host to boot and to grow their disk image.
const (
	vmMinFree  = 2 << 30
	vmWarnFree = 10 << 30
)

var installerRule = rule{
	id:    "host.installer",
	title: "Docker Desktop runs from Applications",
	needs: []fact.ID{fact.Daemon},
	check: func(_ context.Context, env *Env) []Finding {
		if !env.Host.RunningFromInstaller {
			return nil
		}
		return []Finding{{
			Rule:     "host.installer",
			Severity: fact.Warning,
			Title:    "Docker Desktop is running from its installer disk image",
			Evidence: []string{"host: the Docker VM was started from /Volumes/…/Docker.app"},
			Fix:      "Quit Docker Desktop, drag it to Applications, eject the Docker disk and start it from Applications. A copy started from the disk can keep the socket and hang every command.",
		}}
	},
}

var hostDiskRule = rule{
	id:    "host.disk",
	title: "This machine has room for Docker's VM",
	needs: []fact.ID{fact.LocalDaemon},
	check: func(_ context.Context, env *Env) []Finding {
		h := env.Host
		if !h.UsesVM() || h.ClientDiskFree == 0 || h.ClientDiskFree >= vmWarnFree {
			return nil
		}
		f := Finding{
			Rule:     "host.disk",
			Severity: fact.Warning,
			Title:    fmt.Sprintf("only %s free on this machine; Docker's VM disk grows here", humanBytes(h.ClientDiskFree)),
			Evidence: []string{"host: " + humanBytes(h.ClientDiskFree) + " free on the home volume"},
			Fix:      "Free disk space (caches, old simulators, `docker system prune`). Keep at least 10 GiB free.",
		}
		if h.ClientDiskFree < vmMinFree {
			f.Severity = fact.Error
			f.Title = fmt.Sprintf("only %s free on this machine; Docker's VM cannot start or grow", humanBytes(h.ClientDiskFree))
			f.Predicts = "no space left on device / VM fails to start"
		}
		return []Finding{f}
	},
}

var diskRule = rule{
	id:    "resources.disk",
	title: "Enough disk for the images to pull",
	needs: []fact.ID{fact.LocalDaemon, fact.Registry, fact.ComposeFile},
	check: func(_ context.Context, env *Env) []Finding {
		h := env.Host
		if h.DiskFree == 0 {
			return nil
		}
		var compressed int64
		counted := map[string]bool{}
		var biggest []string
		for _, p := range Pulls(env.Project, h) {
			key := ImageKey(p.Ref, p.Platform)
			res := env.Images[key]
			if p.Local || res.Err != nil || res.Image == nil || counted[key] {
				continue
			}
			counted[key] = true
			compressed += res.Image.CompressedSize
			if res.Image.CompressedSize > 200*mib {
				biggest = append(biggest, fmt.Sprintf("%s %s", p.Ref, humanBytes(res.Image.CompressedSize)))
			}
		}
		need := int64(float64(compressed) * unpackFactor)
		free := h.DiskFree
		where := fmt.Sprintf("host: %s free", humanBytes(free))
		// A VM disk is a sparse file on this machine: it can only grow as
		// far as the host volume allows.
		if h.UsesVM() && h.ClientDiskFree > 0 && h.ClientDiskFree < free {
			free = h.ClientDiskFree
			where = fmt.Sprintf("host: VM disk has %s free, but this machine only %s", humanBytes(h.DiskFree), humanBytes(free))
		}
		if !h.DiskExact && free == h.DiskFree {
			where += " on this machine (Docker's VM disk may have less)"
		}
		evidence := []string{where, fmt.Sprintf("registry: %s to download, ≈%s unpacked", humanBytes(compressed), humanBytes(need))}
		if len(biggest) > 0 {
			evidence = append(evidence, "largest: "+strings.Join(biggest, ", "))
		}
		switch {
		case need > free:
			return []Finding{{
				Rule:     "resources.disk",
				Severity: fact.Error,
				Title:    "not enough disk space to pull the images",
				Evidence: evidence,
				Fix:      "Free space (`docker system prune`) or enlarge Docker's disk.",
				Predicts: "no space left on device",
			}}
		case h.DiskExact && need > free*9/10:
			return []Finding{{
				Rule:     "resources.disk",
				Severity: fact.Warning,
				Title:    "pulling the images will nearly fill the disk",
				Evidence: evidence,
				Fix:      "Free space with `docker system prune`.",
			}}
		}
		return nil
	},
}

var gpuRule = rule{
	id:    "resources.gpu",
	title: "GPU requests can be satisfied",
	needs: []fact.ID{fact.Daemon, fact.ComposeFile},
	check: func(_ context.Context, env *Env) []Finding {
		h := env.Host
		hasNvidia := false
		for _, r := range h.Runtimes {
			if strings.Contains(r, "nvidia") {
				hasNvidia = true
			}
		}
		var out []Finding
		for _, svc := range env.Project.Services {
			if !svc.GPU || hasNvidia {
				continue
			}
			evidence := []string{"host: runtimes " + strings.Join(h.Runtimes, ", ") + " (no nvidia)"}
			fix := "Install the NVIDIA Container Toolkit and restart Docker."
			if h.ClientOS == "darwin" {
				evidence = append(evidence, "host: Docker on macOS cannot pass GPUs to containers")
				fix = "Run this service on a Linux or Windows host with an NVIDIA GPU, or make the GPU optional."
			}
			out = append(out, Finding{
				Rule:     "resources.gpu",
				Severity: fact.Error,
				Service:  svc.Name,
				Title:    "service requests a GPU this host cannot provide",
				Evidence: evidence,
				Fix:      fix,
				Location: svc.Location.String(),
				Predicts: `could not select device driver "" with capabilities: [[gpu]]`,
			})
		}
		return out
	},
}

var sysctlRule = rule{
	id:    "kernel.sysctl",
	title: "Kernel settings meet image requirements",
	needs: []fact.ID{fact.LocalDaemon, fact.ComposeFile},
	check: func(_ context.Context, env *Env) []Finding {
		have, ok := env.Host.Sysctls["vm.max_map_count"]
		var out []Finding
		for _, svc := range env.Project.Services {
			for _, k := range known(svc) {
				if k.maxMapCount == 0 || (ok && have >= k.maxMapCount) {
					continue
				}
				if !ok {
					out = append(out, Finding{
						Rule:     "kernel.sysctl",
						Severity: fact.Warning,
						Service:  svc.Name,
						Title:    fmt.Sprintf("%s needs vm.max_map_count ≥ %d; could not read it on this host", k.match, k.maxMapCount),
						Evidence: []string{"host: the daemon's kernel could not be inspected"},
						Fix:      "Check with `sysctl vm.max_map_count` where the daemon runs.",
						Location: svc.Location.String(),
						Predicts: k.predicts,
					})
					continue
				}
				out = append(out, Finding{
					Rule:     "kernel.sysctl",
					Severity: fact.Error,
					Service:  svc.Name,
					Title:    fmt.Sprintf("%s needs vm.max_map_count ≥ %d", k.match, k.maxMapCount),
					Evidence: []string{fmt.Sprintf("host: vm.max_map_count = %d", have)},
					Fix:      fmt.Sprintf("sudo sysctl -w vm.max_map_count=%d (persist it in /etc/sysctl.conf), or set discovery.type=single-node for development.", k.maxMapCount),
					Location: svc.Location.String(),
					Predicts: k.predicts,
				})
			}
		}
		return out
	},
}
