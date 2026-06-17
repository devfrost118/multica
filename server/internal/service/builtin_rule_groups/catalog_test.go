package builtinrulegroups

import "testing"

// expectedGroups maps each builtin group's display name to its rule count. The
// counts pin the catalog so an accidental file deletion fails the test.
var expectedGroups = map[string]int{
	"1C Core":      5,
	"1C Developer": 6,
	"1C Architect": 4,
	"1C Reviewer":  5,
}

func TestCatalogLoadsAllGroups(t *testing.T) {
	groups := Catalog()
	if len(groups) != len(expectedGroups) {
		t.Fatalf("expected %d groups, got %d", len(expectedGroups), len(groups))
	}

	byName := map[string]Group{}
	for _, g := range groups {
		byName[g.Name] = g
	}
	for name, wantRules := range expectedGroups {
		g, ok := byName[name]
		if !ok {
			t.Fatalf("missing builtin group %q", name)
		}
		if g.Description == "" {
			t.Errorf("group %q: empty description", name)
		}
		if len(g.Rules) != wantRules {
			t.Errorf("group %q: expected %d rules, got %d", name, wantRules, len(g.Rules))
		}
	}
}

func TestCatalogRulesAreWellFormed(t *testing.T) {
	for _, g := range Catalog() {
		prev := int32(-1 << 30)
		seenFile := map[string]bool{}
		for _, r := range g.Rules {
			if r.Name == "" {
				t.Errorf("group %q: rule with empty name", g.Name)
			}
			if r.Content == "" {
				t.Errorf("group %q rule %q: empty content (DB CHECK would reject)", g.Name, r.Name)
			}
			// Catalog() returns rules pre-sorted by sort_order ascending.
			if r.SortOrder < prev {
				t.Errorf("group %q: rules not ordered by sort_order (%d after %d)", g.Name, r.SortOrder, prev)
			}
			prev = r.SortOrder
			if r.FileName == "" {
				t.Errorf("group %q rule %q: missing file_name", g.Name, r.Name)
				continue
			}
			if seenFile[r.FileName] {
				t.Errorf("group %q: duplicate file_name %q (unique within group)", g.Name, r.FileName)
			}
			seenFile[r.FileName] = true
		}
	}
}

func TestCatalogIsDeterministic(t *testing.T) {
	first := Catalog()
	second := Catalog()
	if len(first) != len(second) {
		t.Fatalf("non-deterministic group count: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Slug != second[i].Slug {
			t.Fatalf("non-deterministic group order at %d: %q vs %q", i, first[i].Slug, second[i].Slug)
		}
	}
}
