//go:build windows

package winservice

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
)

type eventLogger interface {
	Info(eventID uint32, message string) error
	Error(eventID uint32, message string) error
	Close() error
}

type handler struct {
	name string
	run  func(context.Context) error
	log  eventLogger
}

type serviceContextKey struct{}

func IsServiceContext(ctx context.Context) bool {
	value, _ := ctx.Value(serviceContextKey{}).(bool)
	return value
}

func Run(name string, run func(context.Context) error) (bool, error) {
	if name == "" || run == nil {
		return false, errors.New("service name and runtime are required")
	}
	isService, err := svc.IsWindowsService()
	if err != nil || !isService {
		return false, err
	}
	logger, logErr := eventlog.Open(name)
	if logErr != nil {
		return true, fmt.Errorf("open Windows Event Log source: %w", logErr)
	}
	defer logger.Close()
	return true, svc.Run(name, &handler{name: name, run: run, log: logger})
}

func (h *handler) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), serviceContextKey{}, true))
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.run(ctx) }()
	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	_ = h.log.Info(1, h.name+" started")

	for {
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				_ = h.log.Error(2, h.name+" stopped with a runtime error")
				changes <- svc.Status{State: svc.Stopped, Win32ExitCode: 1}
				return true, 1
			}
			_ = h.log.Info(2, h.name+" stopped")
			changes <- svc.Status{State: svc.Stopped}
			return false, 0
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				changes <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending, CheckPoint: 1, WaitHint: 30000}
				cancel()
			case svc.Pause, svc.Continue:
				// Pause is deliberately unsupported; acquisition state must not be half-paused.
			default:
			}
		}
	}
}
