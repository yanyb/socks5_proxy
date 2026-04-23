package geo

import (
	"net"
	"testing"
)

func TestNopLookuper_ReturnsZeroGeo(t *testing.T) {
	g := NopLookuper.Lookup(net.ParseIP("8.8.8.8"))
	if !g.IsZero() {
		t.Fatalf("expected zero geo, got %+v", g)
	}
}

func TestOpen_EmptyPath(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Fatal("expected error on empty path")
	}
}

func TestOpen_MissingFile(t *testing.T) {
	if _, err := Open("/nonexistent/__no_such__.mmdb"); err == nil {
		t.Fatal("expected error when DB file is missing")
	}
}

func TestGeoLite2_LookupNilSafe(t *testing.T) {
	// Nil receiver / nil reader must not panic.
	var g *GeoLite2
	if !g.Lookup(net.ParseIP("8.8.8.8")).IsZero() {
		t.Fatal("nil receiver should return zero geo")
	}
	g2 := &GeoLite2{}
	if !g2.Lookup(net.ParseIP("8.8.8.8")).IsZero() {
		t.Fatal("nil inner reader should return zero geo")
	}
}
