package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var cfg Config
var version = "dev" // set by -ldflags at build time

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
	root.AddCommand(daemonCmd())

	err := root.Execute()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
