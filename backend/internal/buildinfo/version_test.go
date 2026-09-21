package buildinfo

import "testing"

func TestVersionIsWindowsV02ReleaseCandidate(t *testing.T) {
	if Version != "0.2.0-rc.1" {
		t.Fatalf("Version = %q, want %q", Version, "0.2.0-rc.1")
	}
}
