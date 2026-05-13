package grpccreds

import "testing"

func TestLoadServerCredentials_PlaintextWhenUnset(t *testing.T) {
	c, err := LoadServerCredentials("", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c != nil {
		t.Fatalf("expected nil credentials for plaintext")
	}
}

func TestLoadServerCredentials_ErrorWhenPartialPaths(t *testing.T) {
	_, err := LoadServerCredentials("/tmp/nonexistent.crt", "", "")
	if err == nil {
		t.Fatal("expected error when only cert path set")
	}
	_, err = LoadServerCredentials("", "/tmp/nonexistent.key", "")
	if err == nil {
		t.Fatal("expected error when only key path set")
	}
}
