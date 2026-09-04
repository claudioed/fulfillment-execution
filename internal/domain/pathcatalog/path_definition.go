// Package pathcatalog models the process-path catalogue as a small,
// in-memory lookup: which process-path FAMILIES are declared, and what
// capability set each one requires. The catalogue's CONTENT is
// configuration data (see warehouse-infra/config/process-paths/*.yaml) —
// this package only models its shape and the Lookup behavior every
// consumer needs. Loading the YAML itself is a separate concern (see the
// filecatalog adapter), kept out of the domain layer per this
// repository's hexagonal discipline (ADR-0001).
//
// Lookup is a case-insensitive PREFIX match, not an exact match. Real
// path_id values across this fleet are not the bare canonical id
// ("PICK") — they carry a station/zone/scenario suffix chosen by
// whichever upstream service enqueues the work: order-management's
// shared.DefaultPathId is the bare "pick", while the e2e test fixtures
// and ops-agent's seeded targets use "pick-zone-a", "pick-soak",
// "pick-t5-imbalance", and so on. A path's declared MatchPrefix (e.g.
// "pick") is defined to match every one of those forms, because each
// one either equals the prefix exactly or starts with prefix + "-".
// This was a real bug caught and fixed after the exact-match version
// shipped (see the ADR addendum on this decision) — the deleted
// path_id-prefix-guessing convention this catalogue replaced actually
// got this part right, and the replacement had to keep it.
package pathcatalog

import (
	"errors"
	"strings"
)

// ErrUnknownPath is returned by Lookup when id does not match any
// declared path's prefix. Every consumer of the catalogue must treat
// this as a real error, never as "default to some other path" — see the
// process-path-catalogue-as-configuration ADR for why the WorkReleased
// consumer's old defaulting behavior was a documented bug, not a
// feature.
var ErrUnknownPath = errors.New("pathcatalog: unknown process path id")

// PathDefinition is one declared process path: its canonical id (also
// used as fulfillment-execution's task.Type value), the lower-cased
// prefix family of real path_id values it recognizes, whether it feeds
// directly into an execution task (true for every path in this fleet's
// current single-building topology), and the capability set a station
// must hold to work it.
type PathDefinition struct {
	Id                   string
	MatchPrefix          string
	Direct               bool
	RequiredCapabilities []string
}

// Catalogue is the validated, in-memory set of a building's declared
// process paths.
type Catalogue struct {
	defs []PathDefinition
}

// New builds a Catalogue from defs. Callers (typically a loader adapter)
// are responsible for ensuring defs is non-empty and each definition is
// well-formed — New itself does not re-validate, since the loader is
// expected to reject a malformed source before ever calling this
// constructor (see filecatalog.Load's doc comment for the boot-time
// failure contract).
func New(defs []PathDefinition) *Catalogue {
	out := make([]PathDefinition, len(defs))
	copy(out, defs)
	return &Catalogue{defs: out}
}

// Lookup returns the declared definition whose MatchPrefix matches id
// (case-insensitively: id equals the prefix, or id starts with
// prefix + "-"), or ErrUnknownPath if no declared path recognizes id.
// When more than one declared prefix would match (not expected in this
// fleet's current catalogue, but not structurally prevented), the
// LONGEST matching prefix wins, so a more specific declaration always
// takes precedence over a more general one.
func (c *Catalogue) Lookup(id string) (PathDefinition, error) {
	lower := strings.ToLower(id)

	var best PathDefinition
	bestLen := -1
	for _, d := range c.defs {
		prefix := strings.ToLower(d.MatchPrefix)
		if prefix == "" {
			continue
		}
		if !matchesPrefix(lower, prefix) {
			continue
		}
		if len(prefix) > bestLen {
			best = d
			bestLen = len(prefix)
		}
	}
	if bestLen == -1 {
		return PathDefinition{}, ErrUnknownPath
	}
	return best, nil
}

// matchesPrefix reports whether id (already lower-cased) belongs to
// prefix's family: either id equals prefix exactly, or id starts with
// prefix followed by a "-" separator. This deliberately does NOT match
// a bare substring prefix without the separator (e.g. prefix "pick"
// must not match "picking-station") — every real path_id in this fleet
// either is the bare prefix or uses "-" as the next character.
func matchesPrefix(id, prefix string) bool {
	if id == prefix {
		return true
	}
	return strings.HasPrefix(id, prefix+"-")
}

// Ids returns every declared path's canonical id, in no particular
// order. Used by callers that need to enumerate the catalogue (e.g. a
// health/debug endpoint), not by Lookup itself.
func (c *Catalogue) Ids() []string {
	out := make([]string, 0, len(c.defs))
	for _, d := range c.defs {
		out = append(out, d.Id)
	}
	return out
}
