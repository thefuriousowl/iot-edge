//go:build windows

package winservice

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateInstallRequiresExactAbsoluteServerBinary(t *testing.T) {
	if _, err := ValidateInstall(InstallOptions{ExecutablePath: "iot-edge-server.exe"}); err == nil {
		t.Fatal("relative executable was accepted")
	}
	if _, err := ValidateInstall(InstallOptions{ExecutablePath: filepath.Join(t.TempDir(), "other.exe")}); err == nil {
		t.Fatal("wrong executable name was accepted")
	}
	directory := t.TempDir()
	server := filepath.Join(directory, "iot-edge-server.exe")
	if err := os.WriteFile(server, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	validated, err := ValidateInstall(InstallOptions{ExecutablePath: server})
	if err != nil {
		t.Fatalf("ValidateInstall() error = %v", err)
	}
	if validated.Name != DefaultName || validated.DisplayName != DefaultDisplayName || validated.ExecutablePath != server {
		t.Fatalf("validated options = %#v", validated)
	}
}
