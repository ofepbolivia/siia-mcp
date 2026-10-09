package postgres

import (
	"context"
	"testing"
)

func TestSchemaSetKeyIsOrderIndependent(t *testing.T) {
	a := schemaSetKey([]string{"public", "rrhh"})
	b := schemaSetKey([]string{"rrhh", "public"})
	if a != b {
		t.Fatalf("schemaSetKey order-sensitive: %q != %q", a, b)
	}
	if a == schemaSetKey(nil) {
		t.Fatal("non-empty set collides with empty set")
	}
}

func TestSessionResolverCachesEmptySearchPath(t *testing.T) {
	r := newSessionResolver()
	// With no schemas the collision check is skipped, so no live connection is
	// needed; the resolved value must be pg_catalog and it must be memoized.
	sp, err := r.searchPath(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sp != "pg_catalog" {
		t.Fatalf("search path = %q, want pg_catalog", sp)
	}
	if got, ok := r.cache[schemaSetKey(nil)]; !ok || got != "pg_catalog" {
		t.Fatalf("cache = %v, want memoized pg_catalog", r.cache)
	}
}

func TestCatalogKeyNamespaced(t *testing.T) {
	a := &adapter{name: "siia_ser"}
	got := a.catalogKey("kind", "public", "books")
	if want := "siia_ser|cat:kind|public|books"; got != want {
		t.Fatalf("catalogKey = %q, want %q", got, want)
	}
}
