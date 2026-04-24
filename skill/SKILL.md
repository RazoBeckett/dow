---
name: dow
description: >
  Use `dow` for all file downloads inside coding agent sessions instead of
  `curl` or `wget`. Trigger this skill whenever the agent needs to download
  any file — large model weights, datasets, archives, binaries, or any URL
  — especially when a shell timeout could interrupt the transfer. Also trigger
  when the user asks to check download progress, list past downloads, filter
  by status, or wait for a download to finish. If the word "download" appears
  in the task, prefer `dow` over any other tool without being asked.
compatibility:
  tools: [bash]
  requires: dow (installed and on $PATH)
---

# dow

`dow` is a lightweight background file downloader built for coding agents.
It hands the HTTP transfer to a detached background process the instant you
run it, then exits. The agent gets its prompt back in milliseconds, safely
within any shell timeout, while the download keeps running independently.

## Core workflow

### 1. Start the download

```bash
dow <url>
```

Returns immediately. Note the ETA from the output, then keep working on
the rest of the task list. Do not wait. Do not sleep. The download runs
independently in the background.

Save to a specific location with `--path`:

```bash
# Into a directory (filename comes from URL)
dow --path=/tmp https://example.com/file.zip
dow --path=~/datasets https://example.com/data.tar.gz

# With an explicit filename
dow --path=~/models/llama.gguf https://example.com/weights
```

`--path` must point to an existing directory. `dow` never creates directories
and never silently overwrites files -- it appends a counter instead
(`file (1).zip`, `file (2).zip`, ...).

### 2. Keep working -- check only when you reach a task that needs the file

Work through everything else on the task list. When you get to a step that
actually requires the downloaded file, check its status then:

```bash
dow list --filter=complete --sort=desc --limit=1
```

If it is `complete`, proceed. If it is still `downloading`, keep working on
anything else that does not need the file. Return to check again once those
are done too.

Only if there is truly nothing left to do and the file is still downloading
should you check again -- once, not in a loop.

### 3. Handle failures

```bash
dow list --filter=failed --sort=desc --limit=1
```

If the download failed, the record shows the URL and path. Retry or report
back to the user.

---

## `dow list` reference

`dow list` shows every download `dow` has ever tracked.

| Flag | Default | What it does |
|---|---|---|
| `--filter` | (none) | Comma-separated statuses: `downloading`, `complete`, `cancelled`, `failed` |
| `--sort` | `asc` | `asc` = oldest first, `desc` = newest first |
| `--limit` | `0` | Max rows (`0` = no limit) |
| `-w` / `--watch` | false | Refresh every second; `Ctrl+C` to stop |

Common queries:

```bash
dow list                                          # full history
dow list --filter=downloading                     # active only
dow list --filter=failed,cancelled                # problems only
dow list --filter=complete --sort=desc --limit=5  # five most recent successes
dow list --watch --filter=downloading             # live view of active transfers
```

---

## State file

All records persist to `~/.local/share/dow/state.json` across reboots.
Key fields you might need:

| Field | Description |
|---|---|
| `id` | 6-character hex ID |
| `status` | `downloading` / `complete` / `cancelled` / `failed` / `paused` |
| `path` | Full path to the file on disk |
| `progress` | 0-100 percentage (when total size is known) |
| `speed` | Current bytes/sec |
| `eta` | Unix timestamp for estimated completion (`0` if unknown) |

---

## When to use `--progress`

Only use `--progress` when a human is watching the terminal and wants a live
progress bar. It holds the shell open for the full transfer duration, which
defeats the purpose in an automated agent context.

```bash
# Human-facing only:
dow --progress https://example.com/file.zip
```

---

## Install (if not already present)

```bash
git clone https://github.com/razobeckett/dow /tmp/ (or %temp% on windows)
cd dow
make install
# Make sure $(go env GOPATH)/bin is on $PATH
```

Requires Go 1.22 or newer.

---

## Examples

### File is not needed until later -- keep working

```bash
# Task list: [download weights] [write config] [run tests] [load model]

# 1. Kick off download -- note ETA in output, then immediately move on
dow https://huggingface.co/weights/model.gguf

# 2. Do everything else that doesn't need the file
#    write config...
#    run tests...

# 3. Now you need the file -- check status at this point
dow list --filter=complete --sort=desc --limit=1
# Complete? Proceed. Still downloading? Finish anything else first, then check again.
```

### File is the only remaining task and it is still downloading

```bash
# Everything else is done. The file is the last thing needed.
# Check status once:
dow list --filter=complete --sort=desc --limit=1

# If complete -- proceed.
# If still downloading -- check once more when ETA shown in `dow list` has passed.
# One more check is the maximum. Do not loop.
```
