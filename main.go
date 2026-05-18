// vgit — self-hosted git on charon with gdrive bundle backups.
//
// Turns this Linux box into a canonical git server for private repos that
// can't live on GitHub (proprietary assets, large binaries). Repos are bare,
// configured for partial-clone and bitmap indexes. Backup commands (bundle,
// push to gdrive) are planned for a future pass.
//
// MIT Licence. Copyright (c) 2026 UAA Software / hypernewbie.
// See LICENSE for full terms.

package main

import (
	"os"

	"github.com/spf13/cobra"
)

var noColourFlag bool

var rootCmd = &cobra.Command{
	Use:     "vgit",
	Short:   "Self-hosted git on charon with gdrive backup",
	Version: Version,
	Long: `vgit turns this Linux box into a canonical git server for private repositories.

Repos live at ~/vgit/repos/<name>.git, configured for partial-clone and
bitmap indexes so large-asset repos can stay lean on client machines.

MIT Licence. Copyright (c) 2026 UAA Software / hypernewbie.`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		initColour(noColourFlag)
	},
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&noColourFlag, "no-colour", false, "Disable colour output")
	rootCmd.AddCommand(installCmd)
	rootCmd.AddCommand(repoInitCmd)
	rootCmd.AddCommand(backupCmd)
	rootCmd.AddCommand(statusCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
