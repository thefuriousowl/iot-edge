package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/thefuriousowl/iot-edge/internal/winruntime"
)

func main() {
	flags := flag.NewFlagSet("protect", flag.ExitOnError)
	output := flags.String("output", "", "absolute destination for DPAPI-protected secrets")
	if len(os.Args) < 2 || os.Args[1] != "protect" {
		log.Fatal("usage: iot-edge-config protect --output <absolute path>; secret JSON is read from stdin")
	}
	if err := flags.Parse(os.Args[2:]); err != nil {
		log.Fatal(err)
	}
	if !filepath.IsAbs(*output) {
		log.Fatal("absolute protected-secret output path is required")
	}
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 64*1024))
	decoder.DisallowUnknownFields()
	var secrets winruntime.Secrets
	if err := decoder.Decode(&secrets); err != nil {
		log.Fatal("secret input is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		log.Fatal("secret input contains trailing data")
	}
	if err := winruntime.WriteSecrets(filepath.Clean(*output), secrets); err != nil {
		log.Fatal(err)
	}
	fmt.Println("Protected Windows runtime secrets written successfully.")
}
