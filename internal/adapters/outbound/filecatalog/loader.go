// Package filecatalog loads a process-path catalogue from a YAML file on
// disk (the config/process-paths/*.yaml file warehouse-infra publishes)
// into a pathcatalog.Catalogue. Loading is a BOOT-TIME invariant: a
// missing or malformed file must fail the process's startup outright,
// never fall back to a partial or empty catalogue — a service running
// with the wrong (or no) path definitions would silently accept work for
// paths it cannot actually validate, which is worse than refusing to
// start.
package filecatalog

import (
	"fmt"
	"os"

	"github.com/claudioed/fulfillment-execution/internal/domain/pathcatalog"
	"gopkg.in/yaml.v3"
)

// document is the on-disk shape of a catalogue YAML file. Field names
// match warehouse-infra's config/process-paths/*.yaml exactly — see that
// file's own header comment for the schema and rationale.
type document struct {
	Building string         `yaml:"building"`
	Paths    []pathDocument `yaml:"paths"`
}

type pathDocument struct {
	Id                   string   `yaml:"id"`
	MatchPrefix          string   `yaml:"matchPrefix"`
	Direct               bool     `yaml:"direct"`
	RequiredCapabilities []string `yaml:"requiredCapabilities"`
}

// Load reads and parses the catalogue YAML at path, validating it into a
// pathcatalog.Catalogue. It returns an error — never a partial/empty
// catalogue — if the file cannot be read, is not valid YAML, declares
// zero paths, or declares any path with an empty id, an empty
// matchPrefix, or an empty requiredCapabilities set. Callers
// (composition roots) are expected to treat a non-nil error as fatal:
// log it and exit, do not start serving traffic with a catalogue that
// failed to load.
func Load(path string) (*pathcatalog.Catalogue, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path is operator-controlled config (a boot-time flag/env value), not user input.
	if err != nil {
		return nil, fmt.Errorf("filecatalog: read %s: %w", path, err)
	}

	var doc document
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("filecatalog: parse %s: %w", path, err)
	}

	if len(doc.Paths) == 0 {
		return nil, fmt.Errorf("filecatalog: %s declares zero paths", path)
	}

	defs := make([]pathcatalog.PathDefinition, 0, len(doc.Paths))
	for i, p := range doc.Paths {
		if p.Id == "" {
			return nil, fmt.Errorf("filecatalog: %s: path at index %d has an empty id", path, i)
		}
		if p.MatchPrefix == "" {
			return nil, fmt.Errorf("filecatalog: %s: path %q has an empty matchPrefix", path, p.Id)
		}
		if len(p.RequiredCapabilities) == 0 {
			return nil, fmt.Errorf("filecatalog: %s: path %q declares zero requiredCapabilities", path, p.Id)
		}
		defs = append(defs, pathcatalog.PathDefinition{
			Id:                   p.Id,
			MatchPrefix:          p.MatchPrefix,
			Direct:               p.Direct,
			RequiredCapabilities: p.RequiredCapabilities,
		})
	}

	return pathcatalog.New(defs), nil
}
