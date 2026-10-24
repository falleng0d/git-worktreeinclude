# git-worktreeinclude

`git-worktreeinclude` safely applies ignored files listed in `.worktreeinclude` from a source worktree to the current worktree.

## Quickstart

### Download a binary

Download a release archive from [GitHub Releases](https://github.com/falleng0d/git-worktreeinclude/releases). Binaries are built for macOS, Linux, and Windows on x86-64 and ARM64. Extract the archive and put the executable on your `PATH`. Each release includes `checksums.txt` for SHA-256 verification.

For the latest development build, open the **Build** workflow under GitHub Actions, select a successful run, and download its artifact. These builds run on pushes to `main` or manual dispatch, never on pull requests. Each build verifies archive checksums and runs every binary on its matching operating system and architecture. A GitHub login is required to download workflow artifacts. Fork pull requests also skip CI jobs.

### Install with Go

```sh
go install github.com/falleng0d/git-worktreeinclude/cmd/git-worktreeinclude@latest
```

### Build

```sh
go build -o git-worktreeinclude ./cmd/git-worktreeinclude
```

To use the Git extension form (`git worktreeinclude ...`), place `git-worktreeinclude` on your `PATH`.

Subcommands are explicit. Use `git-worktreeinclude apply ...`.

### Apply

```sh
git-worktreeinclude apply
```

Or via Git extension:

```sh
git worktreeinclude apply
```

## `.worktreeinclude` and `.worktreelink` semantics

- Place `.worktreeinclude` at the source worktree root (by default, this is typically the main worktree selected by `--from auto`).
- Format is gitignore-compatible (`#` comments, blank lines, `!` negation, `/` anchors, `**`, etc.).
- `.worktreeinclude` may be tracked, untracked, or ignored; if the file exists in the source worktree, it is used.
- Actual sync target is the intersection of:
  - Paths matching `.worktreeinclude`
  - Paths Git classifies as ignored

Tracked files are never copied or linked, even if listed in `.worktreeinclude`.

Optional link manifest:

- Place `.worktreelink` at the source worktree root to symlink matching files or directories instead of copying them.
- `.worktreelink` uses the same gitignore-compatible pattern syntax.
- A path is linked when it matches `.worktreelink`, even if it is not listed in `.worktreeinclude`.
- If a path matches both `.worktreeinclude` and `.worktreelink`, the link action wins.
- If `.worktreelink` is missing, all matched files are copied as before.

Example for a typical app repo with local env, editor settings, and tool-specific cache:

```gitignore
.env
.env.local
!.env.example

.vscode/settings.json
.mise.local.toml
turbo/.cache/
```

Example `.worktreelink`:

```gitignore
.env
.mise.local.toml
```

## Commands

### `git-worktreeinclude --version`

Print the installed version.

```sh
git-worktreeinclude --version
git-worktreeinclude -v
```

### `git-worktreeinclude apply`

Uses the current worktree as target and copies from source worktree.

```sh
git-worktreeinclude apply [--from auto|<path>] [--include <path>] [--dry-run] [--force] [--force-link] [--json] [--quiet] [--verbose]
```

- `--from`: `auto` (default) chooses the first non-bare worktree from `git worktree list --porcelain -z` (typically the main worktree)
- `--include`: include file path (default: `.worktreeinclude`)
  - relative path: resolved from source worktree root only
  - absolute path: must be inside source worktree root
- `--dry-run`: plan only, make no changes
  - use `--dry-run --verbose` when you want diagnostics about source/target selection, include file resolution, and planned actions
  - in dry-run mode, the human-readable summary uses `copy_planned=` and `link_planned=` instead of `copied=` and `linked=`, and the JSON summary uses `"copy_planned"` / `"link_planned"` instead of `"copied"` / `"linked"`
- `--force`: overwrite differing target files
- `--force-link`: overwrite differing linked files only
- `--json`: emit a single JSON object to stdout
- `--quiet`: suppress human-readable output
- `--verbose`: print additional details

Safe defaults:

- Never touches tracked files
- Never deletes files
- Never overwrites by default (differences become conflicts, exit code `3`)
- Missing source `.worktreeinclude` is a no-op success (exit code `0`)

### JetBrains IDE projects

When `.idea/workspace.xml` is copied (not linked), `apply` gives the target worktree its own JetBrains project ID. JetBrains IDEs store open tabs, tool window layout, the project tree and window state in `<config>/<product>/workspace/<ProjectId>.xml`. Worktrees that share an ID overwrite each other's state.

For each IDE config folder (`JetBrains` and `Google` under the user config folder, such as `~/Library/Application Support`, `~/.config` or `%APPDATA%`) that has state for the source project ID, `apply`:

- generates a new ID and writes it to the target `.idea/workspace.xml`
- copies the source state to `workspace/<new id>.xml`
- rewrites source root paths to the target root in both files
- adds the target to `options/recentProjects.xml` with the source window size and position, if the target is not listed yet

A running IDE rewrites `recentProjects.xml` from memory, so the window position only carries over when the IDE is closed during `apply`. The step is skipped when the target already has its own ID. A `workspace.xml` that differs from the source only by its ID and root path counts as `same`. With `--force`, the target keeps its existing ID and its state is replaced by the source state. If the target still has the source ID, the step runs even when `workspace.xml` is a conflict. This also fixes worktrees created before this feature.

## JSON output

`apply --json` emits a single JSON object to stdout.

Normal execution (`apply --json`):

```json
{
  "dry_run": false,
  "from": "/abs/path/source",
  "to": "/abs/path/target",
  "include_file": ".worktreeinclude",
  "summary": {
    "matched": 12,
    "copied": 6,
    "linked": 2,
    "skipped_same": 3,
    "skipped_missing_src": 1,
    "conflicts": 0,
    "errors": 0
  },
  "actions": [
    {"op": "copy", "path": ".env", "status": "done"},
    {"op": "link", "path": ".mise.local.toml", "status": "done"},
    {"op": "skip", "path": ".env.local", "status": "same"},
    {"op": "conflict", "path": ".vscode/settings.json", "status": "diff"}
  ]
}
```

Dry-run mode (`apply --dry-run --json`):

```json
{
  "dry_run": true,
  "from": "/abs/path/source",
  "to": "/abs/path/target",
  "include_file": ".worktreeinclude",
  "summary": {
    "matched": 12,
    "copy_planned": 6,
    "link_planned": 2,
    "skipped_same": 3,
    "skipped_missing_src": 1,
    "conflicts": 0,
    "errors": 0
  },
  "actions": [
    {"op": "copy", "path": ".env", "status": "planned"},
    {"op": "link", "path": ".mise.local.toml", "status": "planned"},
    {"op": "skip", "path": ".env.local", "status": "same"},
    {"op": "conflict", "path": ".vscode/settings.json", "status": "diff"}
  ]
}
```

- `"dry_run": true` indicates no files were written
- In dry-run mode `"copy_planned"` and `"link_planned"` are used instead of `"copied"` and `"linked"` in the summary
- `path` is repo-root relative and slash-separated
- File contents and secrets are never output

## External tool integration

Run this immediately after creating a worktree:

```sh
git worktree add <path> -b <branch>
git -C <path> worktreeinclude apply --json
```

- Evaluate success by exit code
- Use JSON `summary` and `actions` for details

## Exit codes

- `0`: success
- `1`: internal error
- `2`: argument/usage error
- `3`: conflict (`apply`) or unknown help topic
- `4`: environment/prerequisite error

## Troubleshooting

- `not inside a git repository`: run from a Git repository
- `source and target are not from the same repository`: verify `--from` points to the same repo worktree
- conflict exit: use `--force` for copies, `--force-link` for links, or resolve target differences first
- no-op due to missing include: verify `.worktreeinclude` exists in the source worktree selected by `--from`
- if include exists only in target: copy that file to source worktree (or run with a different `--from`)

## Development

```sh
make fmt
make check-fmt
make vet
make lint
make test
make test-race
make ci
```

CI runs on pull requests and pushes to `main` via GitHub Actions.`golangci-lint` is used with its default configuration (no `.golangci.yml`).`make lint` installs a pinned `golangci-lint` binary into `.cache/bin` on first run, so the first run needs network access to fetch the tool.

## License

MIT. See [LICENSE](LICENSE).
