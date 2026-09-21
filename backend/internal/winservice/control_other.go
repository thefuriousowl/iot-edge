//go:build !windows

package winservice

import (
	"errors"
)

const DefaultName = "IoTEdge"

type InstallOptions struct{ Name, DisplayName, ExecutablePath string }

var errUnsupported = errors.New("Windows Service control is only available on Windows")

func ValidateInstall(options InstallOptions) (InstallOptions, error) { return options, errUnsupported }
func Install(InstallOptions) error                                   { return errUnsupported }
func Start(string) error                                             { return errUnsupported }
func Stop(string) error                                              { return errUnsupported }
func Restart(string) error                                           { return errUnsupported }
func Status(string) (ServiceStatus, error)                           { return ServiceStatus{}, errUnsupported }
func Uninstall(string) error                                         { return errUnsupported }
