// Package registry reads image metadata straight from registries, without
// pulling, so preflight can see which platforms an image ships and how big it
// is before Docker ever tries.
package registry

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

// Image describes an image reference as the registry sees it.
type Image struct {
	Ref string

	// Index is true for a multi-platform manifest list. A single-platform
	// image is pulled regardless of host platform; an index without a
	// matching entry makes `docker pull` fail.
	Index bool

	// Platforms are normalized "os/arch[/variant]" strings.
	Platforms []string

	// CompressedSize is the download size for the requested platform, or 0
	// when that platform is not available.
	CompressedSize int64
}

var (
	ErrNotFound     = errors.New("image not found in registry")
	ErrUnauthorized = errors.New("registry denied access")
	ErrNetwork      = errors.New("registry unreachable")
)

type Resolver interface {
	Resolve(ctx context.Context, ref, platform string) (*Image, error)
}

// Remote resolves against real registries using the user's Docker
// credentials. Results are cached per reference and platform.
type Remote struct {
	mu    sync.Mutex
	cache map[string]result

	keychain *fallbackKeychain
}

type result struct {
	img *Image
	err error
}

func NewRemote() *Remote {
	return &Remote{cache: map[string]result{}, keychain: &fallbackKeychain{}}
}

// CredentialError returns the first error hit while reading Docker
// credentials, e.g. a credsStore helper that is configured but not
// installed. Docker itself fails pulls in that state.
func (r *Remote) CredentialError() error {
	r.keychain.mu.Lock()
	defer r.keychain.mu.Unlock()
	return r.keychain.err
}

// fallbackKeychain uses the Docker credentials when they can be read and
// falls back to anonymous access, remembering why.
type fallbackKeychain struct {
	mu  sync.Mutex
	err error
}

func (k *fallbackKeychain) Resolve(target authn.Resource) (authn.Authenticator, error) {
	a, err := authn.DefaultKeychain.Resolve(target)
	if err != nil {
		k.mu.Lock()
		if k.err == nil {
			k.err = err
		}
		k.mu.Unlock()
		return authn.Anonymous, nil
	}
	return a, nil
}

func (r *Remote) Resolve(ctx context.Context, ref, platform string) (*Image, error) {
	key := ref + "|" + platform
	r.mu.Lock()
	if c, ok := r.cache[key]; ok {
		r.mu.Unlock()
		return c.img, c.err
	}
	r.mu.Unlock()

	img, err := r.resolve(ctx, ref, platform)

	r.mu.Lock()
	r.cache[key] = result{img, err}
	r.mu.Unlock()
	return img, err
}

func (r *Remote) resolve(ctx context.Context, ref, platform string) (*Image, error) {
	parsed, err := name.ParseReference(ref)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid reference %q: %v", ErrNotFound, ref, err)
	}
	opts := []remote.Option{
		remote.WithContext(ctx),
		remote.WithAuthFromKeychain(r.keychain),
	}
	desc, err := remote.Get(parsed, opts...)
	if err != nil {
		return nil, classify(err)
	}

	out := &Image{Ref: ref}
	want := Normalize(platform)

	if desc.MediaType.IsIndex() {
		out.Index = true
		idx, err := desc.ImageIndex()
		if err != nil {
			return nil, classify(err)
		}
		im, err := idx.IndexManifest()
		if err != nil {
			return nil, classify(err)
		}
		var match *v1.Descriptor
		for i, m := range im.Manifests {
			if m.Platform == nil || m.Platform.OS == "unknown" {
				continue // attestation manifests
			}
			p := Normalize(m.Platform.String())
			out.Platforms = append(out.Platforms, p)
			if match == nil && Compatible(p, want) {
				match = &im.Manifests[i]
			}
		}
		if match != nil {
			if img, err := idx.Image(match.Digest); err == nil {
				out.CompressedSize = layerSize(img)
			}
		}
		return out, nil
	}

	img, err := desc.Image()
	if err != nil {
		return nil, classify(err)
	}
	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, classify(err)
	}
	p := Normalize(cfg.OS + "/" + cfg.Architecture + "/" + cfg.Variant)
	out.Platforms = []string{p}
	out.CompressedSize = layerSize(img)
	return out, nil
}

func layerSize(img v1.Image) int64 {
	m, err := img.Manifest()
	if err != nil {
		return 0
	}
	var total int64
	for _, l := range m.Layers {
		total += l.Size
	}
	return total
}

func classify(err error) error {
	var te *transport.Error
	if errors.As(err, &te) {
		switch te.StatusCode {
		case http.StatusNotFound:
			return fmt.Errorf("%w: %v", ErrNotFound, err)
		case http.StatusUnauthorized, http.StatusForbidden:
			// Docker Hub answers 401 for repositories that do not exist
			// as well as for private ones.
			return fmt.Errorf("%w: %v", ErrUnauthorized, err)
		}
		for _, d := range te.Errors {
			if d.Code == transport.ManifestUnknownErrorCode || d.Code == transport.NameUnknownErrorCode {
				return fmt.Errorf("%w: %v", ErrNotFound, err)
			}
		}
	}
	var ne net.Error
	var dns *net.DNSError
	if errors.As(err, &ne) || errors.As(err, &dns) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %v", ErrNetwork, err)
	}
	return err
}

// Canonical returns the fully qualified form of ref, so "postgres" and
// "docker.io/library/postgres:latest" compare equal.
func Canonical(ref string) string {
	parsed, err := name.ParseReference(ref)
	if err != nil {
		return ref
	}
	return parsed.Name()
}

// Normalize maps platform spellings to one canonical form.
func Normalize(p string) string {
	p = strings.ToLower(strings.Trim(p, "/"))
	parts := strings.Split(p, "/")
	if len(parts) < 2 {
		return p
	}
	arch := parts[1]
	switch arch {
	case "x86_64", "x86-64":
		arch = "amd64"
	case "aarch64":
		arch = "arm64"
	}
	variant := ""
	if len(parts) > 2 {
		variant = parts[2]
	}
	if arch == "arm64" && variant == "v8" {
		variant = ""
	}
	if variant == "" {
		return parts[0] + "/" + arch
	}
	return parts[0] + "/" + arch + "/" + variant
}

// Compatible reports whether an image built for have runs natively on want.
func Compatible(have, want string) bool {
	have, want = Normalize(have), Normalize(want)
	if have == want {
		return true
	}
	// linux/arm/v7 and friends: accept a missing variant on either side.
	hp, wp := strings.Split(have, "/"), strings.Split(want, "/")
	return len(hp) >= 2 && len(wp) >= 2 && hp[0] == wp[0] && hp[1] == wp[1] && (len(hp) == 2 || len(wp) == 2)
}
