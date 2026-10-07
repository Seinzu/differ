# Differ

A local, read-only code review tool. A Go server hosts a React + TypeScript application with GitHub-style side-by-side diffs.

## Run

Requires **Go 1.24+**, **Node.js 20.19+**, and **Git** on your PATH.

```sh
npm ci
make build
./bin/differ
```

Open **http://127.0.0.1:7331**. Started inside a Git working copy, Differ opens it; started anywhere else, it asks you to choose one. Use the **Repository** button at any time to browse your folders (Git repositories are marked) or reopen a recent repository.

The resulting binary embeds the frontend, so Node.js is only needed to build it. You can copy the binary elsewhere and run it from anywhere:

```sh
/path/to/differ -repo /path/to/working-copy
# Or set explicit refs and port:
/path/to/differ -repo . -base main -head feature -addr 127.0.0.1:8080
/path/to/differ HEAD~3 HEAD
```

## Choosing what to review

- **Branch:** pick any local branch (the checked-out branch is the default). A branch is compared with its merge base on the default branch (`main`, then `master`, then the branch `origin/HEAD` names), so only the branch's own commits appear even when `main` has moved on.
- **Latest commits:** on the default branch itself, on a branch with no commits beyond it, or in a repository without a default branch, Differ shows the latest commit. Use the stepper to widen the range; once it covers every first-parent commit, the comparison starts from the empty tree.
- **Uncommitted:** for a branch checked out in a worktree, end the diff at that worktree instead of the last commit. Tracked changes (staged or not) and untracked files are included; ignored files are not. In commit-by-commit review, the uncommitted changes appear as a final **Working tree** step. **Refresh** takes a new snapshot. With **Latest commits** set to 0, only the uncommitted changes are shown.
- **Custom refs:** compare any two commits. Full or abbreviated SHAs, branches, tags, and expressions such as `HEAD~3` are supported; `:worktree` as the head means the working tree.

### Worktrees

Differ works with repositories that use `git worktree`, including the layout where a folder holds the bare repository (for example in `.bare`, with a `.git` file pointing to it) beside one folder per worktree. Open the repository from any of its worktrees, from that folder, or from a plain bare repository such as `project.git`; the chooser marks all of them as repositories. The repository is named after the folder that holds it, not the worktree.

The branch menu shows which worktree each branch is checked out in. **Uncommitted** reads the working tree of whichever worktree has the selected branch, so you can review every worktree's in-progress changes without reopening Differ. A bare repository has no working tree of its own, and a branch that is not checked out anywhere has no uncommitted changes.

Captured conversations are shared by all worktrees of a repository. Run `differ install-hooks` in a bare repository to add the Claude Code settings to each existing worktree; worktrees added later need it too unless `.claude/settings.json` is committed.

The current selection is kept in the URL, so **Copy local link** reopens the same view. An empty or invalid working copy shows a setup/error state; no sample changes are substituted.

## Review behavior

- **Total diff:** directly compares the base snapshot to the head snapshot (`git diff BASE HEAD`). It does not substitute the merge base. Reversed and divergent comparisons work.
- **Commit by commit:** available when either commit is an ancestor of the other. Includes commits reachable from the newer commit but not the older one, in parent-before-child order. The older endpoint is excluded. Reversed comparisons still list commits oldest to newest.
- Each commit is compared with its **first parent**. Merge commits can therefore repeat changes also visible in the merged branch's commits. A root commit introduced by an unrelated-history merge is compared with an empty tree.
- Switch between **Split** (side-by-side) and **Unified** diffs in the toolbar. Unified mode shows each unchanged line once, with deletions followed by additions and separate old/new line numbers. The selected layout is remembered in your browser and applies to both total and commit-by-commit review.
- Both layouts include syntax highlighting, inline changed spans, rename detection, file statistics, and missing-final-newline indicators.
- Filter files, collapse diffs, mark files viewed, hide viewed files, ignore whitespace, wrap long lines, or show more context. Viewed state is stored in the browser per repository and resolved SHA pair.
- Stats describe the original comparison, even when whitespace is ignored. Binary files and submodule changes show metadata instead of a text preview.
- Files load as they approach the viewport. Text previews are limited to 2 MB per blob, 4 MB per patch, and 10,000 patch lines. Larger files show an explicit notice. Individual Git commands have a 30-second timeout and 32 MB output limit.

Git remains the source of truth. Differ does not check out commits, modify files, stage changes, fetch, push, or create commits. To review uncommitted changes, it stages the working tree into a temporary copy of the index and records it as a tree object; your index and files are untouched, and the unreferenced objects are removed by Git's normal garbage collection. Git external diff and text conversion commands are disabled.

The server binds only to loopback addresses and rejects cross-origin API requests and non-local hostnames. It has access to working copies readable by the user running it, and the repository chooser lists folder names (never file contents) the user can read. Local links include the working-copy path and only work on a machine with that path and a running Differ server.

## Claude Code conversations

Differ can record the prompts you give Claude Code in a repository, and Claude's responses, then show them beside the commits they produced.

```sh
differ install-hooks -repo /path/to/repo          # shared: .claude/settings.json
differ install-hooks -repo /path/to/repo -local   # personal: .claude/settings.local.json
```

This adds `UserPromptSubmit` and `Stop` hooks that run `differ hook claude`, plus a Git `post-rewrite` hook (in `core.hooksPath` if set). An existing `post-rewrite` hook is left unchanged, and the line to add is printed instead. The hooks run `differ` from your `PATH` when it is there, otherwise the absolute path of the binary you installed them with (use `-command` to choose). If Differ is missing, they do nothing, and capture errors never block Claude or Git. Commit `.claude/settings.json` to share the hooks with collaborators who also use Differ.

Each **turn** (one prompt and the full text response to it, excluding subagents) is stored in a SQLite database shared by all repositories: `$DIFFER_DB`, or `differ/conversations.db` in your user configuration directory (`~/Library/Application Support` on macOS, `~/.config` on Linux). Run the server with `-db` to read a different file. Prompts and responses are stored in plain text on your machine; nothing is sent anywhere. A turn also records:

- the branch, and `HEAD` when the prompt was submitted and when Claude finished;
- each file Claude's editing tools touched, with the blob SHA of its content when the turn ended;
- the repository's shared Git directory, so linked worktrees share history.

### Linking turns to commits

The **Conversations** tab lists the turns that happened on the branch under review or are linked to its commits. Choose a commit chip to open that commit, or use the turn count in commit-by-commit review to see a commit's turns. Rebases and amends change commit SHAs and committer dates, so links use evidence that survives them, strongest first:

1. **Committed during the turn** (green chip): commits made or amended between the turn's starting and ending `HEAD`.
2. **Same content** (blue chip): the commit leaves a file the turn edited with exactly the content the turn ended with.
3. **HEAD** (amber chip): the commit `HEAD` pointed at while the turn ran, so a conversation links to the point in the branch where it happened, even when it changed nothing (asking about a commit, say).
4. **Same files, later** (dashed chip): when neither of the first two matches, the earliest commit *authored* after the turn that changes a file the turn edited.

Commits rewritten since a turn are followed in two ways. The `post-rewrite` hook records each amend and rebase as old and new SHAs. Without it, a rewritten commit is recognized by its author, author date, and subject, which `git commit --amend --no-edit` and rebases keep (rewording the message needs the hook). A turn on the reviewed branch whose commit is gone entirely, for example after a reset, still appears, marked **on _branch_**. Uncommitted changes are linked the same way when the working tree ends the comparison. Turns from other branches are available through **Include unlinked turns**.

## GitHub releases

The **Release macOS (Apple Silicon)** workflow runs only through `workflow_dispatch`; pushes and tags do not trigger it. Once the workflow is on the default branch, open **Actions → Release macOS (Apple Silicon) → Run workflow**, select the branch to build, and enter a new tag such as `v0.1.0`. Optionally mark it as a prerelease.

The workflow builds and tests the frontend and Go server, cross-compiles a `darwin/arm64` binary with the frontend embedded, and creates a release tagging the exact selected commit. Existing tags are rejected. It uses GitHub's built-in token with `contents: write`; no additional secret is needed.

Release assets:

- `differ-darwin-arm64.tar.gz` — the executable and README, for M-series Macs.
- `differ-darwin-arm64.tar.gz.sha256` — the archive's SHA-256 checksum.

Download both assets into the same directory, then run:

```sh
shasum -a 256 -c differ-darwin-arm64.tar.gz.sha256
tar -xzf differ-darwin-arm64.tar.gz
./differ -repo /path/to/working-copy BASE_SHA HEAD_SHA
```

Git must be installed on the Mac; Node.js and Go are not needed to run the release binary. The binary is not Developer ID signed or notarized.

## Development

Build the frontend once before running Go (it is embedded at compile time):

```sh
npm ci
npm run build
go run ./cmd/differ -repo /path/to/working-copy
```

For frontend hot reload, run `npm run dev` in a second terminal and use the Vite URL. Vite proxies `/api` to the Go server on port 7331. Rebuild/restart Go after frontend changes when using the Go-served URL instead of Vite.

```sh
make test
go vet ./...
```

Go integration tests create temporary repositories and exercise ancestry, reverse and divergent comparisons, merges, root commits, renames, binary files, odd filenames, whitespace, preview limits, branch ranges, working-tree snapshots, conversation capture, and links that survive rebases. Frontend tests cover split-diff alignment and inline changed spans.

The **CI** workflow runs on pull requests and pushes to `main`. It checks Prettier and Go formatting, TypeScript types, frontend tests, `go vet`, Go tests with the race detector, and the final application build. The frontend is built before Go checks because Go embeds its output. Superseded runs for the same pull request or branch are cancelled.

Run `npm run format:check` to check frontend and workflow formatting locally, or `npm run format` to fix it. For Go formatting, use `gofmt -w cmd internal web/embed.go`. Run `go test -race ./...` to match CI's race-enabled Go tests after building the frontend.

## Layout

```text
cmd/differ/       CLI and HTTP server lifecycle
internal/git/     Read-only Git operations and integration tests
internal/server/  JSON API, local-request boundary, static hosting
web/src/          React UI, diff renderer, styles, parser tests
web/embed.go      Built frontend embedded into the Go binary
```

API: `GET /api/config`, `GET /api/compare?repo=…&base=…&head=…`, and `GET /api/file?repo=…&base=…&head=…&path=…&context=3&whitespace=show`.
