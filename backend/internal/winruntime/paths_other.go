//go:build !windows

package winruntime

import "errors"

func DefaultPaths() (Paths, error) {
	return Paths{}, errors.New("native Windows paths are unavailable")
}
