package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// emptyTreeSHA is git's well-known constant for the empty tree object. Used as
// the tree of the synthetic octopus-marker commit so the marker carries no
// file content of its own.
const emptyTreeSHA = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

var backupFullFlag bool

var backupCmd = &cobra.Command{
	Use:   "backup <repo> <target>",
	Short: "Bundle a repo and push it to a backup target (incremental by default)",
	Long: `Creates a git bundle and uploads it to a target. By default each run produces
a small incremental bundle containing only the commits added since the last
backup to this target. Pass --full to consolidate into a fresh full bundle and
prune old incrementals from the destination.

Target syntax:
  gdrive:<path>              Upload via rclone (requires gdrive set up by vgit install)
  ssh:<user@host:path>       Upload via rsync over SSH

Destination layout (per repo):
  <target>/<repo>/full.bundle
  <target>/<repo>/incr.001.bundle
  <target>/<repo>/incr.002.bundle
  ...

State is tracked per (repo, destination) inside the bare repo as a git ref
under refs/vgit/dest/<hash>/ and a git-config counter. So backing up the same
repo to two different targets keeps two independent chains.

Examples:
  vgit backup a1 gdrive:vgit-backups/
  vgit backup a1 gdrive:vgit-backups/ --full
  vgit backup a1 ssh:hypernewbie@hyperion:~/vgit_backup/`,
	Args:         cobra.ExactArgs(2),
	RunE:         runBackup,
	SilenceUsage: true,
}

func init() {
	backupCmd.Flags().BoolVar(&backupFullFlag, "full", false, "Force a fresh full bundle and prune old incrementals from the destination")
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

	destHash := destID(target)
	stagingDir := filepath.Join(cfg.Install.Dir, "bundles", repo)

	// Decide full vs incremental.
	needsFull := backupFullFlag
	if !needsFull {
		has, err := hasMarker(repoPath, destHash)
		if err != nil {
			return err
		}
		needsFull = !has
	}

	if needsFull {
		return runBackupFull(repoPath, repo, target, targetPath, targetType, stagingDir, destHash, cfg)
	}
	return runBackupIncr(repoPath, repo, target, targetPath, targetType, stagingDir, destHash, cfg)
}

func runBackupFull(repoPath, repo, target, targetPath, targetType, stagingDir, destHash string, cfg *Config) error {
	fmt.Printf("vgit: full backup of %s -> %s\n", bold(repo), bold(target))

	// Wipe and recreate local staging dir so any old incrementals are gone
	// locally; the upload step uses sync-with-delete so they're pruned remotely.
	if err := os.RemoveAll(stagingDir); err != nil {
		return fmt.Errorf("clearing staging dir: %w", err)
	}
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return fmt.Errorf("creating staging dir: %w", err)
	}

	bundlePath := filepath.Join(stagingDir, "full.bundle")
	if err := runQuiet("git", "-C", repoPath, "bundle", "create", bundlePath,
		"--branches", "--tags"); err != nil {
		return fmt.Errorf("git bundle create: %w\n"+
			"  (Does the repo have any commits yet?)", err)
	}
	if err := runQuiet("git", "-C", repoPath, "bundle", "verify", bundlePath); err != nil {
		return fmt.Errorf("git bundle verify: %w", err)
	}

	if info, err := os.Stat(bundlePath); err == nil {
		fmt.Printf("vgit: bundle written: %s (%s)\n", bundlePath, humaniseBytes(info.Size()))
	}

	// Upload first; only commit state (counter + marker) after a successful
	// upload so a network failure leaves the previous chain intact.
	fmt.Printf("vgit: syncing to %s (will prune old incrementals)\n", bold(target))
	if err := uploadFull(targetPath, targetType, repo, stagingDir, cfg); err != nil {
		return fmt.Errorf("upload: %w", err)
	}

	if err := setCounter(repoPath, destHash, 0); err != nil {
		return fmt.Errorf("resetting counter: %w", err)
	}
	if err := writeMarker(repoPath, destHash); err != nil {
		return fmt.Errorf("writing marker: %w", err)
	}

	fmt.Println(green("vgit: backup complete (full)"))
	return nil
}

func runBackupIncr(repoPath, repo, target, targetPath, targetType, stagingDir, destHash string, cfg *Config) error {
	// Cheap pre-flight: any new commits since last marker?
	n, err := countNewCommits(repoPath, destHash)
	if err != nil {
		return fmt.Errorf("checking for new commits: %w", err)
	}
	if n == 0 {
		fmt.Println(dim("vgit: nothing new to back up"))
		return nil
	}
	fmt.Printf("vgit: %d new commit(s) since last backup to %s\n", n, bold(target))

	prev, err := getCounter(repoPath, destHash)
	if err != nil {
		return err
	}
	next := prev + 1

	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return fmt.Errorf("creating staging dir: %w", err)
	}

	bundleName := incrFilename(next)
	bundlePath := filepath.Join(stagingDir, bundleName)

	fmt.Printf("vgit: bundling %s\n", bold(bundleName))
	if err := runQuiet("git", "-C", repoPath, "bundle", "create", bundlePath,
		"--branches", "--tags", "--not", markerRef(destHash)); err != nil {
		return fmt.Errorf("git bundle create: %w", err)
	}
	if err := runQuiet("git", "-C", repoPath, "bundle", "verify", bundlePath); err != nil {
		return fmt.Errorf("git bundle verify: %w", err)
	}

	if info, err := os.Stat(bundlePath); err == nil {
		fmt.Printf("vgit: bundle written: %s (%s)\n", bundlePath, humaniseBytes(info.Size()))
	}

	fmt.Printf("vgit: uploading to %s\n", bold(target))
	if err := uploadIncr(targetPath, targetType, repo, stagingDir, cfg); err != nil {
		return fmt.Errorf("upload: %w", err)
	}

	if err := setCounter(repoPath, destHash, next); err != nil {
		return fmt.Errorf("saving counter: %w", err)
	}
	if err := writeMarker(repoPath, destHash); err != nil {
		return fmt.Errorf("writing marker: %w", err)
	}

	fmt.Println(green("vgit: backup complete (incremental)"))
	return nil
}

// incrFilename returns the destination filename for the n-th incremental.
// Zero-padded to 3 digits up to 999; auto-widens past that.
func incrFilename(n int) string {
	if n < 1000 {
		return fmt.Sprintf("incr.%03d.bundle", n)
	}
	return fmt.Sprintf("incr.%d.bundle", n)
}

// destID derives a stable per-target hash for naming state refs and counters.
// 12 hex chars (~48 bits) is enough to avoid collisions for the small number
// of destinations a single user has.
func destID(target string) string {
	h := sha256.Sum256([]byte(target))
	return hex.EncodeToString(h[:])[:12]
}

func markerRef(destHash string) string {
	return "refs/vgit/dest/" + destHash + "/marker"
}

func counterKey(destHash string) string {
	return "vgit.dest." + destHash + ".counter"
}

// hasMarker reports whether the marker ref exists for this (repo, dest).
func hasMarker(repoPath, destHash string) (bool, error) {
	err := exec.Command("git", "-C", repoPath, "show-ref", "--verify", "--quiet", markerRef(destHash)).Run()
	if err == nil {
		return true, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("checking marker ref: %w", err)
}

// currentTipSHAs returns the unique commit SHAs at all branch and tag tips.
func currentTipSHAs(repoPath string) ([]string, error) {
	out, err := exec.Command("git", "-C", repoPath, "for-each-ref",
		"--format=%(objectname)", "refs/heads", "refs/tags").Output()
	if err != nil {
		return nil, fmt.Errorf("for-each-ref: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}
	seen := make(map[string]bool, len(lines))
	unique := make([]string, 0, len(lines))
	for _, sha := range lines {
		if sha == "" || seen[sha] {
			continue
		}
		seen[sha] = true
		unique = append(unique, sha)
	}
	return unique, nil
}

// writeMarker creates a synthetic octopus commit whose parents are every
// current branch/tag tip, and points the marker ref at it. The marker has the
// well-known empty tree, so it adds ~0 bytes of object storage. On the next
// incremental, `git bundle create --branches --tags --not <marker-ref>`
// excludes everything reachable from any tip captured at this point.
func writeMarker(repoPath, destHash string) error {
	tips, err := currentTipSHAs(repoPath)
	if err != nil {
		return err
	}
	if len(tips) == 0 {
		return fmt.Errorf("repo has no commits yet — push at least one before backing up")
	}

	args := []string{"-C", repoPath, "commit-tree", emptyTreeSHA}
	for _, tip := range tips {
		args = append(args, "-p", tip)
	}
	args = append(args, "-m", "vgit marker for "+destHash)

	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return fmt.Errorf("commit-tree: %w", err)
	}
	markerSHA := strings.TrimSpace(string(out))
	if err := runQuiet("git", "-C", repoPath, "update-ref", markerRef(destHash), markerSHA); err != nil {
		return fmt.Errorf("update-ref: %w", err)
	}
	return nil
}

// countNewCommits returns the number of commits reachable from any current
// branch/tag tip but not from the marker ref's parents — i.e. how many new
// commits an incremental would carry.
func countNewCommits(repoPath, destHash string) (int, error) {
	out, err := exec.Command("git", "-C", repoPath, "rev-list",
		"--branches", "--tags", "--not", markerRef(destHash)).Output()
	if err != nil {
		return 0, fmt.Errorf("rev-list: %w", err)
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return 0, nil
	}
	return strings.Count(s, "\n") + 1, nil
}

func getCounter(repoPath, destHash string) (int, error) {
	out, err := exec.Command("git", "-C", repoPath, "config", "--get", counterKey(destHash)).Output()
	if err != nil {
		// Exit 1 means the key isn't set — start from 0.
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return 0, nil
		}
		return 0, fmt.Errorf("reading counter: %w", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("parsing counter %q: %w", strings.TrimSpace(string(out)), err)
	}
	return n, nil
}

func setCounter(repoPath, destHash string, n int) error {
	return runQuiet("git", "-C", repoPath, "config", counterKey(destHash), strconv.Itoa(n))
}

// uploadFull syncs the entire staging dir to <target>/<repo>/, deleting any
// files at the destination that no longer exist locally. This is what prunes
// stale incrementals when --full produces a fresh chain.
func uploadFull(target, targetType, repo, stagingDir string, cfg *Config) error {
	destPath := strings.TrimRight(target, "/") + "/" + repo + "/"
	switch targetType {
	case "gdrive":
		configFile := filepath.Join(cfg.Install.Dir, "config", "rclone.conf")
		// `rclone sync` = copy + delete dest-only files.
		return runInteractive("rclone", "--config", configFile, "sync", "--progress",
			stagingDir+"/", destPath)
	case "ssh":
		return runInteractive("rsync", "-az", "--progress", "--delete", "--mkpath",
			stagingDir+"/", destPath)
	}
	return fmt.Errorf("unknown target type %q", targetType)
}

// uploadIncr copies new files in the staging dir to <target>/<repo>/ without
// deleting anything remote. Files already at the destination are skipped.
func uploadIncr(target, targetType, repo, stagingDir string, cfg *Config) error {
	destPath := strings.TrimRight(target, "/") + "/" + repo + "/"
	switch targetType {
	case "gdrive":
		configFile := filepath.Join(cfg.Install.Dir, "config", "rclone.conf")
		return runInteractive("rclone", "--config", configFile, "copy", "--progress",
			stagingDir+"/", destPath)
	case "ssh":
		return runInteractive("rsync", "-az", "--progress", "--mkpath",
			stagingDir+"/", destPath)
	}
	return fmt.Errorf("unknown target type %q", targetType)
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
