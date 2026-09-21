//go:build windows

package winruntime

import (
	"errors"
	"os"
)

func DefaultPaths() (Paths, error) {
	programFiles, programData := os.Getenv("ProgramFiles"), os.Getenv("ProgramData")
	if programFiles == "" || programData == "" {
		return Paths{}, errors.New("Windows ProgramFiles and ProgramData are required")
	}
	return NewPaths(programFiles, programData)
}
