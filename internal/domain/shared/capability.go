package shared

// Capability names a certification or equipment a station must have to
// accept a given task (e.g. "pick", "pack", "slam", "hazmat").
type Capability string

// CapabilitySet is an unordered collection of capabilities.
type CapabilitySet map[Capability]struct{}

// NewCapabilitySet builds a CapabilitySet from a list of capabilities.
func NewCapabilitySet(caps ...Capability) CapabilitySet {
	set := make(CapabilitySet, len(caps))
	for _, c := range caps {
		set[c] = struct{}{}
	}
	return set
}

// HasAll reports whether every capability in required is present in the set.
func (s CapabilitySet) HasAll(required CapabilitySet) bool {
	for c := range required {
		if _, ok := s[c]; !ok {
			return false
		}
	}
	return true
}

// Contains reports whether the set has the given capability.
func (s CapabilitySet) Contains(c Capability) bool {
	_, ok := s[c]
	return ok
}
