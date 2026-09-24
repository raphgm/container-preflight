package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	osexec "os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/raphgm/container-preflight/internal/executor"
	"github.com/raphgm/container-preflight/pkg/preflight/fact"
)

const snapshotVersion = 1

// Snapshot is a portable host profile. Preflight can run against a snapshot
// from another machine (a teammate's laptop, a CI runner, a server) to
// predict failures there without access to it.
type Snapshot struct {
	Version        int            `json:"version"`
	CapturedAt     time.Time      `json:"capturedAt"`
	Hostname       string         `json:"hostname"`
	Profile        *Profile       `json:"profile"`
	Failures       []fact.Failure `json:"failures,omitempty"`
	ListeningPorts []string       `json:"listeningPorts,omitempty"`
}

// Capture probes this host and records it as a snapshot.
func Capture(ctx context.Context, run executor.Runner) *Snapshot {
	facts := fact.NewSet()
	p := Probe(ctx, run, facts)
	name, _ := os.Hostname()
	s := &Snapshot{
		Version:    snapshotVersion,
		CapturedAt: time.Now().UTC(),
		Hostname:   name,
		Profile:    p,
		Failures:   facts.All(),
	}
	if p.Local {
		s.ListeningPorts = listeningPorts()
	}
	return s
}

// Restore turns a snapshot back into a profile and fact set. Port checks use
// the ports that were listening when the snapshot was taken.
func (s *Snapshot) Restore() (*Profile, *fact.Set) {
	facts := fact.NewSet()
	for _, f := range s.Failures {
		facts.Fail(f)
	}
	p := s.Profile
	if p.Sysctls == nil {
		p.Sysctls = map[string]int64{}
	}
	if p.LocalImages == nil {
		p.LocalImages = map[string]bool{}
	}
	if p.PublishedPorts == nil {
		p.PublishedPorts = map[string][]Container{}
	}
	if p.Local {
		listening := map[string]bool{}
		for _, lp := range s.ListeningPorts {
			listening[lp] = true
		}
		p.PortProbe = func(_ string, port int, proto string) bool {
			return listening[fmt.Sprintf("%d/%s", port, proto)]
		}
		p.Lsof = nil
	}
	return p, facts
}

func LoadSnapshot(path string) (*Snapshot, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if s.Profile == nil {
		return nil, fmt.Errorf("%s: not a container-preflight snapshot", path)
	}
	if s.Version > snapshotVersion {
		return nil, fmt.Errorf("%s: snapshot version %d is newer than this container-preflight supports", path, s.Version)
	}
	return &s, nil
}

// listeningPorts lists TCP ports with a listener on this machine.
func listeningPorts() []string {
	out, err := osexec.Command("lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-Fn").Output()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var ports []string
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "n") {
			continue
		}
		i := strings.LastIndex(line, ":")
		if i < 0 {
			continue
		}
		if _, err := strconv.Atoi(line[i+1:]); err != nil {
			continue
		}
		key := line[i+1:] + "/tcp"
		if !seen[key] {
			seen[key] = true
			ports = append(ports, key)
		}
	}
	return ports
}
