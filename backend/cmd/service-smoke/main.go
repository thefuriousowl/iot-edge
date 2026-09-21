//go:build windows

package main

import (
	"context"
	"log"

	"github.com/thefuriousowl/iot-edge/internal/rotatinglog"
	"github.com/thefuriousowl/iot-edge/internal/winruntime"
	"github.com/thefuriousowl/iot-edge/internal/winservice"
)

var secretPath string
var logPath string

func main() {
	handled, err := winservice.Run(winservice.DefaultName, func(ctx context.Context) error {
		if secretPath != "" {
			if _, err := winruntime.ReadSecrets(secretPath); err != nil {
				return err
			}
		}
		if logPath != "" {
			writer, err := rotatinglog.Open(logPath, 1024, 2)
			if err != nil {
				return err
			}
			defer writer.Close()
			if _, err := writer.Write([]byte("Windows runtime acceptance started\n")); err != nil {
				return err
			}
		}
		<-ctx.Done()
		return ctx.Err()
	})
	if err != nil {
		log.Fatal(err)
	}
	if !handled {
		log.Fatal("service smoke fixture must run under Windows Service Control Manager")
	}
}
