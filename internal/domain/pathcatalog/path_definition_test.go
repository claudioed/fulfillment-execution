package pathcatalog_test

import (
	"errors"
	"testing"

	"github.com/claudioed/fulfillment-execution/internal/domain/pathcatalog"
)

func TestCatalogue_Lookup_UnknownPathId_ReturnsError(t *testing.T) {
	cat := pathcatalog.New([]pathcatalog.PathDefinition{
		{Id: "PICK", Direct: true, RequiredCapabilities: []string{"pick"}},
	})
	if _, err := cat.Lookup("REBIN"); !errors.Is(err, pathcatalog.ErrUnknownPath) {
		t.Fatalf("want ErrUnknownPath for an id not in the catalogue, got %v", err)
	}
}

func TestCatalogue_Lookup_KnownPathId_ReturnsDefinition(t *testing.T) {
	cat := pathcatalog.New([]pathcatalog.PathDefinition{
		{Id: "PICK", Direct: true, RequiredCapabilities: []string{"pick"}},
	})
	def, err := cat.Lookup("PICK")
	if err != nil || def.Id != "PICK" || !def.Direct {
		t.Fatalf("unexpected lookup result: %+v, err=%v", def, err)
	}
	if len(def.RequiredCapabilities) != 1 || def.RequiredCapabilities[0] != "pick" {
		t.Fatalf("expected RequiredCapabilities [pick], got %v", def.RequiredCapabilities)
	}
}

// The catalogue must hold every one of this fleet's four declared paths
// simultaneously and answer each independently — this is the "real
// multi-path test" the plan called out as needing REBIN (Feature A) to
// exist first.
func TestCatalogue_Lookup_AllFourDeclaredPaths(t *testing.T) {
	cat := pathcatalog.New([]pathcatalog.PathDefinition{
		{Id: "PICK", Direct: true, RequiredCapabilities: []string{"pick"}},
		{Id: "PACK", Direct: true, RequiredCapabilities: []string{"pack"}},
		{Id: "REBIN", Direct: true, RequiredCapabilities: []string{"rebin"}},
		{Id: "SLAM", Direct: true, RequiredCapabilities: []string{"slam"}},
	})
	for _, want := range []string{"PICK", "PACK", "REBIN", "SLAM"} {
		def, err := cat.Lookup(want)
		if err != nil {
			t.Fatalf("Lookup(%q): unexpected error %v", want, err)
		}
		if def.Id != want {
			t.Fatalf("Lookup(%q): got id %q", want, def.Id)
		}
	}
}

func TestCatalogue_Ids_ReturnsEveryDeclaredId(t *testing.T) {
	cat := pathcatalog.New([]pathcatalog.PathDefinition{
		{Id: "PICK", Direct: true, RequiredCapabilities: []string{"pick"}},
		{Id: "PACK", Direct: true, RequiredCapabilities: []string{"pack"}},
	})
	ids := cat.Ids()
	if len(ids) != 2 {
		t.Fatalf("expected 2 ids, got %v", ids)
	}
}

// An empty catalogue is a valid (if useless) Catalogue value at the
// domain level — New does not itself enforce non-emptiness. That
// invariant belongs to the loader (a boot-time concern), not to the
// in-memory type, per this package's own doc comment.
func TestCatalogue_EmptyCatalogue_EveryLookupFails(t *testing.T) {
	cat := pathcatalog.New(nil)
	if _, err := cat.Lookup("PICK"); !errors.Is(err, pathcatalog.ErrUnknownPath) {
		t.Fatalf("want ErrUnknownPath, got %v", err)
	}
}
