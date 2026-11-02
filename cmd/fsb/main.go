package main

import (
	"EverythingSuckz/fsb/config"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

const versionString = "4.0.0-stream"

var rootCmd = &cobra.Command{
	Use:               "fsb [command]",
	Short:             "Telegram File Stream Server",
	Long:              "Streaming / serving half of the file-to-link bot. Pair it with the fsb-bot repo.",
	Example:           "fsb run --port 8080",
	Version:           versionString,
	CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	Run:               func(cmd *cobra.Command, args []string) { cmd.Help() },
}

func init() {
	config.SetFlagsFromConfig(runCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(sessionCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
