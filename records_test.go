package main

import (
	"context"
	"slices"
	"testing"

	"github.com/jaxxstorm/dnsscale/providers"
	"go.uber.org/zap"
)

func TestQualify(t *testing.T) {
	cases := []struct{ in, want string }{
		{"music", "music.example.com"},
		{"music.example.com", "music.example.com"},
		{"music.example.com.", "music.example.com"},
		{"*", "*.example.com"},
		{"@", "example.com"},
		{"", "example.com"},
		{"  music  ", "music.example.com"},
		// A name that merely contains the domain is not already qualified.
		{"example.com.attacker", "example.com.attacker.example.com"},
	}

	for _, tc := range cases {
		if got := qualify(tc.in, "example.com"); got != tc.want {
			t.Errorf("qualify(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Route53 returns wildcard records with the "*" octal-escaped, which has to be
// undone before names can be compared.
func TestNormalizeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"music.example.com.", "music.example.com"},
		{"Music.Example.COM", "music.example.com"},
		{`\052.example.com.`, "*.example.com"},
		{"*.example.com", "*.example.com"},
		{`\137test.example.com`, "_test.example.com"},
		// A stray backslash that is not a valid escape is left alone.
		{`weird\zz.example.com`, `weird\zz.example.com`},
	}

	for _, tc := range cases {
		if got := normalizeName(tc.in); got != tc.want {
			t.Errorf("normalizeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestOwnerFromValue(t *testing.T) {
	if owner, ok := ownerFromValue(ownershipValue("node-1")); !ok || owner != "node-1" {
		t.Errorf("ownerFromValue round trip = %q, %v", owner, ok)
	}
	if owner, ok := ownerFromValue(ownershipValue(ownerStatic)); !ok || owner != ownerStatic {
		t.Errorf("static round trip = %q, %v", owner, ok)
	}
	// Values written by an older version carried embedded quotes.
	if owner, ok := ownerFromValue("dnsscale-managed node_id=node-1"); !ok || owner != "node-1" {
		t.Errorf("legacy value = %q, %v", owner, ok)
	}
	for _, in := range []string{"", "some other TXT record", "v=spf1 -all"} {
		if _, ok := ownerFromValue(in); ok {
			t.Errorf("ownerFromValue(%q) claimed ownership", in)
		}
	}
}

func testReconciler(t *testing.T, p providers.DNSProvider) *DNSReconciler {
	t.Helper()
	return NewDNSReconciler(nil, p, "example.com", 0, zap.NewNop())
}

func TestNamesForNodeIncludesAliasesAndTags(t *testing.T) {
	r := testReconciler(t, newFakeProvider())
	r.aliases["fresno"] = []string{"music", "mealie", "memory"}
	r.tagAliases["tag:photos"] = "photos"

	node := TailscaleNode{ID: "n1", Name: "fresno", Tags: []string{"tag:photos", "tag:unrelated"}}

	got := r.namesForNode(node)
	want := []string{
		"fresno.example.com",
		"mealie.example.com",
		"memory.example.com",
		"music.example.com",
		"photos.example.com",
	}

	if !slices.Equal(got, want) {
		t.Errorf("namesForNode() = %v, want %v", got, want)
	}
}

func TestNamesForNodeDeduplicates(t *testing.T) {
	r := testReconciler(t, newFakeProvider())
	// The alias repeats the node's own name and the tag-derived name.
	r.aliases["fresno"] = []string{"fresno", "music", "music.example.com"}
	r.tagAliases["tag:music"] = "music"

	node := TailscaleNode{ID: "n1", Name: "fresno", Tags: []string{"tag:music"}}

	got := r.namesForNode(node)
	want := []string{"fresno.example.com", "music.example.com"}
	if !slices.Equal(got, want) {
		t.Errorf("namesForNode() = %v, want %v", got, want)
	}
}

// Every alias needs its own ownership marker, or the delete path cannot reclaim
// it when the node goes away.
func TestAliasRecordsCarryOwnership(t *testing.T) {
	fake := newFakeProvider()
	r := testReconciler(t, fake)
	r.aliases["fresno"] = []string{"music"}
	r.nodeCache["n1"] = TailscaleNode{
		ID:        "n1",
		Name:      "fresno",
		Addresses: []string{"100.64.0.1", "fd7a::1"},
	}

	if err := r.reconcile(context.Background(), "n1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	var txtNames []string
	for _, rec := range fake.records {
		if rec.Type == "TXT" {
			if owner, ok := ownerFromValue(rec.Value); !ok || owner != "n1" {
				t.Errorf("TXT at %s has owner %q", rec.Name, owner)
			}
			txtNames = append(txtNames, rec.Name)
		}
	}

	slices.Sort(txtNames)
	want := []string{"fresno.example.com", "music.example.com"}
	if !slices.Equal(txtNames, want) {
		t.Errorf("ownership TXT records at %v, want %v", txtNames, want)
	}

	// And the alias must actually be reclaimable.
	if err := r.deleteNodeDNS(context.Background(), "n1"); err != nil {
		t.Fatalf("deleteNodeDNS: %v", err)
	}
	if len(fake.records) != 0 {
		t.Errorf("expected alias records reclaimed, still have %v", fake.names())
	}
}

func TestStaticRecordsGetOwnershipMarker(t *testing.T) {
	r := testReconciler(t, newFakeProvider())
	r.static = []StaticRecord{
		{Name: "*", Type: "A", Value: "100.64.0.1", TTL: 300},
	}

	records := r.staticRecords()
	if len(records) != 2 {
		t.Fatalf("expected the record plus its marker, got %d: %+v", len(records), records)
	}

	for _, rec := range records {
		if rec.Name != "*.example.com" {
			t.Errorf("record name = %q, want *.example.com", rec.Name)
		}
	}

	var sawTXT bool
	for _, rec := range records {
		if rec.Type == "TXT" {
			sawTXT = true
			if owner, ok := ownerFromValue(rec.Value); !ok || owner != ownerStatic {
				t.Errorf("static marker owner = %q", owner)
			}
		}
	}
	if !sawTXT {
		t.Error("static record has no ownership marker")
	}
}
