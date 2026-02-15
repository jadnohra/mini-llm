package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	cfg    Config
	tunnel *SSHTunnel
)

func ensureTunnel() (*SSHTunnel, error) {
	if tunnel != nil {
		return tunnel, nil
	}
	var err error
	tunnel, err = StartTunnel(cfg.SSHUser, cfg.Host, cfg.OllamaPort)
	return tunnel, err
}

func main() {
	cfg = LoadConfig()

	root := &cobra.Command{
		Use:   "mini",
		Short: "Control your Mac Mini AI server",
	}

	root.AddCommand(statusCmd())
	root.AddCommand(modelsCmd())
	root.AddCommand(selftestCmd())
	root.AddCommand(askCmd())

	err := root.Execute()

	// Clean up tunnel
	if tunnel != nil {
		tunnel.Close()
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
