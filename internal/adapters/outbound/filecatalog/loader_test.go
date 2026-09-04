package filecatalog_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/claudioed/fulfillment-execution/internal/adapters/outbound/filecatalog"
)

func writeTempCatalogue(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "catalogue.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp catalogue: %v", err)
	}
	return path
}

func TestLoad_ValidCatalogue_LoadsAllDeclaredPaths(t *testing.T) {
	path := writeTempCatalogue(t, `
building: test-fc
paths:
  - id: PICK
    matchPrefix: pick
    direct: true
    requiredCapabilities: [pick]
  - id: PACK
    matchPrefix: pack
    direct: true
    requiredCapabilities: [pack]
  - id: REBIN
    matchPrefix: rebin
    direct: true
    requiredCapabilities: [rebin]
  - id: SLAM
    matchPrefix: slam
    direct: true
    requiredCapabilities: [slam]
`)

	cat, err := filecatalog.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, id := range []string{"PICK", "PACK", "REBIN", "SLAM"} {
		if _, err := cat.Lookup(id); err != nil {
			t.Fatalf("Lookup(%q): unexpected error %v", id, err)
		}
	}
	// Real-world path_id forms must resolve too, not just the bare
	// canonical id — see path_definition_test.go's regression test for
	// the full rationale.
	for _, id := range []string{"pick", "pick-zone-a", "pack-soak"} {
		if _, err := cat.Lookup(id); err != nil {
			t.Fatalf("Lookup(%q): unexpected error %v", id, err)
		}
	}
}

func TestLoad_MissingFile_ReturnsError(t *testing.T) {
	_, err := filecatalog.Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("expected an error for a missing file, got nil")
	}
}

func TestLoad_InvalidYAML_ReturnsError(t *testing.T) {
	path := writeTempCatalogue(t, "not: valid: yaml: [")
	_, err := filecatalog.Load(path)
	if err == nil {
		t.Fatal("expected an error for invalid YAML, got nil")
	}
}

func TestLoad_ZeroPaths_ReturnsError(t *testing.T) {
	path := writeTempCatalogue(t, "building: empty-fc\npaths: []\n")
	_, err := filecatalog.Load(path)
	if err == nil {
		t.Fatal("expected an error for a catalogue declaring zero paths, got nil")
	}
}

func TestLoad_PathWithEmptyId_ReturnsError(t *testing.T) {
	path := writeTempCatalogue(t, `
building: bad-fc
paths:
  - id: ""
    matchPrefix: pick
    direct: true
    requiredCapabilities: [pick]
`)
	_, err := filecatalog.Load(path)
	if err == nil {
		t.Fatal("expected an error for a path with an empty id, got nil")
	}
}

func TestLoad_PathWithEmptyMatchPrefix_ReturnsError(t *testing.T) {
	path := writeTempCatalogue(t, `
building: bad-fc
paths:
  - id: PICK
    matchPrefix: ""
    direct: true
    requiredCapabilities: [pick]
`)
	_, err := filecatalog.Load(path)
	if err == nil {
		t.Fatal("expected an error for a path with an empty matchPrefix, got nil")
	}
}

func TestLoad_PathWithNoCapabilities_ReturnsError(t *testing.T) {
	path := writeTempCatalogue(t, `
building: bad-fc
paths:
  - id: PICK
    matchPrefix: pick
    direct: true
    requiredCapabilities: []
`)
	_, err := filecatalog.Load(path)
	if err == nil {
		t.Fatal("expected an error for a path with zero requiredCapabilities, got nil")
	}
}

// Real-integration proof: load the ACTUAL catalogue file
// warehouse-infra publishes, not a synthetic fixture, so a schema drift
// between that file and this loader's parsing fails a test rather than
// only being discovered at deploy time. Gated on an env var (rather than
// a guessed relative sibling path) because this repo may be checked out
// as a plain clone or as one of several git worktrees sharing a parent
// directory — a relative path guess would be correct for one layout and
// silently wrong for the other. Set WAREHOUSE_INFRA_CATALOGUE_PATH to
// exercise this test; it skips (not fails) when unset, since CI does not
// check out a sibling warehouse-infra repo.
func TestLoad_RealWarehouseInfraCatalogue(t *testing.T) {
	path := os.Getenv("WAREHOUSE_INFRA_CATALOGUE_PATH")
	if path == "" {
		t.Skip("WAREHOUSE_INFRA_CATALOGUE_PATH not set, skipping real-catalogue test")
	}

	cat, err := filecatalog.Load(path)
	if err != nil {
		t.Fatalf("failed to load the real warehouse-infra catalogue at %s: %v", path, err)
	}
	for _, id := range []string{"PICK", "PACK", "REBIN", "SLAM"} {
		if _, err := cat.Lookup(id); err != nil {
			t.Fatalf("real catalogue missing expected path %q: %v", id, err)
		}
	}
	// The real fleet path_id forms this catalogue exists to handle.
	for _, id := range []string{"pick", "pick-zone-a", "pack-soak"} {
		if _, err := cat.Lookup(id); err != nil {
			t.Fatalf("real catalogue failed to resolve real-world path_id %q: %v", id, err)
		}
	}
}
