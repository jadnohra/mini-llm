package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	cfg       Config
	tunnel    *SSHTunnel
	sttTunnel *SSHTunnel
)

func ensureTunnel() (*SSHTunnel, error) {
	if tunnel != nil {
		return tunnel, nil
	}
	var err error
	tunnel, err = StartTunnel(cfg.SSHUser, cfg.Host, cfg.OllamaPort)
	return tunnel, err
}

func ensureSTTTunnel() (*SSHTunnel, error) {
	if sttTunnel != nil {
		return sttTunnel, nil
	}
	var err error
	sttTunnel, err = StartTunnel(cfg.SSHUser, cfg.Host, 8090)
	return sttTunnel, err
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
	root.AddCommand(chatCmd())
	root.AddCommand(sessionsCmd())
	root.AddCommand(updateCmd())
	root.AddCommand(sayCmd())
	root.AddCommand(ttsCmd())
	root.AddCommand(sttCmd())
	root.AddCommand(dictateCmd())

	err := root.Execute()

	// Clean up tunnels
	if tunnel != nil {
		tunnel.Close()
	}
	if sttTunnel != nil {
		sttTunnel.Close()
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
