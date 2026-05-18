package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var backupCmd = &cobra.Command{
	Use:   "backup <repo> <target>",
	Short: "Bundle a repo and push it to a backup target",
	Long: `Creates a git bundle of all refs in the named repo and uploads it to a target.

Target syntax:
  gdrive:<path>              Upload via rclone (requires gdrive set up by vgit install)
  ssh:<user@host:path>       Upload via rsync over SSH

Each backup overwrites the previous bundle for that repo. Bundles are also
kept locally at ~/vgit/bundles/<repo>.bundle as a staging copy.

Examples:
  vgit backup a1 gdrive:vgit-backups/
  vgit backup a1 ssh:hypernewbie@hyperion:~/vgit_backup/`,
	Args:         cobra.ExactArgs(2),
	RunE:         runBackup,
	SilenceUsage: true,
}

func runBackup(cmd *cobra.Command, args []string) error {
	repo := args[0]
	target := args[1]

	if !validRepoName.MatchString(repo) {
		return fmt.Errorf("invalid repo name %q\n"+
			"  Use alphanumeric characters, dashes, and underscores only.", repo)
	}

	targetType, targetPath, err := parseBackupTarget(target)
	if err != nil {
		return err
	}

	vgitDir := defaultVgitDir()
	tomlPath := filepath.Join(vgitDir, "config", "vgit.toml")
	cfg, err := loadConfig(tomlPath)
	if err != nil {
		return fmt.Errorf("%w\n  Run `vgit install` first.", err)
	}

	repoPath := filepath.Join(cfg.Install.Dir, "repos", repo+".git")
	if _, err := os.Stat(repoPath); err != nil {
		return fmt.Errorf("repo not found at %s", repoPath)
	}

	if targetType == "gdrive" {
		r, ok := cfg.Remotes["gdrive"]
		if !ok || !r.Enabled {
			return fmt.Errorf("gdrive remote not configured\n" +
				"  Run `vgit install --force` and complete the gdrive setup.")
		}
	}

	bundleDir := filepath.Join(cfg.Install.Dir, "bundles")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		return fmt.Errorf("creating bundle dir: %w", err)
	}
	bundlePath := filepath.Join(bundleDir, repo+".bundle")

	fmt.Printf("vgit: bundling %s\n", bold(repo))
	if err := runQuiet("git", "-C", repoPath, "bundle", "create", bundlePath, "--all"); err != nil {
		return fmt.Errorf("git bundle create: %w", err)
	}

	if err := runQuiet("git", "-C", repoPath, "bundle", "verify", bundlePath); err != nil {
		return fmt.Errorf("git bundle verify: %w", err)
	}

	if info, err := os.Stat(bundlePath); err == nil {
		fmt.Printf("vgit: bundle written: %s (%s)\n", bundlePath, humaniseBytes(info.Size()))
	}

	fmt.Printf("vgit: uploading to %s\n", bold(target))
	switch targetType {
	case "gdrive":
		configFile := filepath.Join(cfg.Install.Dir, "config", "rclone.conf")
		if err := runInteractive("rclone", "--config", configFile, "copy", "--progress", bundlePath, targetPath); err != nil {
			return fmt.Errorf("rclone copy: %w", err)
		}
	case "ssh":
		if err := runInteractive("rsync", "-az", "--progress", bundlePath, targetPath); err != nil {
			return fmt.Errorf("rsync: %w", err)
		}
	}

	fmt.Println(green("vgit: backup complete"))
	return nil
}

// parseBackupTarget splits a target like "gdrive:path" or "ssh:user@host:path"
// into a type tag and the path argument to pass to the underlying tool.
//
// For gdrive, the full "gdrive:path" string is preserved (rclone needs it).
// For ssh, the "ssh:" prefix is stripped (rsync wants the bare destination).
func parseBackupTarget(target string) (kind, path string, err error) {
	switch {
	case strings.HasPrefix(target, "gdrive:"):
		return "gdrive", target, nil
	case strings.HasPrefix(target, "ssh:"):
		dest := strings.TrimPrefix(target, "ssh:")
		if dest == "" {
			return "", "", fmt.Errorf("ssh target is empty\n  Expected ssh:<user@host:path>")
		}
		return "ssh", dest, nil
	default:
		return "", "", fmt.Errorf("unknown target type %q\n"+
			"  Expected gdrive:<path> or ssh:<user@host:path>", target)
	}
}

// humaniseBytes formats a byte count using IEC binary units (KiB, MiB, ...).
func humaniseBytes(n int64) string {
	const k = 1024
	if n < k {
		return fmt.Sprintf("%d B", n)
	}
	units := "KMGTPE"
	div, exp := int64(k), 0
	for size := n / k; size >= k && exp < len(units)-1; size /= k {
		div *= k
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), units[exp])
}
