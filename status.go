package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show install health, configured remotes, and repo/backup state",
	Long: `Reports on the current vgit install:

  Install     directory existence and permissions
  Tools       presence and versions of git, rclone, rsync
  Remotes     backup targets configured in vgit.toml
  Repositories per-repo size, last commit, and local bundle age

Local checks only — does not make network calls. To verify the gdrive
remote actually works, run:

  rclone --config ~/vgit/config/rclone.conf lsd gdrive:`,
	Args:         cobra.NoArgs,
	RunE:         runStatus,
	SilenceUsage: true,
}

func runStatus(cmd *cobra.Command, args []string) error {
	vgitDir := defaultVgitDir()

	fmt.Printf("vgit %s\n\n", Version)

	// Install section.
	fmt.Println(bold("Install"))
	info, err := os.Stat(vgitDir)
	if os.IsNotExist(err) {
		printStatusRow("dir", vgitDir, red("not installed"))
		fmt.Println()
		fmt.Println("Run `vgit install` to set up the host.")
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat %s: %w", vgitDir, err)
	}

	perm := info.Mode().Perm()
	dirStatus := green("ok")
	if perm != 0o700 {
		dirStatus = yellow(fmt.Sprintf("mode %04o (want 0700)", perm))
	}
	printStatusRow("dir", vgitDir, dirStatus)

	for _, sub := range []string{"repos", "bundles", "config"} {
		path := filepath.Join(vgitDir, sub)
		if _, err := os.Stat(path); err != nil {
			printStatusRow(sub+"/", path, red("missing"))
		} else {
			printStatusRow(sub+"/", path, green("ok"))
		}
	}

	tomlPath := filepath.Join(vgitDir, "config", "vgit.toml")
	cfg, err := loadConfig(tomlPath)
	if err != nil {
		printStatusRow("vgit.toml", tomlPath, red("missing or unreadable"))
		fmt.Println()
		fmt.Println("Run `vgit install` to initialise.")
		return nil
	}
	printStatusRow("vgit.toml", tomlPath, green("ok"))

	rclonePath := filepath.Join(vgitDir, "config", "rclone.conf")
	if rcInfo, err := os.Stat(rclonePath); err == nil {
		rcStatus := green("ok")
		if rcInfo.Mode().Perm() != 0o600 {
			rcStatus = yellow(fmt.Sprintf("mode %04o (want 0600)", rcInfo.Mode().Perm()))
		}
		printStatusRow("rclone.conf", rclonePath, rcStatus)
	} else {
		printStatusRow("rclone.conf", rclonePath, dim("not present"))
	}

	// Tools section.
	fmt.Println()
	fmt.Println(bold("Tools"))

	if maj, min, err := gitVersion(); err == nil {
		s := green("ok")
		if maj < 2 || (maj == 2 && min < 22) {
			s = yellow(fmt.Sprintf("too old (need >= 2.22)"))
		}
		printStatusRow("git", fmt.Sprintf("%d.%d", maj, min), s)
	} else {
		printStatusRow("git", "", red("missing"))
	}

	if ver, err := rcloneVersion(); err == nil {
		printStatusRow("rclone", ver, green("ok"))
	} else {
		printStatusRow("rclone", "", red("missing"))
	}

	if ver, err := rsyncVersion(); err == nil {
		printStatusRow("rsync", ver, green("ok"))
	} else {
		printStatusRow("rsync", "", dim("not present (needed for ssh backups)"))
	}

	// Remotes section.
	fmt.Println()
	fmt.Println(bold("Remotes"))
	if len(cfg.Remotes) == 0 {
		fmt.Println("  (none configured)")
	} else {
		names := make([]string, 0, len(cfg.Remotes))
		for k := range cfg.Remotes {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, name := range names {
			r := cfg.Remotes[name]
			state := dim("disabled")
			if r.Enabled {
				state = green("enabled")
			}
			detail := ""
			if r.Bucket != "" {
				detail = " (bucket: " + r.Bucket + ")"
			}
			printStatusRow(name, state+detail, "")
		}
	}

	// Repositories section.
	fmt.Println()
	fmt.Println(bold("Repositories"))
	reposDir := filepath.Join(cfg.Install.Dir, "repos")
	bundlesDir := filepath.Join(cfg.Install.Dir, "bundles")
	repos, err := listRepos(reposDir, bundlesDir)
	if err != nil {
		return fmt.Errorf("listing repos: %w", err)
	}
	if len(repos) == 0 {
		fmt.Println("  (none — run `vgit init <name>`)")
		return nil
	}
	fmt.Printf("  %-20s %-10s %-16s %s\n", "NAME", "SIZE", "LAST COMMIT", "BUNDLE")
	for _, r := range repos {
		fmt.Printf("  %-20s %-10s %-16s %s\n", r.Name, r.Size, r.LastCommit, r.Bundle)
	}
	return nil
}

type repoInfo struct {
	Name       string
	Size       string
	LastCommit string
	Bundle     string
}

func listRepos(reposDir, bundlesDir string) ([]repoInfo, error) {
	entries, err := os.ReadDir(reposDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var repos []repoInfo
	for _, e := range entries {
		if !e.IsDir() || !strings.HasSuffix(e.Name(), ".git") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".git")
		repoPath := filepath.Join(reposDir, e.Name())

		size, _ := dirSize(repoPath)
		repos = append(repos, repoInfo{
			Name:       name,
			Size:       humaniseBytes(size),
			LastCommit: repoLastCommit(repoPath),
			Bundle:     bundleInfo(filepath.Join(bundlesDir, name+".bundle")),
		})
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].Name < repos[j].Name })
	return repos, nil
}

func dirSize(path string) (int64, error) {
	var total int64
	err := filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err == nil {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

func repoLastCommit(repoPath string) string {
	out, err := exec.Command("git", "-C", repoPath, "log", "-1", "--format=%ct", "--all").Output()
	if err != nil {
		return dim("(empty)")
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return dim("(empty)")
	}
	ts, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return dim("(unknown)")
	}
	d := time.Since(time.Unix(ts, 0))
	return humaniseDuration(d)
}

func bundleInfo(bundlePath string) string {
	info, err := os.Stat(bundlePath)
	if err != nil {
		return dim("(none)")
	}
	return fmt.Sprintf("%s, %s", humaniseBytes(info.Size()), humaniseDuration(time.Since(info.ModTime())))
}

// humaniseDuration formats a duration as a compact "Xs/m/h/d/mo/y ago" string.
func humaniseDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	days := int(d.Hours() / 24)
	switch {
	case days < 30:
		return fmt.Sprintf("%dd ago", days)
	case days < 365:
		return fmt.Sprintf("%dmo ago", days/30)
	default:
		return fmt.Sprintf("%dy ago", days/365)
	}
}

func printStatusRow(label, value, statusStr string) {
	if statusStr == "" {
		fmt.Printf("  %-12s %s\n", label, value)
		return
	}
	fmt.Printf("  %-12s %-40s %s\n", label, value, statusStr)
}
