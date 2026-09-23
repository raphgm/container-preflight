// Package project extracts what a container project requires from the host:
// platforms, memory, ports, bind mounts, build inputs and tool features. It
// reads Compose files and Dockerfiles with the same parsers Docker uses.
package project

import (
	"fmt"
	"path/filepath"
)

// Location points at the line in a project file that created a requirement.
type Location struct {
	File string
	Line int
}

func (l Location) String() string {
	if l.File == "" {
		return ""
	}
	if l.Line == 0 {
		return filepath.Base(l.File)
	}
	return fmt.Sprintf("%s:%d", filepath.Base(l.File), l.Line)
}

// Project is the set of requirements extracted from one directory.
type Project struct {
	Dir         string
	Name        string
	ComposeFile string

	Services []*Service

	// ComposeFeatures lists Compose-file syntax that needs a minimum
	// Compose version.
	ComposeFeatures []Feature

	// EnvRefs lists every variable interpolated in the Compose file.
	EnvRefs []EnvRef

	// Env is the environment Compose would interpolate with (OS + .env).
	Env map[string]string

	// EnvFiles lists env_file entries referenced by services.
	EnvFiles []EnvFile
}

type Service struct {
	Name string

	// Image is the image Compose pulls. It is empty when the service is
	// built and never pulled.
	Image string

	Build *Build

	// Platform is the explicit `platform:` value, if any.
	Platform string

	MemLimit       int64
	MemReservation int64
	Replicas       int

	Ports []Port
	Binds []Bind

	GPU bool

	Environment map[string]string

	Location Location
}

type Port struct {
	HostIP   string
	Start    int
	End      int
	Target   uint32
	Protocol string
}

func (p Port) String() string {
	host := fmt.Sprint(p.Start)
	if p.End != p.Start {
		host = fmt.Sprintf("%d-%d", p.Start, p.End)
	}
	if p.HostIP != "" {
		host = p.HostIP + ":" + host
	}
	return fmt.Sprintf("%s->%d/%s", host, p.Target, p.Protocol)
}

type Bind struct {
	Source         string
	Target         string
	CreateHostPath bool
}

type Build struct {
	Context        string
	DockerfilePath string
	Dockerfile     *Dockerfile
	ParseErr       error
	Target         string
	Platforms      []string
	Args           map[string]string
}

// Feature is syntax that only works with a new enough tool.
type Feature struct {
	Name       string
	MinVersion string
	Location   Location
}

type EnvRef struct {
	Name       string
	HasDefault bool
	Required   bool
	Location   Location
}

type EnvFile struct {
	Service  string
	Path     string
	Required bool
}
