package rules

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/raphgm/container-doctor/pkg/preflight/fact"
	"github.com/raphgm/container-doctor/pkg/preflight/project"
)

// containerUser returns the user a service's processes start as: the
// Compose `user:` value, else the image's USER. uid is -1 when the user is
// a name preflight cannot resolve without unpacking the image.
func containerUser(env *Env, svc *project.Service) (user string, uid int) {
	user = svc.User
	if user == "" {
		ref := serviceImage(svc)
		if ref != "" {
			want := hostPlatform(env.Host)
			if svc.Platform != "" {
				want = svc.Platform
			}
			if res, ok := env.Images[ImageKey(ref, want)]; ok && res.Image != nil {
				user = res.Image.User
			}
		}
	}
	name, _, _ := strings.Cut(user, ":")
	switch name {
	case "", "root", "0":
		return user, 0
	case "nobody":
		return user, 65534
	}
	if n, err := strconv.Atoi(name); err == nil {
		return user, n
	}
	return user, -1
}

var permissionsRule = rule{
	id:    "mounts.permissions",
	title: "Containers can write to their bind mounts",
	needs: []fact.ID{fact.LocalDaemon, fact.ComposeFile},
	check: func(_ context.Context, env *Env) []Finding {
		h := env.Host
		var out []Finding
		for _, svc := range env.Project.Services {
			user, uid := containerUser(env, svc)
			for _, b := range svc.Binds {
				src := rel(env, b.Source)

				if h.SELinux && b.SELinux == "" {
					out = append(out, Finding{
						Rule:     "mounts.permissions",
						Severity: fact.Error,
						Service:  svc.Name,
						Title:    fmt.Sprintf("SELinux will block access to bind mount %s", src),
						Evidence: []string{"host: SELinux is enabled for Docker", "project: " + src + " is mounted without :z or :Z"},
						Fix:      fmt.Sprintf("Mount it as %s:%s:z (shared) or :Z (private to this container).", src, b.Target),
						Location: svc.Location.String(),
						Predicts: "Permission denied",
					})
				}

				if b.ReadOnly || uid == 0 || h.MapsOwnership() {
					continue
				}
				who := fmt.Sprintf("user %q", user)
				if uid >= 0 {
					who = fmt.Sprintf("uid %d", uid)
				}

				fi, err := os.Stat(b.Source)
				if err != nil {
					if !b.CreateHostPath {
						continue // mounts.bind reports this
					}
					out = append(out, Finding{
						Rule:     "mounts.permissions",
						Severity: fact.Error,
						Service:  svc.Name,
						Title:    fmt.Sprintf("Docker will create %s owned by root, but the container runs as %s", src, who),
						Evidence: []string{"host: " + src + " does not exist; Docker creates missing bind sources as root", "image: runs as " + who},
						Fix:      fmt.Sprintf("Create %s yourself and `chown` it to the container's uid, or use a named volume.", src),
						Location: svc.Location.String(),
						Predicts: "Permission denied",
					})
					continue
				}
				own, ok := owner(fi)
				if !ok || uid < 0 || own == uid || fi.Mode().Perm()&0o002 != 0 {
					continue
				}
				out = append(out, Finding{
					Rule:     "mounts.permissions",
					Severity: fact.Warning,
					Service:  svc.Name,
					Title:    fmt.Sprintf("%s is owned by uid %d, but the container writes as uid %d", src, own, uid),
					Evidence: []string{fmt.Sprintf("host: %s owner %d, mode %s", src, own, fi.Mode().Perm()), "image: runs as " + who},
					Fix:      fmt.Sprintf("`sudo chown -R %d %s`, run the service with `user: \"%d\"`, or mount it read-only if it only reads.", uid, src, own),
					Location: svc.Location.String(),
					Predicts: "Permission denied",
				})
			}
		}
		return out
	},
}
