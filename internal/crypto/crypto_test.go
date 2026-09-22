package crypto

import (
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	b, err := New("ZGV2LW9ubHktZW5jcnlwdGlvbi1rZXktMzItYnl0ZXM=")
	if err != nil {
		t.Fatal(err)
	}
	enc, err := b.Encrypt("s3cret")
	if err != nil || !strings.HasPrefix(enc, prefix) || strings.Contains(enc, "s3cret") {
		t.Fatalf("encrypt: %q %v", enc, err)
	}
	if got, err := b.Decrypt(enc); err != nil || got != "s3cret" {
		t.Fatalf("decrypt: %q %v", got, err)
	}
	other, _ := New("b3RoZXItZW5jcnlwdGlvbi1rZXktMzItYnl0ZXMhISE=")
	if _, err := other.Decrypt(enc); err == nil {
		t.Error("wrong key must fail")
	}
	if _, err := New("c2hvcnQ="); err == nil {
		t.Error("short key must fail")
	}
}
