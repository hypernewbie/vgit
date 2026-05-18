package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testBin is the path to the vgit binary built by TestMain for integration tests.
var testBin string

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "vgit-testbin-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: MkdirTemp: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmp)

	testBin = filepath.Join(tmp, "vgit")
	if out, err := exec.Command("go", "build", "-o", testBin, ".").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: build failed: %v\n%s\n", err, out)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// --- unit tests --------------------------------------------------------------

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	cases := []struct{ in, want string }{
		{"~", home},
		{"~/foo", filepath.Join(home, "foo")},
		{"~/foo/bar", filepath.Join(home, "foo", "bar")},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
		{"", ""},
		{"~nope", "~nope"}, // not a home prefix
	}
	for _, c := range cases {
		if got := expandHome(c.in); got != c.want {
			t.Errorf("expandHome(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestValidRepoName(t *testing.T) {
	valid := []string{"a1", "a", "my-repo", "game_assets", "ABC", "abc123", "a-b_c"}
	for _, name := range valid {
		if !validRepoName.MatchString(name) {
			t.Errorf("%q should be a valid repo name", name)
		}
	}
	invalid := []string{
		"", ".hidden", "bad/name", "has space", "../escape",
		"-leading-dash", "_leading-underscore", "has.dot",
	}
	for _, name := range invalid {
		if validRepoName.MatchString(name) {
			t.Errorf("%q should be an invalid repo name", name)
		}
	}
}

func TestConfigRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vgit.toml")

	cfg := newConfig("/home/testuser/vgit")
	cfg.Remotes["gdrive"] = RemoteConfig{
		Enabled:      true,
		RcloneRemote: "gdrive",
		Bucket:       "vgit-backups",
	}

	if err := saveConfig(path, cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	loaded, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if loaded.Install.Dir != "/home/testuser/vgit" {
		t.Errorf("dir: got %q", loaded.Install.Dir)
	}
	if loaded.Install.Version != Version {
		t.Errorf("version: got %q, want %q", loaded.Install.Version, Version)
	}
	r, ok := loaded.Remotes["gdrive"]
	if !ok || !r.Enabled || r.Bucket != "vgit-backups" || r.RcloneRemote != "gdrive" {
		t.Errorf("gdrive remote: %+v", loaded.Remotes)
	}
}

func TestLoadConfigMissing(t *testing.T) {
	_, err := loadConfig("/nonexistent/path/vgit.toml")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestColourFunctions(t *testing.T) {
	colourEnabled = false
	for _, fn := range []func(string) string{green, red, yellow, bold, dim} {
		if got := fn("hello"); got != "hello" {
			t.Errorf("colour off: got %q, want %q", got, "hello")
		}
	}

	colourEnabled = true
	cases := []struct {
		fn   func(string) string
		want string
	}{
		{green, "\033[32mhello\033[0m"},
		{red, "\033[31mhello\033[0m"},
		{yellow, "\033[33mhello\033[0m"},
		{bold, "\033[1mhello\033[0m"},
		{dim, "\033[2mhello\033[0m"},
	}
	for _, c := range cases {
		if got := c.fn("hello"); got != c.want {
			t.Errorf("colour on: got %q, want %q", got, c.want)
		}
	}
	colourEnabled = false // reset so other tests are unaffected
}

// --- integration test helpers ------------------------------------------------

// stubRclone writes a minimal rclone shell stub to a temp dir and returns the
// dir path (for prepending to PATH). If VGIT_TEST_RCLONE_LOG is set in the
// environment at runtime, the stub appends each call's args to that file so
// tests can verify what rclone was invoked with.
func stubRclone(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "rclone")
	content := `#!/bin/sh
if [ -n "$VGIT_TEST_RCLONE_LOG" ]; then
  echo "$@" >> "$VGIT_TEST_RCLONE_LOG"
fi
case "$1" in
  version) echo "rclone v99.0.0-stub" ;;
esac
exit 0
`
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("stubRclone: %v", err)
	}
	return dir
}

// testEnv returns a copy of the current environment with PATH prepended by
// prepend and any VGIT_DIR overridden by vgitDir (pass "" to leave unset).
func testEnv(pathPrepend, vgitDir string) []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, e := range os.Environ() {
		switch {
		case strings.HasPrefix(e, "PATH="):
			env = append(env, "PATH="+pathPrepend+":"+strings.TrimPrefix(e, "PATH="))
		case strings.HasPrefix(e, "VGIT_DIR="):
			// drop — we'll set it below if needed
		default:
			env = append(env, e)
		}
	}
	if vgitDir != "" {
		env = append(env, "VGIT_DIR="+vgitDir)
	}
	return env
}

// run executes testBin with the given args and env, returns combined output and error.
func run(env []string, args ...string) (string, error) {
	cmd := exec.Command(testBin, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// --- integration tests -------------------------------------------------------

func TestInstallCreatesLayout(t *testing.T) {
	installDir := filepath.Join(t.TempDir(), "vgit")
	env := testEnv(stubRclone(t), installDir)

	out, err := run(env, "install", "--dir", installDir, "--no-gdrive", "--no-colour")
	if err != nil {
		t.Fatalf("vgit install failed: %v\n%s", err, out)
	}

	for _, sub := range []string{"repos", "bundles", "config"} {
		if _, err := os.Stat(filepath.Join(installDir, sub)); err != nil {
			t.Errorf("missing subdirectory %q: %v", sub, err)
		}
	}

	info, err := os.Stat(installDir)
	if err != nil {
		t.Fatalf("stat install dir: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("install dir mode: got %04o, want 0700", info.Mode().Perm())
	}

	cfg, err := loadConfig(filepath.Join(installDir, "config", "vgit.toml"))
	if err != nil {
		t.Fatalf("loadConfig after install: %v", err)
	}
	if cfg.Install.Dir != installDir {
		t.Errorf("vgit.toml dir: got %q, want %q", cfg.Install.Dir, installDir)
	}
	if cfg.Install.Version != Version {
		t.Errorf("vgit.toml version: got %q", cfg.Install.Version)
	}
}

func TestInstallIdempotencyError(t *testing.T) {
	// A non-empty directory should produce a clear error (no clobber).
	installDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(installDir, "sentinel"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := testEnv(stubRclone(t), installDir)

	out, err := run(env, "install", "--dir", installDir, "--no-gdrive", "--no-colour")
	if err == nil {
		t.Fatalf("expected error for non-empty dir, got success\n%s", out)
	}
	if !strings.Contains(out, "non-empty") {
		t.Errorf("expected 'non-empty' in error output, got:\n%s", out)
	}
}

func TestInstallForce(t *testing.T) {
	// --force should proceed even on a non-empty directory.
	installDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(installDir, "sentinel"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := testEnv(stubRclone(t), installDir)

	out, err := run(env, "install", "--dir", installDir, "--no-gdrive", "--force", "--no-colour")
	if err != nil {
		t.Fatalf("vgit install --force failed: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(filepath.Join(installDir, "config", "vgit.toml")); statErr != nil {
		t.Errorf("vgit.toml not created under --force: %v", statErr)
	}
}

func TestInitCreatesRepo(t *testing.T) {
	installDir := filepath.Join(t.TempDir(), "vgit")
	env := testEnv(stubRclone(t), installDir)

	if out, err := run(env, "install", "--dir", installDir, "--no-gdrive", "--no-colour"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	out, err := run(env, "init", "testrepo", "--description", "smoke test", "--no-colour")
	if err != nil {
		t.Fatalf("vgit init failed: %v\n%s", err, out)
	}

	repoPath := filepath.Join(installDir, "repos", "testrepo.git")

	// Valid bare repo.
	result, err := exec.Command("git", "-C", repoPath, "rev-parse", "--git-dir").Output()
	if err != nil {
		t.Fatalf("not a valid bare repo: %v", err)
	}
	if strings.TrimSpace(string(result)) != "." {
		t.Errorf("rev-parse --git-dir: got %q, want '.'", strings.TrimSpace(string(result)))
	}

	// All six config keys must be "true".
	for _, key := range []string{
		"uploadpack.allowFilter",
		"uploadpack.allowAnySHA1InWant",
		"gc.writeBitmaps",
		"pack.useBitmaps",
		"core.commitGraph",
		"gc.writeCommitGraph",
	} {
		val, err := exec.Command("git", "-C", repoPath, "config", "--get", key).Output()
		if err != nil {
			t.Errorf("config key %s: not set (%v)", key, err)
			continue
		}
		if strings.TrimSpace(string(val)) != "true" {
			t.Errorf("config key %s = %q, want 'true'", key, strings.TrimSpace(string(val)))
		}
	}

	// Description file.
	desc, _ := os.ReadFile(filepath.Join(repoPath, "description"))
	if !strings.Contains(string(desc), "smoke test") {
		t.Errorf("description file: got %q", string(desc))
	}
}

func TestInitDuplicateError(t *testing.T) {
	installDir := filepath.Join(t.TempDir(), "vgit")
	env := testEnv(stubRclone(t), installDir)

	if out, err := run(env, "install", "--dir", installDir, "--no-gdrive", "--no-colour"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if out, err := run(env, "init", "dup", "--no-colour"); err != nil {
		t.Fatalf("first init: %v\n%s", err, out)
	}

	out, err := run(env, "init", "dup", "--no-colour")
	if err == nil {
		t.Fatalf("expected error on duplicate init, got success\n%s", out)
	}
	if !strings.Contains(out, "already exists") {
		t.Errorf("expected 'already exists' in error, got:\n%s", out)
	}
}

func TestInitInvalidNames(t *testing.T) {
	installDir := filepath.Join(t.TempDir(), "vgit")
	env := testEnv(stubRclone(t), installDir)

	if out, err := run(env, "install", "--dir", installDir, "--no-gdrive", "--no-colour"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	invalid := []string{
		"bad/name", ".hidden", "has space", "-leading", "../escape",
	}
	for _, name := range invalid {
		out, err := run(env, "init", name, "--no-colour")
		if err == nil {
			t.Errorf("name %q: expected error, got success\n%s", name, out)
		}
	}
}

func TestInitWithoutInstall(t *testing.T) {
	// VGIT_DIR points to a dir with no vgit.toml → should get a clear error.
	env := testEnv(stubRclone(t), t.TempDir())

	out, err := run(env, "init", "noop", "--no-colour")
	if err == nil {
		t.Fatalf("expected error without prior install, got success\n%s", out)
	}
	if !strings.Contains(out, "vgit install") {
		t.Errorf("expected 'vgit install' hint in error, got:\n%s", out)
	}
}

func TestHelpFlags(t *testing.T) {
	env := testEnv("", "")
	for _, args := range [][]string{
		{"--help"},
		{"install", "--help"},
		{"init", "--help"},
		{"backup", "--help"},
		{"status", "--help"},
	} {
		out, err := run(env, args...)
		if err != nil {
			t.Errorf("%v: unexpected error: %v", args, err)
		}
		if len(out) == 0 {
			t.Errorf("%v: empty help output", args)
		}
	}
}

// --- backup tests ------------------------------------------------------------

func TestParseBackupTarget(t *testing.T) {
	cases := []struct {
		in       string
		wantKind string
		wantPath string
		wantErr  bool
	}{
		{"gdrive:vgit-backups/", "gdrive", "gdrive:vgit-backups/", false},
		{"gdrive:nested/dir/path", "gdrive", "gdrive:nested/dir/path", false},
		{"gdrive:", "gdrive", "gdrive:", false},
		{"ssh:user@host:/path", "ssh", "user@host:/path", false},
		{"ssh:user@host:~/path", "ssh", "user@host:~/path", false},
		{"ssh:/local/path/", "ssh", "/local/path/", false},
		{"ssh:", "", "", true},
		{"unknown:foo", "", "", true},
		{"", "", "", true},
		{"gdrive", "", "", true},
		{"plain/path", "", "", true},
	}
	for _, c := range cases {
		kind, path, err := parseBackupTarget(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseBackupTarget(%q): expected error, got (%q, %q)", c.in, kind, path)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseBackupTarget(%q): unexpected error: %v", c.in, err)
			continue
		}
		if kind != c.wantKind || path != c.wantPath {
			t.Errorf("parseBackupTarget(%q): got (%q, %q), want (%q, %q)",
				c.in, kind, path, c.wantKind, c.wantPath)
		}
	}
}

func TestHumaniseBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1024 * 1024, "1.0 MiB"},
		{2 * 1024 * 1024, "2.0 MiB"},
		{1024 * 1024 * 1024, "1.0 GiB"},
	}
	for _, c := range cases {
		if got := humaniseBytes(c.in); got != c.want {
			t.Errorf("humaniseBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// backupSetup installs vgit and creates a single empty repo, then makes a
// commit so the bundle has something in it (empty bundles fail verify). It
// returns the install dir and env so subsequent run() calls reuse them.
func backupSetup(t *testing.T, repo string) (installDir string, env []string) {
	t.Helper()
	installDir = filepath.Join(t.TempDir(), "vgit")
	env = testEnv(stubRclone(t), installDir)

	if out, err := run(env, "install", "--dir", installDir, "--no-gdrive", "--no-colour"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if out, err := run(env, "init", repo, "--no-colour"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}

	// Push an actual commit so `git bundle create --all` produces a non-empty
	// bundle (a bundle with zero refs fails verify with "needs these prerequisite
	// commits" or similar). Use a temp working clone + push.
	work := t.TempDir()
	repoPath := filepath.Join(installDir, "repos", repo+".git")
	mustRun(t, "git", "clone", repoPath, work)
	mustRun(t, "git", "-C", work, "-c", "user.email=t@test", "-c", "user.name=t", "commit", "--allow-empty", "-m", "initial")
	mustRun(t, "git", "-C", work, "push", "origin", "HEAD:refs/heads/main")
	return installDir, env
}

func mustRun(t *testing.T, name string, args ...string) {
	t.Helper()
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

func TestBackupInvalidTarget(t *testing.T) {
	_, env := backupSetup(t, "a1")
	out, err := run(env, "backup", "a1", "unknown:foo", "--no-colour")
	if err == nil {
		t.Fatalf("expected error, got success\n%s", out)
	}
	if !strings.Contains(out, "unknown target type") {
		t.Errorf("expected 'unknown target type' in error, got:\n%s", out)
	}
}

func TestBackupInvalidRepoName(t *testing.T) {
	_, env := backupSetup(t, "a1")
	out, err := run(env, "backup", "bad/name", "ssh:/tmp/foo", "--no-colour")
	if err == nil {
		t.Fatalf("expected error, got success\n%s", out)
	}
	if !strings.Contains(out, "invalid repo name") {
		t.Errorf("expected 'invalid repo name' in error, got:\n%s", out)
	}
}

func TestBackupNonexistentRepo(t *testing.T) {
	installDir := filepath.Join(t.TempDir(), "vgit")
	env := testEnv(stubRclone(t), installDir)
	if out, err := run(env, "install", "--dir", installDir, "--no-gdrive", "--no-colour"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	out, err := run(env, "backup", "ghost", "ssh:/tmp/foo", "--no-colour")
	if err == nil {
		t.Fatalf("expected error, got success\n%s", out)
	}
	if !strings.Contains(out, "not found") {
		t.Errorf("expected 'not found' in error, got:\n%s", out)
	}
}

func TestBackupGdriveNotConfigured(t *testing.T) {
	_, env := backupSetup(t, "a1")
	out, err := run(env, "backup", "a1", "gdrive:test-backups/", "--no-colour")
	if err == nil {
		t.Fatalf("expected error for unconfigured gdrive, got success\n%s", out)
	}
	if !strings.Contains(out, "gdrive remote not configured") {
		t.Errorf("expected 'gdrive remote not configured' in error, got:\n%s", out)
	}
}

func TestBackupSshToLocalDir(t *testing.T) {
	// rsync supports local destinations (no host: prefix). Use that to test
	// the ssh dispatch end-to-end without needing a real SSH server.
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync not available")
	}

	installDir, env := backupSetup(t, "a1")
	destDir := filepath.Join(t.TempDir(), "backup-dest")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := run(env, "backup", "a1", "ssh:"+destDir+"/", "--no-colour")
	if err != nil {
		t.Fatalf("backup failed: %v\n%s", err, out)
	}

	// Local staging copy.
	localBundle := filepath.Join(installDir, "bundles", "a1.bundle")
	if _, err := os.Stat(localBundle); err != nil {
		t.Errorf("local bundle missing: %v", err)
	}

	// Destination copy.
	destBundle := filepath.Join(destDir, "a1.bundle")
	if _, err := os.Stat(destBundle); err != nil {
		t.Errorf("bundle did not arrive at destination: %v", err)
	}
}

// --- status tests ------------------------------------------------------------

func TestHumaniseDuration(t *testing.T) {
	const (
		minute = 60
		hour   = 60 * minute
		day    = 24 * hour
		mo     = 30 * day
		year   = 365 * day
	)
	cases := []struct {
		in   int64 // seconds
		want string
	}{
		{0, "0s ago"},
		{5, "5s ago"},
		{minute, "1m ago"},
		{59 * minute, "59m ago"},
		{hour, "1h ago"},
		{5 * hour, "5h ago"},
		{day, "1d ago"},
		{29 * day, "29d ago"},
		{31 * day, "1mo ago"},
		{364 * day, "12mo ago"},
		{2 * year, "2y ago"},
	}
	for _, c := range cases {
		got := humaniseDuration(time.Duration(c.in) * time.Second)
		if got != c.want {
			t.Errorf("humaniseDuration(%ds) = %q, want %q", c.in, got, c.want)
		}
	}
	// Negative durations clamp to 0.
	if got := humaniseDuration(-1 * time.Hour); got != "0s ago" {
		t.Errorf("humaniseDuration(negative) = %q, want '0s ago'", got)
	}
}

func TestStatusNotInstalled(t *testing.T) {
	// Point VGIT_DIR at a non-existent path.
	env := testEnv(stubRclone(t), filepath.Join(t.TempDir(), "does-not-exist"))
	out, err := run(env, "status", "--no-colour")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	if !strings.Contains(out, "not installed") {
		t.Errorf("expected 'not installed' in output, got:\n%s", out)
	}
	if !strings.Contains(out, "vgit install") {
		t.Errorf("expected install hint in output, got:\n%s", out)
	}
}

func TestStatusFreshInstall(t *testing.T) {
	installDir := filepath.Join(t.TempDir(), "vgit")
	env := testEnv(stubRclone(t), installDir)
	if out, err := run(env, "install", "--dir", installDir, "--no-gdrive", "--no-colour"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	out, err := run(env, "status", "--no-colour")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}

	// Sections present.
	for _, section := range []string{"Install", "Tools", "Remotes", "Repositories"} {
		if !strings.Contains(out, section) {
			t.Errorf("expected section %q in output, got:\n%s", section, out)
		}
	}
	// Tool checks present.
	for _, tool := range []string{"git", "rclone"} {
		if !strings.Contains(out, tool) {
			t.Errorf("expected tool %q listed, got:\n%s", tool, out)
		}
	}
	if !strings.Contains(out, "(none configured)") {
		t.Errorf("expected '(none configured)' for remotes, got:\n%s", out)
	}
	if !strings.Contains(out, "(none") {
		t.Errorf("expected '(none' for repositories, got:\n%s", out)
	}
}

func TestStatusWithRepoAndBundle(t *testing.T) {
	installDir, env := backupSetup(t, "a1")

	// Run a backup so a local bundle exists.
	destDir := filepath.Join(t.TempDir(), "dest")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("rsync"); err == nil {
		if out, err := run(env, "backup", "a1", "ssh:"+destDir+"/", "--no-colour"); err != nil {
			t.Fatalf("backup: %v\n%s", err, out)
		}
	}

	out, err := run(env, "status", "--no-colour")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}

	// Repo listed by name with size and a commit timestamp.
	if !strings.Contains(out, "a1") {
		t.Errorf("expected repo 'a1' in output, got:\n%s", out)
	}
	if !strings.Contains(out, "ago") {
		t.Errorf("expected 'ago' (last commit / bundle age) in output, got:\n%s", out)
	}

	// Bundle row should reference a size and age if rsync ran.
	if _, err := os.Stat(filepath.Join(installDir, "bundles", "a1.bundle")); err == nil {
		if !strings.Contains(out, "B") && !strings.Contains(out, "KiB") && !strings.Contains(out, "MiB") {
			t.Errorf("expected a humanised size in output, got:\n%s", out)
		}
	}
}

func TestStatusWithGdriveRemote(t *testing.T) {
	installDir := filepath.Join(t.TempDir(), "vgit")
	env := testEnv(stubRclone(t), installDir)
	if out, err := run(env, "install", "--dir", installDir, "--no-gdrive", "--no-colour"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	// Inject a gdrive remote.
	tomlPath := filepath.Join(installDir, "config", "vgit.toml")
	cfg, err := loadConfig(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Remotes["gdrive"] = RemoteConfig{Enabled: true, RcloneRemote: "gdrive", Bucket: "vgit-backups"}
	if err := saveConfig(tomlPath, cfg); err != nil {
		t.Fatal(err)
	}

	out, err := run(env, "status", "--no-colour")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	if !strings.Contains(out, "gdrive") {
		t.Errorf("expected 'gdrive' in remotes section, got:\n%s", out)
	}
	if !strings.Contains(out, "enabled") {
		t.Errorf("expected 'enabled' for gdrive remote, got:\n%s", out)
	}
	if !strings.Contains(out, "vgit-backups") {
		t.Errorf("expected bucket name in remote details, got:\n%s", out)
	}
}

func TestBackupGdriveStubbed(t *testing.T) {
	installDir, env := backupSetup(t, "a1")
	rcloneLog := filepath.Join(t.TempDir(), "rclone.log")
	env = append(env, "VGIT_TEST_RCLONE_LOG="+rcloneLog)

	// Inject a gdrive remote into vgit.toml, bypassing the OAuth flow.
	tomlPath := filepath.Join(installDir, "config", "vgit.toml")
	cfg, err := loadConfig(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Remotes["gdrive"] = RemoteConfig{Enabled: true, RcloneRemote: "gdrive", Bucket: "test"}
	if err := saveConfig(tomlPath, cfg); err != nil {
		t.Fatal(err)
	}

	out, err := run(env, "backup", "a1", "gdrive:test-backups/", "--no-colour")
	if err != nil {
		t.Fatalf("backup failed: %v\n%s", err, out)
	}

	// Verify rclone was called with the right copy command.
	logBytes, err := os.ReadFile(rcloneLog)
	if err != nil {
		t.Fatalf("reading rclone log: %v", err)
	}
	log := string(logBytes)
	if !strings.Contains(log, "copy") {
		t.Errorf("rclone copy not invoked; log:\n%s", log)
	}
	if !strings.Contains(log, "gdrive:test-backups/") {
		t.Errorf("rclone not called with gdrive:test-backups/; log:\n%s", log)
	}
	if !strings.Contains(log, "a1.bundle") {
		t.Errorf("rclone not called with bundle path; log:\n%s", log)
	}
}
