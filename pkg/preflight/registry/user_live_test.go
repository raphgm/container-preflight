package registry

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestLiveImageUser(t *testing.T) {
	if os.Getenv("PREFLIGHT_LIVE") == "" {
		t.Skip("set PREFLIGHT_LIVE=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	r := NewRemote()
	for _, ref := range []string{"grafana/grafana:11.2.0", "postgres:16", "docker.elastic.co/elasticsearch/elasticsearch:8.15.0", "jenkins/jenkins:lts"} {
		img, err := r.Resolve(ctx, ref, "linux/arm64")
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%-55s user=%q", ref, img.User)
	}
}
