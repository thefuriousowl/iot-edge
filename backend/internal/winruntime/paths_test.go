package winruntime

import (
	"path/filepath"
	"testing"
)

func TestNewPathsBuildsBoundedNativeLayout(t *testing.T) {
	programFiles := filepath.Join(t.TempDir(), "Program Files")
	programData := filepath.Join(t.TempDir(), "ProgramData")
	paths, err := NewPaths(programFiles, programData)
	if err != nil {
		t.Fatal(err)
	}
	if paths.InstallRoot != filepath.Join(programFiles, "IoT Edge") || paths.ConfigFile != filepath.Join(programData, "IoT Edge", "config", "runtime.json") || paths.SecretsFile != filepath.Join(programData, "IoT Edge", "config", "secrets.dpapi") || paths.LogFile != filepath.Join(programData, "IoT Edge", "logs", "iot-edge.log") {
		t.Fatalf("paths = %#v", paths)
	}
	if _, err := NewPaths("relative", programData); err == nil {
		t.Fatal("relative root accepted")
	}
}
