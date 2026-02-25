package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "dev"

var noColor bool

var rootCmd = &cobra.Command{
	Use:   "tbyd",
	Short: "tbyd — local-first personal knowledge base",
	Long:  "A local-first data sovereignty layer that enriches cloud LLM interactions with personal context.",
	CompletionOptions: cobra.CompletionOptions{
		DisableDefaultCmd: true,
	},
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&noColor, "no-color", false, "disable colorized output")

	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(ingestCmd)
	rootCmd.AddCommand(profileCmd)
	rootCmd.AddCommand(recallCmd)
	rootCmd.AddCommand(interactionsCmd)
	rootCmd.AddCommand(dataCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print tbyd version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(version)
	},
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
