package renderer

import (
	"encoding/json"
	"os"

	"github.com/raphgm/container-preflight/pkg/report"
)

type JSON struct{}

func NewJSON() *JSON {
	return &JSON{}
}

func (j *JSON) Render(r report.Report) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(r)
}
