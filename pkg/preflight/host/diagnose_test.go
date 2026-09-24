package host

import (
	"strings"
	"testing"
)

var colima = engine{"Colima", "/h/.colima/default/docker.sock", "colima start", "colima"}

func TestDiagnoseStaleDesktopContext(t *testing.T) {
	f := diagnoseDaemon(daemonClues{
		Context:   "desktop-linux",
		Endpoint:  "unix:///nonexistent/.docker/run/docker.sock",
		Err:       "Cannot connect to the Docker daemon",
		Installed: []engine{colima},
	})
	if f.Summary != `Docker context "desktop-linux" still points to Docker Desktop, which is not installed` {
		t.Errorf("summary = %q", f.Summary)
	}
	if f.Fix != "Start Colima: `colima start`, then `docker context use colima`" {
		t.Errorf("fix = %q", f.Fix)
	}
}

func TestDiagnoseRunningEngineUnderOtherContext(t *testing.T) {
	f := diagnoseDaemon(daemonClues{
		Context:   "desktop-linux",
		Endpoint:  "unix:///nonexistent/.docker/run/docker.sock",
		Installed: []engine{colima},
		Running:   []engine{colima},
	})
	if !strings.Contains(f.Summary, "Colima is running") || f.Fix != "`docker context use colima`" {
		t.Errorf("got %q / %q", f.Summary, f.Fix)
	}
}

func TestDiagnosePermission(t *testing.T) {
	f := diagnoseDaemon(daemonClues{Endpoint: "unix:///var/run/docker.sock", Err: "dial unix /var/run/docker.sock: connect: permission denied"})
	if !strings.Contains(f.Fix, "usermod -aG docker") {
		t.Errorf("fix = %q", f.Fix)
	}
}

func TestDiagnoseNothingInstalled(t *testing.T) {
	f := diagnoseDaemon(daemonClues{Endpoint: "unix:///nonexistent.sock"})
	if !strings.Contains(f.Fix, "Install a Docker engine") {
		t.Errorf("fix = %q", f.Fix)
	}
}

func TestDiagnoseInstalledButStopped(t *testing.T) {
	f := diagnoseDaemon(daemonClues{
		Context:   "colima",
		Endpoint:  "unix:///nonexistent/.colima/default/docker.sock",
		Installed: []engine{colima},
	})
	if f.Summary != "Colima is installed but not running" && !strings.Contains(f.Summary, "does not exist") {
		t.Errorf("summary = %q", f.Summary)
	}
	if f.Fix != "Start Colima: `colima start`" {
		t.Errorf("fix = %q", f.Fix)
	}
}

func TestDiagnoseFullDiskBlocksVMStart(t *testing.T) {
	f := diagnoseDaemon(daemonClues{
		Context:   "desktop-linux",
		Endpoint:  "unix:///nonexistent/.docker/run/docker.sock",
		Installed: []engine{colima},
		DiskFree:  118 << 20,
	})
	if !strings.HasPrefix(f.Fix, "Free disk space first: only 118 MiB free") || !strings.Contains(f.Fix, "colima start") {
		t.Errorf("fix = %q", f.Fix)
	}
}

func TestDiagnoseHungDaemonWithTwoVMs(t *testing.T) {
	desktop := engine{"Docker Desktop", "/h/.docker/run/docker.sock", "open -a Docker", "desktop-linux"}
	f := diagnoseDaemon(daemonClues{
		Endpoint: "unix:///h/.docker/run/docker.sock",
		Err:      "`docker info --format {{json .}}` did not answer within 20s (the daemon may still be starting, or be out of memory)",
		Running:  []engine{colima, desktop},
	})
	if f.Summary != "Docker daemon accepts connections but does not answer" || !strings.Contains(f.Fix, "colima stop") {
		t.Errorf("got %q / %q", f.Summary, f.Fix)
	}
}

func TestDiagnoseInstallerCopy(t *testing.T) {
	f := diagnoseDaemon(daemonClues{Endpoint: "unix:///h/.docker/run/docker.sock", Err: "did not answer within 20s", FromInstaller: true})
	if !strings.Contains(f.Summary, "installer disk image") || !strings.Contains(f.Fix, "eject") {
		t.Errorf("got %q / %q", f.Summary, f.Fix)
	}
}
