package winruntime

import (
	"errors"
	"path/filepath"
	"strings"
)

type Paths struct {
	InstallRoot string
	DataRoot    string
	ConfigDir   string
	DataDir     string
	LogDir      string
	BackupDir   string
	ConfigFile  string
	SecretsFile string
	LogFile     string
}

func NewPaths(programFilesRoot, programDataRoot string) (Paths, error) {
	if !filepath.IsAbs(programFilesRoot) || !filepath.IsAbs(programDataRoot) {
		return Paths{}, errors.New("Windows runtime roots must be absolute")
	}
	installRoot := filepath.Clean(filepath.Join(programFilesRoot, "IoT Edge"))
	dataRoot := filepath.Clean(filepath.Join(programDataRoot, "IoT Edge"))
	if !within(installRoot, filepath.Clean(programFilesRoot)) || !within(dataRoot, filepath.Clean(programDataRoot)) {
		return Paths{}, errors.New("Windows runtime paths escaped their roots")
	}
	configDir := filepath.Join(dataRoot, "config")
	logDir := filepath.Join(dataRoot, "logs")
	return Paths{InstallRoot: installRoot, DataRoot: dataRoot, ConfigDir: configDir, DataDir: filepath.Join(dataRoot, "data"), LogDir: logDir, BackupDir: filepath.Join(dataRoot, "backups"), ConfigFile: filepath.Join(configDir, "runtime.json"), SecretsFile: filepath.Join(configDir, "secrets.dpapi"), LogFile: filepath.Join(logDir, "iot-edge.log")}, nil
}

func within(candidate, root string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
