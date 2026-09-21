//go:build windows

package winservice

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

const DefaultName = "IoTEdge"
const DefaultDisplayName = "IoT Edge"
const LocalServiceAccount = `NT AUTHORITY\LocalService`

type InstallOptions struct{ Name, DisplayName, ExecutablePath string }

func ValidateInstall(options InstallOptions) (InstallOptions, error) {
	options.Name = strings.TrimSpace(options.Name)
	options.DisplayName = strings.TrimSpace(options.DisplayName)
	if options.Name == "" {
		options.Name = DefaultName
	}
	if options.DisplayName == "" {
		options.DisplayName = DefaultDisplayName
	}
	executablePath := strings.TrimSpace(options.ExecutablePath)
	if !filepath.IsAbs(executablePath) || !strings.EqualFold(filepath.Base(executablePath), "iot-edge-server.exe") {
		return InstallOptions{}, errors.New("an absolute server .exe path is required")
	}
	absolute, err := filepath.Abs(executablePath)
	if err != nil {
		return InstallOptions{}, errors.New("resolve server executable path")
	}
	if info, statErr := os.Stat(absolute); statErr != nil || info.IsDir() {
		return InstallOptions{}, errors.New("server executable does not exist")
	}
	options.ExecutablePath = filepath.Clean(absolute)
	return options, nil
}

func Install(options InstallOptions) error {
	validated, err := ValidateInstall(options)
	if err != nil {
		return err
	}
	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager: %w", err)
	}
	defer manager.Disconnect()
	service, err := manager.CreateService(validated.Name, validated.ExecutablePath, mgr.Config{
		DisplayName: validated.DisplayName, Description: "IoT Edge industrial data acquisition and analytics runtime",
		StartType: mgr.StartAutomatic, ErrorControl: mgr.ErrorNormal, ServiceStartName: LocalServiceAccount,
		SidType: windows.SERVICE_SID_TYPE_UNRESTRICTED, DelayedAutoStart: true,
	})
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	rollback := true
	defer func() {
		_ = service.Close()
		if rollback {
			_ = service.Delete()
			_ = eventlog.Remove(validated.Name)
		}
	}()
	actions := []mgr.RecoveryAction{{Type: mgr.ServiceRestart, Delay: 5 * time.Second}, {Type: mgr.ServiceRestart, Delay: 15 * time.Second}, {Type: mgr.ServiceRestart, Delay: time.Minute}}
	if err := service.SetRecoveryActions(actions, 86400); err != nil {
		return fmt.Errorf("set recovery actions: %w", err)
	}
	if err := service.SetRecoveryActionsOnNonCrashFailures(true); err != nil {
		return fmt.Errorf("set recovery failure policy: %w", err)
	}
	if err := eventlog.InstallAsEventCreate(validated.Name, eventlog.Info|eventlog.Warning|eventlog.Error); err != nil {
		return fmt.Errorf("install Event Log source: %w", err)
	}
	rollback = false
	return nil
}

func Start(name string) error {
	return withService(name, func(service *mgr.Service) error {
		if err := service.Start(); err != nil {
			return err
		}
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			status, err := service.Query()
			if err != nil {
				return err
			}
			if status.State == svc.Running {
				return nil
			}
			if status.State == svc.Stopped {
				return errors.New("service stopped before reaching running state")
			}
			time.Sleep(250 * time.Millisecond)
		}
		return errors.New("timed out waiting for running state")
	})
}
func Stop(name string) error { return controlAndWait(name, svc.Stop, svc.Stopped, 30*time.Second) }
func Restart(name string) error {
	if err := Stop(name); err != nil {
		return err
	}
	return Start(name)
}
func Status(name string) (ServiceStatus, error) {
	var status svc.Status
	err := withService(name, func(service *mgr.Service) error {
		var queryErr error
		status, queryErr = service.Query()
		return queryErr
	})
	return ServiceStatus{State: uint32(status.State), PID: status.ProcessId}, err
}
func Uninstall(name string) error {
	_ = Stop(name)
	err := withService(name, func(service *mgr.Service) error { return service.Delete() })
	if err != nil {
		return err
	}
	if err := eventlog.Remove(normalizeName(name)); err != nil {
		return fmt.Errorf("remove Event Log source: %w", err)
	}
	return nil
}

func withService(name string, action func(*mgr.Service) error) error {
	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager: %w", err)
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(normalizeName(name))
	if err != nil {
		return fmt.Errorf("open service: %w", err)
	}
	defer service.Close()
	if err := action(service); err != nil {
		return fmt.Errorf("service operation: %w", err)
	}
	return nil
}
func controlAndWait(name string, command svc.Cmd, target svc.State, timeout time.Duration) error {
	return withService(name, func(service *mgr.Service) error {
		status, err := service.Control(command)
		if err != nil {
			return err
		}
		deadline := time.Now().Add(timeout)
		for status.State != target && time.Now().Before(deadline) {
			time.Sleep(250 * time.Millisecond)
			status, err = service.Query()
			if err != nil {
				return err
			}
		}
		if status.State != target {
			return fmt.Errorf("timed out waiting for state %d", target)
		}
		return nil
	})
}
func normalizeName(name string) string {
	if strings.TrimSpace(name) == "" {
		return DefaultName
	}
	return strings.TrimSpace(name)
}
