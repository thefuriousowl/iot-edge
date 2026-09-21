//go:build windows

package winruntime

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestMachineDPAPIAndAtomicSecretStoreRoundTrip(t *testing.T) {
	plaintext := []byte("generated-test-secret-material")
	protected, err := ProtectMachine(plaintext, secretEntropy)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(protected, plaintext) {
		t.Fatal("DPAPI ciphertext contains plaintext")
	}
	recovered, err := UnprotectMachine(protected, secretEntropy)
	if err != nil || !bytes.Equal(recovered, plaintext) {
		t.Fatalf("unprotect = %q, %v", recovered, err)
	}
	if _, err := UnprotectMachine(protected, []byte("different-entropy-material")); err == nil {
		t.Fatal("wrong entropy decrypted ciphertext")
	}

	filename := filepath.Join(t.TempDir(), "config", "secrets.dpapi")
	want := Secrets{DatabaseURL: "postgres://generated@127.0.0.1/generated", JWTSecret: "generated-jwt-secret-at-least-thirty-two-characters"}
	if err := WriteSecrets(filename, want); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filename)
	if bytes.Contains(raw, []byte(want.DatabaseURL)) || bytes.Contains(raw, []byte(want.JWTSecret)) {
		t.Fatal("secret file contains plaintext")
	}
	got, err := ReadSecrets(filename)
	if err != nil || got != want {
		t.Fatalf("ReadSecrets() = %#v, %v", got, err)
	}
}
