# vgit

Self-hosted git server with bundle-based backup to Google Drive and SSH targets.
Bare repos configured for partial-clone and bitmap indexes.

## Install

    go install github.com/hypernewbie/vgit@latest

Requires `git` >= 2.22, `rclone` (for gdrive backups), `rsync` (for ssh backups).

## Commands

### vgit install [flags]

One-time host setup. Creates `~/vgit/` (mode 700) with `repos/`, `bundles/`, and
`config/` subdirs; verifies `git` and `rclone` are present; optionally runs the
rclone OAuth flow for Google Drive.

The gdrive auth flow uses `config_is_local=false` (no local OAuth helper port;
prints a URL you open in any browser — works over SSH with no port forwarding)
and `scope=drive.file` (rclone can read and manage only files it creates;
cannot see the rest of your Drive).

Flags:

    --dir string    Installation directory (default $HOME/vgit)
    --no-gdrive     Skip Google Drive setup
    --yes           Non-interactive; skip prompts (implies --no-gdrive)
    --force         Proceed even if the directory is non-empty

Rerunning against a non-empty directory errors without `--force`. With
`--force`, existing `vgit.toml` is loaded and merged, so configured remotes
are preserved.

### vgit init <name> [flags]

Creates a bare repo at `~/vgit/repos/<name>.git`, configures it for
partial-clone and bitmap indexes, and starts `git maintenance` (systemd user
timers for auto-gc).

Name must match `^[A-Za-z0-9][A-Za-z0-9_-]*$`.

Config keys set:

    uploadpack.allowFilter        = true
    uploadpack.allowAnySHA1InWant = true
    gc.writeBitmaps               = true
    pack.useBitmaps               = true
    core.commitGraph              = true
    gc.writeCommitGraph           = true

`receive.denyNonFastForwards` is intentionally NOT set. Enable later with:

    git -C ~/vgit/repos/<name>.git config receive.denyNonFastForwards true

Flags:

    --description string   Repository description (written to .git/description)

### vgit backup <repo> <target>

Bundles `<repo>` with `git bundle create --all`, verifies the bundle, and
uploads it to `<target>`. Each backup overwrites the previous bundle for that
repo. The local copy stays at `~/vgit/bundles/<repo>.bundle` as a staging
artefact.

Target syntax:

    gdrive:<path>             Upload via rclone (requires gdrive set up by `vgit install`)
    ssh:<user@host:path>      Upload via rsync over SSH

Examples:

    vgit backup a1 gdrive:vgit-backups/
    vgit backup a1 ssh:hypernewbie@hyperion:~/vgit_backup/

Errors fail fast and non-zero:

- Unknown target prefix
- Repo missing at `~/vgit/repos/<name>.git`
- gdrive target requested but no gdrive remote in `vgit.toml`
- `git bundle verify` failure
- Underlying rclone or rsync failure

Run from cron for scheduled backups:

    0 3 * * 0  /home/hypernewbie/go/bin/vgit backup a1 gdrive:vgit-backups/

## Cloning

From another machine:

    git clone --filter=blob:none --sparse charon:vgit/repos/a1.git

Or full clone:

    git clone charon:vgit/repos/a1.git

The `--filter=blob:none --sparse` form requires `uploadpack.allowFilter` on the
bare repo; `vgit init` sets it. Without it, clients silently fall back to full
clones.

## Files

    ~/vgit/
      repos/                Bare repos (created by `vgit init`)
      bundles/              Local bundle staging (latest snapshot per repo)
      config/
        vgit.toml           vgit's own config: install dir, configured remotes
        rclone.conf         rclone config + gdrive token (mode 600)

`~/vgit/` is mode 700. `rclone.conf` is mode 600. `vgit.toml` contains no
secrets.

## Environment

    VGIT_DIR     Override install-dir lookup for `vgit init` and `vgit backup`
    NO_COLOR     Disable ANSI colour output (also `--no-colour`)

## Notes

- vgit's rclone config (`~/vgit/config/rclone.conf`) is isolated from the global
  `~/.config/rclone/rclone.conf`. The `--config` flag is passed on every rclone
  invocation; no other rclone state is touched.
- `gdrive:` paths in `vgit backup` are passed through verbatim to rclone, so
  the rclone remote must be named `gdrive` (which it is when configured by
  `vgit install`).
- For SSH targets, `~` in the destination path is expanded by the remote shell
  (rsync's normal behaviour).

## Licence

MIT. Copyright (c) 2026 UAA Software / hypernewbie.
