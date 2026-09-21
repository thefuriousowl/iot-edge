package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/thefuriousowl/iot-edge/internal/winservice"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	command := os.Args[1]
	flags := flag.NewFlagSet(command, flag.ExitOnError)
	name := flags.String("name", winservice.DefaultName, "Windows Service name")
	executable := flags.String("exe", "", "absolute path to iot-edge-server.exe")
	if err := flags.Parse(os.Args[2:]); err != nil {
		log.Fatal(err)
	}
	var err error
	switch command {
	case "install":
		err = winservice.Install(winservice.InstallOptions{Name: *name, ExecutablePath: *executable})
	case "start":
		err = winservice.Start(*name)
	case "stop":
		err = winservice.Stop(*name)
	case "restart":
		err = winservice.Restart(*name)
	case "status":
		status, statusErr := winservice.Status(*name)
		err = statusErr
		if err == nil {
			fmt.Printf("%s state=%d pid=%d\n", *name, status.State, status.PID)
		}
	case "uninstall":
		err = winservice.Uninstall(*name)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		log.Fatal(err)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: iot-edge-service <install|start|stop|restart|status|uninstall> [options]")
}
