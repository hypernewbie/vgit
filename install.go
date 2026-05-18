package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "One-time setup: create ~/vgit/ and optionally configure Google Drive backup",
	Long: `Sets up the vgit data directory, verifies git and rclone are available,
and optionally walks you through rclone Google Drive authentication.

Re-running vgit install against an existing non-empty directory will error
rather than clobber. Pass --force to override (e.g. to add a remote after a
--no-gdrive initial run). Additional backup targets (mounted folders, NAS)
can be configured in a later pass.`,
	RunE:         runInstall,
	SilenceUsage: true,
}

var (
	installDir      string
	installNoGdrive bool
	installYes      bool
	installForce    bool
)

func init() {
	home, _ := os.UserHomeDir()
	installCmd.Flags().StringVar(&installDir, "dir", filepath.Join(home, "vgit"), "Installation directory")
	installCmd.Flags().BoolVar(&installNoGdrive, "no-gdrive", false, "Skip Google Drive setup")
	installCmd.Flags().BoolVar(&installYes, "yes", false, "Non-interactive; skip all prompts (implies --no-gdrive)")
	installCmd.Flags().BoolVar(&installForce, "force", false, "Proceed even if the directory already exists and is non-empty")
}

func runInstall(cmd *cobra.Command, args []string) error {
	dir := expandHome(installDir)

	// Existence check: non-empty dir without --force is a hard stop.
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 && !installForce {
		return fmt.Errorf("%s already exists and is non-empty\n"+
			"  Pass --force to proceed anyway, or remove the directory first.", dir)
	}

	// Create directory tree.
	for _, sub := range []string{".", "repos", "bundles", "config"} {
		path := filepath.Join(dir, sub)
		if err := os.MkdirAll(path, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", path, err)
		}
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("chmod %s: %w", dir, err)
	}
	fmt.Printf("vgit: creating %s\n", dir)

	// Sanity checks.
	gitMaj, gitMin, err := gitVersion()
	if err != nil {
		return fmt.Errorf("%w\n  Install: apt install git", err)
	}
	if gitMaj < 2 || (gitMaj == 2 && gitMin < 22) {
		return fmt.Errorf("git %d.%d is too old (need >= 2.22)", gitMaj, gitMin)
	}
	fmt.Printf("vgit: git %d.%d %s\n", gitMaj, gitMin, green("(ok)"))

	rcloneVer, err := rcloneVersion()
	if err != nil {
		return fmt.Errorf("%w\n  Install: sudo apt install rclone\n  Or: https://rclone.org/downloads/", err)
	}
	fmt.Printf("vgit: %s %s\n", rcloneVer, green("(ok)"))

	// Load existing config so a --force rerun preserves known remotes.
	configDir := filepath.Join(dir, "config")
	tomlPath := filepath.Join(configDir, "vgit.toml")
	cfg, err := loadConfig(tomlPath)
	if err != nil {
		cfg = newConfig(dir)
	} else {
		cfg.Install.Dir = dir
		cfg.Install.Version = Version
	}

	// Gdrive auth.
	if !installNoGdrive && !installYes {
		if promptYesNo("Set up Google Drive remote for backups? [Y/n] ", true) {
			if err := rcloneAuthGdrive(configDir); err != nil {
				return err
			}
			cfg.Remotes["gdrive"] = RemoteConfig{
				Enabled:      true,
				RcloneRemote: "gdrive",
				Bucket:       "vgit-backups",
			}
		}
	}

	// Write vgit.toml.
	if err := saveConfig(tomlPath, cfg); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	// Summary.
	fmt.Println()
	fmt.Printf("vgit: installed at %s\n", bold(dir))
	remoteList := "none"
	if len(cfg.Remotes) > 0 {
		names := make([]string, 0, len(cfg.Remotes))
		for k := range cfg.Remotes {
			names = append(names, k)
		}
		remoteList = strings.Join(names, ", ")
	}
	fmt.Printf("  remotes configured: %s\n", remoteList)
	fmt.Printf("  next: %s\n", bold("vgit init <name>"))
	return nil
}

// expandHome replaces a leading ~ with the user's home directory.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}

// promptYesNo prints prompt and reads a y/n response. defaultYes is returned
// when the user presses Enter without typing anything.
func promptYesNo(prompt string, defaultYes bool) bool {
	fmt.Print(prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return defaultYes
	}
	resp := strings.TrimSpace(strings.ToLower(scanner.Text()))
	if resp == "" {
		return defaultYes
	}
	return resp == "y" || resp == "yes"
}

// defaultVgitDir returns the vgit installation directory, honouring the
// VGIT_DIR environment variable if set.
func defaultVgitDir() string {
	if dir := os.Getenv("VGIT_DIR"); dir != "" {
		return expandHome(dir)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "vgit")
}
