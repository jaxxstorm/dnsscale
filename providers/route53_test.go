package providers

import "testing"

// Route53 requires a delete to match the stored record exactly, so whatever
// ListRecords hands back must re-encode to the byte-identical value on the way
// out again.
func TestTXTValueRoundTrip(t *testing.T) {
	cases := []string{
		"dnsscale-managed node_id=nodeXXCNTRL",
		"",
		`value with "quotes" inside`,
		`trailing backslash \`,
		`both \ and " together`,
	}

	for _, want := range cases {
		encoded := quoteTXTValue(want)
		if got := unquoteTXTValue(encoded); got != want {
			t.Errorf("round trip of %q: encoded %q, decoded %q", want, encoded, got)
		}
	}
}

func TestQuoteTXTValue(t *testing.T) {
	if got, want := quoteTXTValue("hello"), `"hello"`; got != want {
		t.Errorf("quoteTXTValue(hello) = %q, want %q", got, want)
	}
	if got, want := quoteTXTValue(`a"b`), `"a\"b"`; got != want {
		t.Errorf(`quoteTXTValue(a"b) = %q, want %q`, got, want)
	}
}

// TXT records written by an older dnsscale, or by hand, may not be quoted at
// all; those must still be readable rather than being mangled.
func TestUnquoteTXTValueLeavesUnquotedValuesAlone(t *testing.T) {
	for _, in := range []string{"", `"`, "unquoted value", `"mismatched`} {
		if got := unquoteTXTValue(in); got != in {
			t.Errorf("unquoteTXTValue(%q) = %q, want unchanged", in, got)
		}
	}
}

func TestEncodeValueOnlyTouchesTXT(t *testing.T) {
	if got, want := encodeValue("A", "100.64.0.1"), "100.64.0.1"; got != want {
		t.Errorf("encodeValue(A) = %q, want %q", got, want)
	}
	if got, want := encodeValue("AAAA", "fd7a::1"), "fd7a::1"; got != want {
		t.Errorf("encodeValue(AAAA) = %q, want %q", got, want)
	}
	if got, want := encodeValue("TXT", "marker"), `"marker"`; got != want {
		t.Errorf("encodeValue(TXT) = %q, want %q", got, want)
	}
}
