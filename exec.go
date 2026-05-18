package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// runQuiet runs a command capturing combined output. Returns an error with
// stderr included when the command fails.
func runQuiet(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("%s: %w\n%s", name, err, msg)
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// runInteractive runs a command with stdin/stdout/stderr inherited so that
// interactive flows (e.g. rclone OAuth) can prompt the user.
func runInteractive(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// gitVersion returns the installed git major.minor version.
func gitVersion() (int, int, error) {
	out, err := exec.Command("git", "--version").Output()
	if err != nil {
		return 0, 0, fmt.Errorf("git not found in PATH")
	}
	// "git version 2.43.0"
	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) < 3 {
		return 0, 0, fmt.Errorf("unexpected git --version output: %q", string(out))
	}
	vparts := strings.SplitN(parts[2], ".", 3)
	if len(vparts) < 2 {
		return 0, 0, fmt.Errorf("cannot parse git version string: %q", parts[2])
	}
	major, err1 := strconv.Atoi(vparts[0])
	minor, err2 := strconv.Atoi(vparts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("cannot parse git version string: %q", parts[2])
	}
	return major, minor, nil
}

// rcloneVersion returns the first line of `rclone version` (e.g. "rclone v1.65.0").
func rcloneVersion() (string, error) {
	out, err := exec.Command("rclone", "version").Output()
	if err != nil {
		return "", fmt.Errorf("rclone not found in PATH")
	}
	line := strings.SplitN(string(out), "\n", 2)[0]
	return strings.TrimSpace(line), nil
}

// rsyncVersion returns the first line of `rsync --version`.
func rsyncVersion() (string, error) {
	out, err := exec.Command("rsync", "--version").Output()
	if err != nil {
		return "", fmt.Errorf("rsync not found in PATH")
	}
	line := strings.SplitN(string(out), "\n", 2)[0]
	return strings.TrimSpace(line), nil
}

// rcloneAuthGdrive runs the rclone OAuth flow for Google Drive and writes the
// token to <configDir>/rclone.conf. Adapted from vlfs.py:auth_gdrive.
func rcloneAuthGdrive(configDir string) error {
	configFile := filepath.Join(configDir, "rclone.conf")

	fmt.Println("vgit: Setting up Google Drive authentication...")
	fmt.Println()
	fmt.Println("  OAuth needs a browser. If you're on SSH, two options:")
	fmt.Println("    A) Easiest — re-SSH with port forwarding, then re-run vgit install:")
	fmt.Println("         ssh -L 53682:localhost:53682 <this-host>")
	fmt.Println("       rclone's local OAuth helper will work over the forwarded port.")
	fmt.Println("    B) Headless — when rclone asks 'Use auto config?', answer N.")
	fmt.Println("       Follow the printed instructions to run `rclone authorize` on")
	fmt.Println("       a machine with a browser, then paste the resulting token here.")
	fmt.Println()
	fmt.Println("  Access is limited to files vgit creates (drive.file scope).")
	fmt.Println()

	// Step 1: create the remote stub with scope. Pass `config_is_local=false`
	// so rclone doesn't fire its localhost OAuth helper at this point — we
	// drive OAuth explicitly in step 2.
	if err := runInteractive(
		"rclone", "config", "create",
		"gdrive",                // remote name
		"drive",                 // remote type
		"scope=drive.file",      // only access files vgit creates
		"config_is_local=false", // skip the immediate localhost OAuth attempt
		"--config", configFile,
	); err != nil {
		return fmt.Errorf("rclone config create: %w", err)
	}

	// Step 2: trigger the OAuth flow via `config reconnect`. This is the
	// documented path for headless setups — rclone itself suggests it when
	// a remote exists without a token. It runs interactively and asks
	// 'Use auto config?' so the user can pick local vs paste-back.
	fmt.Println()
	fmt.Println("vgit: completing OAuth (rclone will prompt)...")
	fmt.Println()
	if err := runInteractive(
		"rclone", "--config", configFile,
		"config", "reconnect", "gdrive:",
	); err != nil {
		return fmt.Errorf("rclone config reconnect: %w", err)
	}

	// Verify the remote actually works before declaring success.
	result, err := exec.Command("rclone", "--config", configFile, "lsd", "gdrive:").CombinedOutput()
	if err != nil {
		return fmt.Errorf("gdrive auth verification failed (did you complete the OAuth flow?):\n  %s\n\n"+
			"  To retry without losing the rest of the install, run:\n"+
			"    rclone --config %s config reconnect gdrive:",
			strings.TrimSpace(string(result)), configFile)
	}

	if err := os.Chmod(configFile, 0o600); err != nil {
		return fmt.Errorf("chmod rclone.conf: %w", err)
	}

	fmt.Println(green("vgit: Google Drive authentication complete."))
	return nil
}
