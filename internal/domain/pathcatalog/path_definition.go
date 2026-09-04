// Package pathcatalog models the process-path catalogue as a small,
// in-memory lookup: which process-path ids are valid, and what capability
// set each one requires. The catalogue's CONTENT is configuration data
// (see warehouse-infra/config/process-paths/*.yaml) — this package only
// models its shape and the Lookup behavior every consumer needs. Loading
// the YAML itself is a separate concern (see the filecatalog adapter),
// kept out of the domain layer per this repository's hexagonal
// discipline (ADR-0001).
package pathcatalog

import "errors"

// ErrUnknownPath is returned by Lookup when id is not present in the
// catalogue. Every consumer of the catalogue must treat this as a real
// error, never as "default to some other path" — see ADR on the
// path-catalogue-as-configuration decision for why the WorkReleased
// consumer's old defaulting behavior was a documented bug, not a feature.
var ErrUnknownPath = errors.New("pathcatalog: unknown process path id")

// PathDefinition is one declared process path: its id, whether it feeds
// directly into an execution task (true for every path in this fleet's
// current single-building topology), and the capability set a station
// must hold to work it.
type PathDefinition struct {
	Id                   string
	Direct               bool
	RequiredCapabilities []string
}

// Catalogue is the validated, in-memory set of a building's declared
// process paths, keyed by id for O(1) lookup.
type Catalogue struct {
	byId map[string]PathDefinition
}

// New builds a Catalogue from defs. Callers (typically a loader adapter)
// are responsible for ensuring defs is non-empty and each definition is
// well-formed — New itself does not re-validate, since the loader is
// expected to reject a malformed source before ever calling this
// constructor (see filecatalog.Load's doc comment for the boot-time
// failure contract).
func New(defs []PathDefinition) *Catalogue {
	byId := make(map[string]PathDefinition, len(defs))
	for _, d := range defs {
		byId[d.Id] = d
	}
	return &Catalogue{byId: byId}
}

// Lookup returns the declared definition for id, or ErrUnknownPath if id
// is not part of this catalogue.
func (c *Catalogue) Lookup(id string) (PathDefinition, error) {
	def, ok := c.byId[id]
	if !ok {
		return PathDefinition{}, ErrUnknownPath
	}
	return def, nil
}

// Ids returns every declared path id, in no particular order. Used by
// callers that need to enumerate the catalogue (e.g. a health/debug
// endpoint), not by Lookup itself.
func (c *Catalogue) Ids() []string {
	out := make([]string, 0, len(c.byId))
	for id := range c.byId {
		out = append(out, id)
	}
	return out
}
