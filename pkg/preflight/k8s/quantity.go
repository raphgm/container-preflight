package k8s

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// quantity parses a Kubernetes resource quantity into a base value: CPU in
// millicores, everything else in bytes (or plain units). It covers the
// suffixes manifests use; exponent notation is accepted as a plain float.
func quantity(s string, cpu bool) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	mult := 1.0
	suffixes := []struct {
		s string
		m float64
	}{
		{"Ki", 1 << 10}, {"Mi", 1 << 20}, {"Gi", 1 << 30}, {"Ti", 1 << 40}, {"Pi", 1 << 50}, {"Ei", 1 << 60},
		{"k", 1e3}, {"M", 1e6}, {"G", 1e9}, {"T", 1e12}, {"P", 1e15}, {"E", 1e18},
		{"m", 1e-3}, {"u", 1e-6}, {"n", 1e-9},
	}
	for _, suf := range suffixes {
		if strings.HasSuffix(s, suf.s) {
			mult, s = suf.m, strings.TrimSuffix(s, suf.s)
			break
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid quantity %q", s)
	}
	v *= mult
	if cpu {
		v *= 1000 // cores -> millicores
	}
	return int64(math.Ceil(v - 1e-9)), nil
}

func formatCPU(milli int64) string {
	if milli%1000 == 0 {
		return strconv.FormatInt(milli/1000, 10)
	}
	return strconv.FormatInt(milli, 10) + "m"
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
