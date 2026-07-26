package migration

import (
	"crypto/sha256"
	"encoding/binary"
	"testing"
)

func TestFingerprintSortsAndLengthPrefixesEveryCatalogField(t *testing.T) {
	t.Parallel()

	objects := []CatalogObject{
		{
			Kind:       "column",
			Schema:     "stacks_core",
			Parent:     "source_documents",
			Name:       "provider_document_id",
			Definition: "text|nullable=false|default=",
		},
		{
			Kind:       "table",
			Schema:     "stacks_core",
			Name:       "source_documents",
			Definition: "ordinary",
		},
	}
	reordered := []CatalogObject{objects[1], objects[0]}

	got := Fingerprint(objects)
	if got != Fingerprint(reordered) {
		t.Fatal("Fingerprint() changed with catalog row order")
	}

	want := sha256.New()
	for _, object := range objects {
		for _, field := range []string{
			object.Kind,
			object.Schema,
			object.Parent,
			object.Name,
			object.Definition,
		} {
			var length [8]byte
			binary.BigEndian.PutUint64(length[:], uint64(len(field)))
			_, _ = want.Write(length[:])
			_, _ = want.Write([]byte(field))
		}
	}
	var wantSum [sha256.Size]byte
	copy(wantSum[:], want.Sum(nil))
	if got != wantSum {
		t.Fatalf("Fingerprint() = %x, want independently length-prefixed %x", got, wantSum)
	}

	left := Fingerprint([]CatalogObject{{
		Kind: "ab", Schema: "c", Parent: "", Name: "d", Definition: "ef",
	}})
	right := Fingerprint([]CatalogObject{{
		Kind: "a", Schema: "bc", Parent: "", Name: "d", Definition: "ef",
	}})
	if left == right {
		t.Fatal("Fingerprint() collided across adjacent catalog-field boundaries")
	}
}

func TestFingerprintChangesForEveryCatalogObjectField(t *testing.T) {
	t.Parallel()

	baseline := CatalogObject{
		Kind:       "column",
		Schema:     "stacks_core",
		Parent:     "observations",
		Name:       "predicate",
		Definition: "text|nullable=false|default=",
	}
	tests := []struct {
		name   string
		mutate func(*CatalogObject)
	}{
		{name: "kind", mutate: func(object *CatalogObject) { object.Kind = "index" }},
		{name: "schema", mutate: func(object *CatalogObject) { object.Schema = "stacks_directory" }},
		{name: "parent", mutate: func(object *CatalogObject) { object.Parent = "mentions" }},
		{name: "name", mutate: func(object *CatalogObject) { object.Name = "role" }},
		{name: "definition", mutate: func(object *CatalogObject) { object.Definition = "text|nullable=true|default=" }},
	}

	wantDifferentFrom := Fingerprint([]CatalogObject{baseline})
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			mutated := baseline
			test.mutate(&mutated)
			if got := Fingerprint([]CatalogObject{mutated}); got == wantDifferentFrom {
				t.Fatalf("Fingerprint() did not change when %s changed", test.name)
			}
		})
	}
}
