package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/spf13/cobra"
)

var repoInitCmd = &cobra.Command{
	Use:   "init <name>",
	Short: "Create a bare git repo configured for partial-clone and auto-maintenance",
	Long: `Creates a bare git repository at ~/vgit/repos/<name>.git, configures it
for partial-clone (uploadpack.allowFilter), bitmap indexes, and commit graph,
then starts git maintenance for automated gc/repack via systemd user timers.

Name must be alphanumeric with dashes or underscores (e.g. "a1", "game-assets").

Note: receive.denyNonFastForwards is intentionally not set. Enable it once
early-development force-pushes are no longer needed:
  git -C ~/vgit/repos/<name>.git config receive.denyNonFastForwards true`,
	Args:         cobra.ExactArgs(1),
	RunE:         runRepoInit,
	SilenceUsage: true,
}

var repoInitDescription string

func init() {
	repoInitCmd.Flags().StringVar(&repoInitDescription, "description", "", "Repository description (written to .git/description)")
}

var validRepoName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

func runRepoInit(cmd *cobra.Command, args []string) error {
	name := args[0]
	if !validRepoName.MatchString(name) {
		return fmt.Errorf("invalid repo name %q\n"+
			"  Use alphanumeric characters, dashes, and underscores only.\n"+
			"  No leading dots, slashes, or spaces.", name)
	}

	// Find install dir via vgit.toml.
	vgitDir := defaultVgitDir()
	tomlPath := filepath.Join(vgitDir, "config", "vgit.toml")
	cfg, err := loadConfig(tomlPath)
	if err != nil {
		return fmt.Errorf("%w\n  Run `vgit install` first.", err)
	}

	repoPath := filepath.Join(cfg.Install.Dir, "repos", name+".git")
	if _, err := os.Stat(repoPath); err == nil {
		return fmt.Errorf("repo already exists at %s", repoPath)
	}

	// Create bare repo.
	if err := runQuiet("git", "init", "--bare", repoPath); err != nil {
		return fmt.Errorf("git init --bare: %w", err)
	}
	fmt.Printf("vgit: created bare repo at %s\n", bold(repoPath))

	// Optional description.
	if repoInitDescription != "" {
		descPath := filepath.Join(repoPath, "description")
		if err := os.WriteFile(descPath, []byte(repoInitDescription+"\n"), 0o644); err != nil {
			return fmt.Errorf("writing description: %w", err)
		}
	}

	// Configure for partial-clone, bitmaps, and commit graph.
	fmt.Println()
	configs := [][2]string{
		{"uploadpack.allowFilter", "true"},
		{"uploadpack.allowAnySHA1InWant", "true"},
		{"gc.writeBitmaps", "true"},
		{"pack.useBitmaps", "true"},
		{"core.commitGraph", "true"},
		{"gc.writeCommitGraph", "true"},
	}
	for _, kv := range configs {
		if err := runQuiet("git", "-C", repoPath, "config", kv[0], kv[1]); err != nil {
			return fmt.Errorf("git config %s: %w", kv[0], err)
		}
		fmt.Printf("  %-38s = %s\n", kv[0], green(kv[1]))
	}

	// Start git maintenance (installs systemd user timers for auto-gc).
	fmt.Println()
	if err := runQuiet("git", "-C", repoPath, "maintenance", "start"); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", yellow("vgit: warning: git maintenance start failed (no systemd user session?):"))
		fmt.Fprintf(os.Stderr, "  %v\n", err)
		fmt.Fprintln(os.Stderr, "  You can run it manually once a systemd user session is active.")
	} else {
		fmt.Printf("vgit: git maintenance start: %s\n", green("ok"))
	}

	// Clone URL hints.
	fmt.Printf(`
Clone from another machine:
  %s

Or, without partial-clone:
  %s

To enable receive.denyNonFastForwards later:
  %s
`,
		bold(fmt.Sprintf("git clone --filter=blob:none --sparse charon:vgit/repos/%s.git", name)),
		bold(fmt.Sprintf("git clone charon:vgit/repos/%s.git", name)),
		dim(fmt.Sprintf(`ssh charon "git -C ~/vgit/repos/%s.git config receive.denyNonFastForwards true"`, name)),
	)

	return nil
}
