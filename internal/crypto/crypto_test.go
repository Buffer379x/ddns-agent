package crypto

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExistingDamagedKeyIsNeverOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".key")
	original := []byte("broken-key")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateKey(path); err == nil {
		t.Fatal("accepted corrupt key")
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(original) {
		t.Fatal("overwrote original key")
	}
}
func TestCipherIntegrityAndLegacyMigration(t *testing.T) {
	enc, _ := NewEncryptor(GenerateKey())
	cipher, err := enc.Encrypt(`{"token":"secret"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{cipher, strings.TrimPrefix(cipher, "v1:"), `{"token":"legacy"}`} {
		if _, err := enc.OpenConfig(value); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []string{"invalidcipher", `{"token":`, cipher[:len(cipher)-4] + "AAAA"} {
		if _, err := enc.OpenConfig(value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	other, _ := NewEncryptor(GenerateKey())
	if _, err := other.Decrypt(cipher); err == nil {
		t.Fatal("accepted different key")
	}
}
