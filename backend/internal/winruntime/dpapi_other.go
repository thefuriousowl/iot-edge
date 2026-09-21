//go:build !windows

package winruntime

import "errors"

func ProtectMachine([]byte, []byte) ([]byte, error) {
	return nil, errors.New("DPAPI is only available on Windows")
}
func UnprotectMachine([]byte, []byte) ([]byte, error) {
	return nil, errors.New("DPAPI is only available on Windows")
}
