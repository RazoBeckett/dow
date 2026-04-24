# dow

A lightweight file downloader built for coding agents.

## The problem

Coding agents — Claude Code, OpenCode, and others — execute shell commands through a harness that enforces a timeout on every command. When an agent tries to download a large file with `curl` or `wget`, the harness kills the command once the timeout expires, the download dies mid-transfer, and the task fails.

## The solution

`dow` hands the download off to a background process the moment you run it, then immediately exits. The agent gets its prompt back in milliseconds, well within any timeout, while the download continues running independently in the background. No timeout can touch it.

```
$ dow https://example.com/large-model-weights.tar.gz

id abc123
name large-model-weights.tar.gz
status downloading...(15%) 1.8 MB/s
eta 5m
path /home/user/Downloads/large-model-weights.tar.gz
```

The agent can check progress at any time with `dow list`, wait for completion by polling `dow list --filter=downloading`, and move on once the status flips to `complete`.

---

## Install

**From source** (requires Go 1.22+):

```bash
git clone https://github.com/razobeckett/dow
cd dow
make install
```

This installs `dow` to `$(go env GOPATH)/bin`. Make sure that's on your `$PATH`.

**Build only** (without installing):

```bash
make build
# produces ./dow
```

---

## Usage

### Download a file

```bash
dow <url>
```

Starts the download in the background, prints a one-line status snapshot, and returns immediately.

```bash
dow https://example.com/file.zip
```

### Download to a specific location

Use `--path` to control where the file lands. It accepts a directory, a relative or absolute file path, or `~`-prefixed paths.

```bash
# Save to a directory (filename comes from the URL):
dow --path=/tmp https://example.com/file.zip
dow --path=. https://example.com/file.zip
dow --path=~/projects/assets https://example.com/file.zip

# Save with a specific filename (last component has an extension):
dow --path=cat.mkv https://example.com/videos/cat-on-table.mkv
dow --path=~/photos/cat.png https://example.com/photo
dow --path=/tmp/archive.tar.gz https://example.com/release.tar.gz
```

If the path is a directory it must already exist — `dow` will not create it. If a file with the same name already exists, `dow` appends a counter (`file (1).zip`, `file (2).zip`, …) so nothing is ever silently overwritten.

### Download with live progress

```bash
dow --progress <url>
```

Holds the terminal and shows a live progress bar. Use this when you're running `dow` manually and want to watch the transfer.

```bash
dow --progress https://example.com/file.zip
```

### List all downloads

```bash
dow list
```

Shows every download dow has ever tracked — in progress, complete, cancelled, and failed.

#### Filter by status

```bash
dow list --filter=downloading
dow list --filter=complete
dow list --filter=failed
dow list --filter=downloading,cancelled   # multiple statuses
```

#### Sort and limit

```bash
dow list --sort=desc                      # newest first
dow list --filter=failed --sort=asc --limit=5   # five oldest failures
dow list --filter=cancelled --sort=desc --limit=1  # most recently cancelled
```

#### Watch mode

```bash
dow list -w
dow list --watch --filter=downloading
```

Refreshes the list every second in-place. Useful when you want to keep an eye on active downloads without manually re-running the command. Press `Ctrl+C` to stop.

---

## How it works

When you run `dow <url>`:

1. `dow` validates the URL, determines the output path under `~/Downloads/`, and registers a record in its state store (`~/.local/share/dow/state.json`).
2. It re-executes itself as a detached background process (new session via `setsid`) that performs the actual HTTP transfer.
3. The foreground process prints the status snapshot and exits.

The background worker updates the state file every 500 ms with current progress and speed. It keeps running regardless of what happens to the terminal or the parent process that spawned it.

By default downloads land in `~/Downloads/`. Use `--path` to send the file somewhere else — either a directory or an explicit file path (see [Download to a specific location](#download-to-a-specific-location)). If a file with the same name already exists, `dow` appends a counter — `file (1).zip`, `file (2).zip`, etc. — so nothing is ever silently overwritten.

State is stored at `~/.local/share/dow/state.json` and persists across reboots. `dow list` always gives you a full history.

---

## For coding agents

Install the `dow` skill directly for your coding agents.

```bash
npx skills add https://github.com/razoBeckett/dow
```

Or add the following to your `CLAUDE.md` (or equivalent agent instructions file) to teach your agent to use `dow` instead of `curl`/`wget` for file downloads:

```markdown
## Downloading files

Use `dow` for all file downloads instead of `curl` or `wget`.

`dow` starts the download in the background and exits immediately, so downloads
are never interrupted by command timeouts — regardless of file size.

### Workflow

Start a download:
\```
dow <url>
\```

Wait for it to finish (poll until no results):
\```
dow list --filter=downloading
\```

Verify success:
\```
dow list --filter=complete --sort=desc --limit=1
\```

If something went wrong:
\```
dow list --filter=failed --sort=desc --limit=1
\```
```

### Typical agent workflow

```bash
# 1. Start the download — returns immediately
dow https://example.com/dataset.tar.gz

# 2. Check if it's still going
dow list --filter=downloading

# 3. Once the above returns nothing, confirm it completed
dow list --filter=complete --sort=desc --limit=1

# 4. The file is in ~/Downloads/ — proceed with the task
```

---

## State file

All download records are persisted to `~/.local/share/dow/state.json`. The schema is straightforward:

| Field        | Description                              |
|--------------|------------------------------------------|
| `id`         | 6-character hex ID                       |
| `url`        | Original URL                             |
| `filename`   | Filename on disk                         |
| `path`       | Full path to the downloaded file         |
| `status`     | `downloading` · `complete` · `cancelled` · `failed` · `paused` |
| `progress`   | 0–100 (percentage, when size is known)   |
| `speed`      | Current transfer speed in bytes/sec      |
| `size`       | Total file size in bytes (`-1` if unknown) |
| `downloaded` | Bytes received so far                    |
| `eta`        | Unix timestamp for estimated completion (`0` if unknown) |
| `created_at` | When the download was registered         |
| `updated_at` | Last state update                        |

---

## Flags reference

### `dow`

| Flag         | Description                                    |
|--------------|------------------------------------------------|
| `--path`     | Destination directory or file path (default: `~/Downloads`); directories must already exist |
| `--progress` | Hold the terminal and show live progress       |
| `--version`  | Print version and commit                       |

### `dow list`

| Flag              | Default | Description                                                        |
|-------------------|---------|--------------------------------------------------------------------|
| `--filter`        | —       | Comma-separated statuses: `downloading`, `complete`, `cancelled`, `failed` |
| `--sort`          | `asc`   | `asc` (oldest first) or `desc` (newest first)                      |
| `--limit`         | `0`     | Max rows to show (`0` = no limit)                                  |
| `-w` / `--watch`  | `false` | Refresh every second · `Ctrl+C` to stop                           |
