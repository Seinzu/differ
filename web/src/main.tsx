import React, { useEffect, useMemo, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  ArrowLeft,
  ArrowRight,
  ArrowLeftRight,
  Check,
  ChevronDown,
  ChevronRight,
  CircleDot,
  Copy,
  FileCode2,
  FileDiff,
  FolderGit2,
  GitBranch,
  GitCommitHorizontal,
  GitCompareArrows,
  LoaderCircle,
  Minus,
  PanelLeftClose,
  PanelLeftOpen,
  Plus,
  RefreshCw,
  Search,
  Settings2,
  X,
} from "lucide-react";
import hljs from "highlight.js/lib/common";
import type { ChangedFile, Comparison, Config, Patch, RepoInfo } from "./types";
import { RepoPicker, rememberRepository } from "./RepoPicker";
import { api } from "./api";
import { changedSpan, parsePatch, toUnifiedRows, type Line } from "./diff";
import "./style.css";

type DiffLayout = "split" | "unified";

const short = (sha: string) =>
  sha === ":empty" ? "empty tree" : sha.slice(0, 7);
// Working-tree snapshots are tree SHAs; name them instead of showing a hash.
const headLabel = (comparison: Comparison, worktreeTree?: string) =>
  comparison.worktree || comparison.head === worktreeTree
    ? "working tree"
    : short(comparison.head);
const statusNames: Record<string, string> = {
  A: "Added",
  D: "Deleted",
  M: "Modified",
  R: "Renamed",
  T: "Type changed",
  C: "Copied",
};
function Stats({
  additions,
  deletions,
}: {
  additions: number;
  deletions: number;
}) {
  return (
    <span className="stats">
      <span className="added">+{additions.toLocaleString()}</span>
      <span className="deleted">−{deletions.toLocaleString()}</span>
    </span>
  );
}
interface Selection {
  repository: string;
  branch: string;
  count: number;
  uncommitted: boolean;
  // Explicit refs replace the branch-based range when both are set.
  base: string;
  head: string;
}
const isCustom = (selection: Selection) => !!(selection.base && selection.head);

function App() {
  const [selection, setSelection] = useState<Selection>({
    repository: "",
    branch: "",
    count: 1,
    uncommitted: false,
    base: "",
    head: "",
  });
  const [refs, setRefs] = useState({ base: "", head: "" });
  const [customRefs, setCustomRefs] = useState(false);
  const [info, setInfo] = useState<RepoInfo>();
  const [pickerOpen, setPickerOpen] = useState(false);
  const [comparison, setComparison] = useState<Comparison>();
  const [viewComparison, setViewComparison] = useState<Comparison>();
  const [mode, setMode] = useState<"total" | "commits">("total");
  const [commitIndex, setCommitIndex] = useState(0);
  const [loading, setLoading] = useState(false);
  const [commitLoading, setCommitLoading] = useState(false);
  const [error, setError] = useState("");
  const [filter, setFilter] = useState("");
  const [sidebarCollapsed, setSidebarCollapsed] = useState(() => {
    try {
      return localStorage.getItem("differ:sidebar-collapsed") === "true";
    } catch {
      return false;
    }
  });
  const [context, setContext] = useState("3");
  const [ignoreWhitespace, setIgnoreWhitespace] = useState(false);
  const [wrap, setWrap] = useState(false);
  const [diffLayout, setDiffLayout] = useState<DiffLayout>(() => {
    try {
      return localStorage.getItem("differ:diff-layout") === "unified"
        ? "unified"
        : "split";
    } catch {
      return "split";
    }
  });
  const [reviewed, setReviewed] = useState<Set<string>>(new Set());
  const [hideViewed, setHideViewed] = useState(false);
  const [activePath, setActivePath] = useState("");
  const [copied, setCopied] = useState(false);
  const request = useRef<AbortController | undefined>(undefined);
  const commitRequest = useRef<AbortController | undefined>(undefined);
  const active = mode === "total" ? comparison : viewComparison;
  const activeCommit = comparison?.commits[commitIndex];
  const reviewKey = active
    ? `differ:review:${active.repository}:${active.base}:${active.head}`
    : "";
  const range = comparison?.range;

  useEffect(() => {
    try {
      localStorage.setItem(
        "differ:sidebar-collapsed",
        String(sidebarCollapsed),
      );
    } catch {
      // The toggle still works when browser storage is unavailable.
    }
  }, [sidebarCollapsed]);

  useEffect(() => {
    try {
      localStorage.setItem("differ:diff-layout", diffLayout);
    } catch {
      // Layout selection still works when browser storage is unavailable.
    }
  }, [diffLayout]);

  async function load(next: Selection) {
    request.current?.abort();
    commitRequest.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    setLoading(true);
    setError("");
    try {
      const repoInfo = await api<RepoInfo>(
        "repo",
        { repo: next.repository },
        controller.signal,
      );
      const custom = isCustom(next);
      const result = await api<Comparison>(
        "compare",
        custom
          ? { repo: repoInfo.repository, base: next.base, head: next.head }
          : {
              repo: repoInfo.repository,
              branch: next.branch,
              count: String(next.count),
              ...(next.uncommitted ? { uncommitted: "1" } : {}),
            },
        controller.signal,
      );
      const applied = {
        ...next,
        repository: result.repository,
        branch: result.range?.branch ?? next.branch,
      };
      setInfo(repoInfo);
      setComparison(result);
      setViewComparison(undefined);
      setMode("total");
      setCommitIndex(0);
      setFilter("");
      setActivePath("");
      setSelection(applied);
      setCustomRefs(custom);
      setRefs(
        custom
          ? { base: next.base, head: next.head }
          : { base: short(result.base), head: applied.branch || "HEAD" },
      );
      rememberRepository(result.repository);
      const query = new URLSearchParams({ repo: result.repository });
      if (custom) {
        query.set("base", next.base);
        query.set("head", next.head);
      } else {
        if (next.branch) query.set("branch", next.branch);
        if (result.range?.mode === "recent")
          query.set("count", String(next.count));
        if (next.uncommitted) query.set("uncommitted", "1");
      }
      window.history.replaceState(null, "", `?${query}`);
    } catch (e) {
      if (!controller.signal.aborted) setError((e as Error).message);
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
  }
  const chooseBranch = (branch: string) =>
    void load({
      ...selection,
      branch,
      count: 1,
      // Uncommitted changes only exist on the checked-out branch.
      uncommitted: selection.uncommitted && branch === info?.currentBranch,
      base: "",
      head: "",
    });
  const onCheckedOutBranch = !!info && selection.branch === info.currentBranch;
  const minCount = selection.uncommitted ? 0 : 1;
  const changeCount = (count: number) =>
    void load({
      ...selection,
      count: Math.max(minCount, Math.min(count, range?.available ?? count)),
    });
  useEffect(() => {
    const controller = new AbortController();
    api<Config>("config", {}, controller.signal)
      .then((config) => {
        const query = new URLSearchParams(location.search);
        const count = Number(query.get("count") ?? "1");
        const next: Selection = {
          repository: query.get("repo") ?? config.repository,
          branch: query.get("branch") ?? "",
          count: Number.isInteger(count) && count >= 0 ? count : 1,
          uncommitted: query.get("uncommitted") === "1",
          base: query.get("base") ?? config.base,
          head: query.get("head") ?? config.head,
        };
        setSelection(next);
        if (next.repository) void load(next);
        else setPickerOpen(true);
      })
      .catch((e) => {
        if (!controller.signal.aborted) setError(e.message);
      });
    return () => {
      controller.abort();
      request.current?.abort();
      commitRequest.current?.abort();
    };
  }, []);
  useEffect(() => {
    if (mode !== "commits" || !comparison || !activeCommit) return;
    const controller = new AbortController();
    commitRequest.current = controller;
    setCommitLoading(true);
    setViewComparison(undefined);
    setError("");
    setActivePath("");
    api<Comparison>(
      "compare",
      {
        repo: comparison.repository,
        base: activeCommit.parents[0] || ":empty",
        head: activeCommit.sha,
      },
      controller.signal,
    )
      .then(setViewComparison)
      .catch((e) => {
        if (!controller.signal.aborted) setError(e.message);
      })
      .finally(() => {
        if (!controller.signal.aborted) setCommitLoading(false);
      });
    return () => controller.abort();
  }, [mode, comparison, activeCommit]);
  useEffect(() => {
    try {
      const saved = JSON.parse(localStorage.getItem(reviewKey) || "[]");
      setReviewed(new Set(Array.isArray(saved) ? saved : []));
    } catch {
      setReviewed(new Set());
    }
  }, [reviewKey]);
  function toggleViewed(path: string) {
    setReviewed((previous) => {
      const next = new Set(previous);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      try {
        localStorage.setItem(reviewKey, JSON.stringify([...next]));
      } catch {
        /* Review still works when storage is disabled. */
      }
      return next;
    });
  }
  const files = active?.files ?? [];
  const visibleFiles = files.filter(
    (file) =>
      file.path.toLowerCase().includes(filter.toLowerCase()) &&
      (!hideViewed || !reviewed.has(file.path)),
  );
  const reviewedCount = files.filter((file) => reviewed.has(file.path)).length;
  const canBrowseCommits = !!comparison?.commits.length;
  function navigateTo(path: string) {
    setActivePath(path);
    document
      .getElementById(`file-${encodeURIComponent(path)}`)
      ?.scrollIntoView({ behavior: "smooth", block: "start" });
  }
  async function copyLink() {
    try {
      await navigator.clipboard.writeText(location.href);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      setError(
        "Could not copy the link. You can copy it from the address bar.",
      );
    }
  }

  return (
    <>
      <header className="app-header">
        <a className="brand" href={location.pathname} aria-label="Differ home">
          <span className="brand-icon">
            <GitCompareArrows size={21} />
          </span>
          differ<span className="local-badge">LOCAL</span>
        </a>
        <div className="header-divider" />
        <span className="header-repo">
          <FolderGit2 size={16} />
          {comparison?.name || "Your working copy"}
        </span>
        <span className="header-right">
          <span className="connection-dot" />
          Local code review
        </span>
      </header>
      <main>
        <section className="comparison-header">
          <div className="title-row">
            <div>
              <div className="eyebrow">A FRESH PAIR OF EYES</div>
              <h1>
                Review your changes<span className="title-dot">.</span>
              </h1>
              <p>
                From the first commit to the final diff. All on your machine.
              </p>
            </div>
            <button className="button subtle share-button" onClick={copyLink}>
              {copied ? <Check size={15} /> : <Copy size={15} />}
              {copied ? "Copied" : "Copy local link"}
            </button>
          </div>
          <form
            className="compare-form"
            onSubmit={(e) => {
              e.preventDefault();
              void load(
                customRefs
                  ? { ...selection, base: refs.base, head: refs.head }
                  : { ...selection, base: "", head: "" },
              );
            }}
          >
            <div className="repo-input">
              <span className="field-label">Repository</span>
              <button
                type="button"
                className="input-wrap repo-button"
                title={selection.repository || "Choose a repository"}
                onClick={() => setPickerOpen(true)}
              >
                <FolderGit2 size={16} />
                <span className="repo-name">
                  {info?.name || selection.repository || "Choose a repository…"}
                </span>
                {info && <small>{"\u200e" + info.repository}</small>}
                <ChevronDown size={14} />
              </button>
            </div>
            {customRefs ? (
              <>
                <label className="ref-input">
                  <span>Base</span>
                  <div className="input-wrap">
                    <GitBranch size={15} />
                    <input
                      required
                      aria-label="Base commit"
                      placeholder="Commit SHA or ref"
                      value={refs.base}
                      onChange={(e) =>
                        setRefs({ ...refs, base: e.target.value })
                      }
                      spellCheck={false}
                    />
                  </div>
                </label>
                <button
                  type="button"
                  className="icon-button swap"
                  title="Swap base and head"
                  aria-label="Swap base and head"
                  onClick={() => setRefs({ base: refs.head, head: refs.base })}
                >
                  <ArrowLeftRight size={16} />
                </button>
                <label className="ref-input">
                  <span>Head</span>
                  <div className="input-wrap">
                    <GitBranch size={15} />
                    <input
                      required
                      aria-label="Head commit"
                      placeholder="Commit SHA or ref"
                      value={refs.head}
                      onChange={(e) =>
                        setRefs({ ...refs, head: e.target.value })
                      }
                      spellCheck={false}
                    />
                  </div>
                </label>
              </>
            ) : (
              <>
                <label className="ref-input branch-input">
                  <span>Branch</span>
                  <div className="input-wrap">
                    <GitBranch size={15} />
                    <select
                      aria-label="Branch"
                      value={selection.branch}
                      disabled={!info || loading}
                      onChange={(e) => chooseBranch(e.target.value)}
                    >
                      {(!info || info.detached) && (
                        <option value="">
                          {info ? "Detached HEAD" : "No repository"}
                        </option>
                      )}
                      {info?.branches.map((branch) => (
                        <option key={branch.name} value={branch.name}>
                          {branch.name}
                          {branch.current ? " (checked out)" : ""}
                        </option>
                      ))}
                    </select>
                  </div>
                </label>
                {range?.mode === "recent" && (
                  <div className="count-input">
                    <span className="field-label">Latest commits</span>
                    <div className="input-wrap stepper">
                      <button
                        type="button"
                        className="icon-button"
                        aria-label="Fewer commits"
                        disabled={loading || selection.count <= minCount}
                        onClick={() => changeCount(selection.count - 1)}
                      >
                        <Minus size={14} />
                      </button>
                      <input
                        aria-label="Number of latest commits"
                        type="number"
                        min={minCount}
                        max={range.available}
                        value={selection.count}
                        onChange={(e) =>
                          setSelection({
                            ...selection,
                            count: Math.max(0, Number(e.target.value) || 0),
                          })
                        }
                      />
                      <button
                        type="button"
                        className="icon-button"
                        aria-label="More commits"
                        disabled={loading || selection.count >= range.available}
                        onClick={() => changeCount(selection.count + 1)}
                      >
                        <Plus size={14} />
                      </button>
                    </div>
                  </div>
                )}
              </>
            )}
            {!customRefs && (
              <label
                className={`uncommitted-toggle${onCheckedOutBranch ? "" : " unavailable"}`}
                title={
                  onCheckedOutBranch
                    ? "End the diff at your working tree instead of the last commit"
                    : "Uncommitted changes belong to the checked-out branch"
                }
              >
                <input
                  type="checkbox"
                  checked={selection.uncommitted && onCheckedOutBranch}
                  disabled={!onCheckedOutBranch || loading}
                  onChange={(e) =>
                    void load({
                      ...selection,
                      uncommitted: e.target.checked,
                      count: Math.max(
                        e.target.checked ? 0 : 1,
                        selection.count,
                      ),
                    })
                  }
                />
                <span>
                  Uncommitted
                  {onCheckedOutBranch && info?.dirty && (
                    <span
                      className="dirty-dot"
                      title="Has uncommitted changes"
                    />
                  )}
                </span>
              </label>
            )}
            <button
              type="button"
              className="button subtle mode-button"
              disabled={!info}
              title={
                customRefs
                  ? "Review a branch against the default branch"
                  : "Compare any two commits, branches, or tags"
              }
              onClick={() => {
                if (customRefs && isCustom(selection))
                  void load({ ...selection, base: "", head: "" });
                else setCustomRefs(!customRefs);
              }}
            >
              {customRefs ? "Branch view" : "Custom refs"}
            </button>
            <button
              className="button primary compare-button"
              disabled={loading || !selection.repository}
            >
              {loading ? (
                <LoaderCircle size={16} className="spin" />
              ) : customRefs ? (
                <GitCompareArrows size={16} />
              ) : (
                <RefreshCw size={15} />
              )}
              {loading ? "Loading…" : customRefs ? "Compare" : "Refresh"}
            </button>
          </form>
          {range && !customRefs && (
            <p className="range-summary">
              {range.mode === "branch" ? (
                <>
                  Changes on <strong>{range.branch || "HEAD"}</strong> since it
                  branched from <strong>{range.defaultBranch}</strong> at{" "}
                  <code>{short(range.base)}</code>.
                </>
              ) : (
                <>
                  {range.base === ":empty"
                    ? `All ${range.available} ${range.available === 1 ? "commit" : "commits"}`
                    : `The latest ${range.count === 1 ? "commit" : `${range.count} commits`}`}{" "}
                  on <strong>{range.branch || "HEAD"}</strong>
                  {range.branch && range.branch === range.defaultBranch
                    ? ", the default branch."
                    : range.defaultBranch
                      ? `, which has no commits beyond ${range.defaultBranch}.`
                      : "."}
                </>
              )}
              {comparison?.worktree && " Ends with your uncommitted changes."}
            </p>
          )}
          {error && (
            <div className="error" role="alert">
              <CircleDot size={16} />
              <span>{error}</span>
              <button
                className="icon-button"
                aria-label="Dismiss error"
                onClick={() => setError("")}
              >
                <X size={14} />
              </button>
            </div>
          )}
        </section>
        {comparison ? (
          <>
            <div className="review-tabs">
              <div className="tabs" role="tablist" aria-label="Review mode">
                <button
                  role="tab"
                  aria-selected={mode === "total"}
                  className={mode === "total" ? "selected" : ""}
                  onClick={() => setMode("total")}
                >
                  <FileDiff size={17} />
                  Total diff
                  <span className="count">{comparison.files.length}</span>
                </button>
                <button
                  role="tab"
                  aria-selected={mode === "commits"}
                  disabled={!canBrowseCommits}
                  title={
                    canBrowseCommits
                      ? "Review each commit against its first parent"
                      : "Commit view requires one commit to be an ancestor of the other"
                  }
                  className={mode === "commits" ? "selected" : ""}
                  onClick={() => setMode("commits")}
                >
                  <GitCommitHorizontal size={18} />
                  Commit by commit
                  <span className="count">{comparison.commits.length}</span>
                </button>
              </div>
              <span className="comparison-range">
                <code>{short(comparison.base)}</code>
                <ArrowRight size={13} />
                <code>{headLabel(comparison)}</code>
              </span>
            </div>
            {comparison.relationship === "diverged" && (
              <div className="notice">
                <GitBranch size={16} />
                These histories diverge. Showing the direct snapshot diff;
                commit-by-commit review needs an ancestor relationship.
              </div>
            )}
            {comparison.relationship === "reverse" && (
              <div className="notice">
                <ArrowLeftRight size={16} />
                Head is an ancestor of base. Total diff follows your selected
                direction; commits are listed oldest to newest.
              </div>
            )}
            {mode === "commits" && activeCommit && (
              <div className="commit-bar">
                <GitCommitHorizontal size={20} />
                <div className="commit-select">
                  <label htmlFor="commit">
                    Commit {commitIndex + 1} of {comparison.commits.length}
                  </label>
                  <select
                    id="commit"
                    value={commitIndex}
                    onChange={(e) => setCommitIndex(Number(e.target.value))}
                  >
                    {comparison.commits.map((commit, index) => (
                      <option key={commit.sha} value={index}>
                        {commit.uncommitted
                          ? "Working tree · Uncommitted changes"
                          : `${short(commit.sha)} · ${commit.subject}`}
                      </option>
                    ))}
                  </select>
                </div>
                <span className="commit-author">
                  {activeCommit.uncommitted
                    ? "Not committed yet"
                    : activeCommit.author}
                  <small>
                    {new Date(activeCommit.date).toLocaleDateString(undefined, {
                      month: "short",
                      day: "numeric",
                      year: "numeric",
                    })}
                    {activeCommit.parents.length > 1
                      ? " · Merge · first parent"
                      : ""}
                  </small>
                </span>
                <button
                  className="icon-button bordered"
                  aria-label="Previous commit"
                  disabled={commitIndex === 0}
                  onClick={() => setCommitIndex((i) => i - 1)}
                >
                  <ArrowLeft size={16} />
                </button>
                <button
                  className="icon-button bordered"
                  aria-label="Next commit"
                  disabled={commitIndex === comparison.commits.length - 1}
                  onClick={() => setCommitIndex((i) => i + 1)}
                >
                  <ArrowRight size={16} />
                </button>
              </div>
            )}
            <div className="review-toolbar">
              <button
                type="button"
                className="button subtle file-tree-toggle"
                aria-label={
                  sidebarCollapsed ? "Show file tree" : "Hide file tree"
                }
                title={sidebarCollapsed ? "Show file tree" : "Hide file tree"}
                aria-expanded={!sidebarCollapsed}
                aria-controls="changed-files-sidebar"
                onClick={() => setSidebarCollapsed((collapsed) => !collapsed)}
              >
                {sidebarCollapsed ? (
                  <PanelLeftOpen size={16} />
                ) : (
                  <PanelLeftClose size={16} />
                )}
              </button>
              <div className="change-summary">
                <strong>
                  {files.length} changed {files.length === 1 ? "file" : "files"}
                </strong>
                {active && (
                  <Stats
                    additions={active.additions}
                    deletions={active.deletions}
                  />
                )}
              </div>
              <div className="review-progress">
                <span>
                  {reviewedCount} / {files.length} viewed
                </span>
                <progress
                  value={reviewedCount}
                  max={files.length || 1}
                  aria-label="Files reviewed"
                />
              </div>
              <div
                className="diff-view-switch"
                role="group"
                aria-label="Diff view"
              >
                <button
                  type="button"
                  aria-pressed={diffLayout === "split"}
                  title="Side-by-side diff"
                  onClick={() => setDiffLayout("split")}
                >
                  Split
                </button>
                <button
                  type="button"
                  aria-pressed={diffLayout === "unified"}
                  onClick={() => setDiffLayout("unified")}
                >
                  Unified
                </button>
              </div>
              <details className="settings">
                <summary className="button subtle">
                  <Settings2 size={15} />
                  Diff settings
                  <ChevronDown size={13} />
                </summary>
                <div className="settings-menu">
                  <label>
                    <input
                      type="checkbox"
                      checked={ignoreWhitespace}
                      onChange={(e) => setIgnoreWhitespace(e.target.checked)}
                    />
                    Ignore whitespace
                  </label>
                  <label>
                    <input
                      type="checkbox"
                      checked={wrap}
                      onChange={(e) => setWrap(e.target.checked)}
                    />
                    Wrap long lines
                  </label>
                  <label>
                    <input
                      type="checkbox"
                      checked={hideViewed}
                      onChange={(e) => setHideViewed(e.target.checked)}
                    />
                    Hide viewed files
                  </label>
                  <label>
                    Context lines
                    <select
                      aria-label="Context lines"
                      value={context}
                      onChange={(e) => setContext(e.target.value)}
                    >
                      <option value="3">3 lines</option>
                      <option value="20">20 lines</option>
                      <option value="all">Entire file</option>
                    </select>
                  </label>
                </div>
              </details>
            </div>
            {mode === "commits" && commitLoading ? (
              <div className="loading-state">
                <LoaderCircle className="spin" size={20} />
                Loading commit…
              </div>
            ) : (
              <div
                className={`review-layout${sidebarCollapsed ? " sidebar-collapsed" : ""}`}
              >
                <aside
                  id="changed-files-sidebar"
                  className="file-sidebar"
                  hidden={sidebarCollapsed}
                >
                  <div className="file-filter">
                    <Search size={15} />
                    <input
                      aria-label="Filter files"
                      placeholder="Filter files…"
                      value={filter}
                      onChange={(e) => setFilter(e.target.value)}
                    />
                    {filter && (
                      <button
                        aria-label="Clear filter"
                        className="icon-button"
                        onClick={() => setFilter("")}
                      >
                        <X size={13} />
                      </button>
                    )}
                  </div>
                  <div className="sidebar-label">
                    FILES CHANGED<span>{visibleFiles.length}</span>
                  </div>
                  <nav aria-label="Changed files">
                    {visibleFiles.map((file) => (
                      <button
                        key={file.path}
                        className={`file-nav ${activePath === file.path ? "active" : ""}`}
                        title={file.path}
                        onClick={() => navigateTo(file.path)}
                      >
                        <FileCode2 size={15} />
                        <span>{file.path}</span>
                        {reviewed.has(file.path) ? (
                          <Check size={13} className="added" />
                        ) : (
                          <span className={`file-status status-${file.status}`}>
                            {file.status}
                          </span>
                        )}
                      </button>
                    ))}
                  </nav>
                  <div className="sidebar-footnote">
                    <span className="connection-dot" />
                    Read-only · Nothing leaves your machine
                  </div>
                </aside>
                <section
                  className="diff-list"
                  aria-label="File diffs"
                  aria-busy={loading}
                >
                  {active &&
                    visibleFiles.map((file) => (
                      <FileCard
                        key={`${active.base}:${active.head}:${file.path}`}
                        file={file}
                        comparison={active}
                        headLabel={headLabel(
                          active,
                          comparison.worktree ? comparison.head : undefined,
                        )}
                        context={context}
                        ignoreWhitespace={ignoreWhitespace}
                        wrap={wrap}
                        layout={diffLayout}
                        viewed={reviewed.has(file.path)}
                        onViewed={() => toggleViewed(file.path)}
                      />
                    ))}
                  {!visibleFiles.length && (
                    <div className="empty-files">
                      <Check size={28} />
                      <h2>
                        {files.length
                          ? "No files to show"
                          : "No changes between these commits"}
                      </h2>
                      <p>
                        {files.length
                          ? "Adjust your filter or show viewed files in diff settings."
                          : "The two snapshots have identical contents."}
                      </p>
                    </div>
                  )}
                  {!!visibleFiles.length && (
                    <div className="end-note">
                      <Check size={14} />
                      You’ve reached the end of this diff.
                    </div>
                  )}
                </section>
              </div>
            )}
          </>
        ) : (
          <section className="welcome">
            <div className="welcome-icon">
              <GitCompareArrows size={30} />
            </div>
            <h2>Your next review starts here</h2>
            <p>
              Choose a local Git working copy and a branch. Differ compares it
              with the default branch and takes care of the rest.
            </p>
            {!loading && (
              <button
                className="button primary welcome-choose"
                onClick={() => setPickerOpen(true)}
              >
                <FolderGit2 size={16} />
                Choose a repository
              </button>
            )}
            <div className="welcome-features">
              <span>
                <FileDiff size={16} />
                Side-by-side diffs
              </span>
              <span>
                <GitCommitHorizontal size={17} />
                One commit at a time
              </span>
              <span>
                <Check size={16} />
                Always read-only
              </span>
            </div>
            {loading && (
              <span className="loading-state">
                <LoaderCircle size={18} className="spin" />
                Opening comparison…
              </span>
            )}
          </section>
        )}
      </main>
      {pickerOpen && (
        <RepoPicker
          current={selection.repository}
          onClose={() => setPickerOpen(false)}
          onChoose={(repository) => {
            setPickerOpen(false);
            void load({
              repository,
              branch: "",
              count: 1,
              uncommitted: selection.uncommitted,
              base: "",
              head: "",
            });
          }}
        />
      )}
      <footer>
        <span>
          differ <span className="footer-dot">/</span> A little clarity for your
          code.
        </span>
        <span>Built for the work before the pull request.</span>
      </footer>
    </>
  );
}

const languages: Record<string, string> = {
  ts: "typescript",
  tsx: "typescript",
  js: "javascript",
  jsx: "javascript",
  go: "go",
  rs: "rust",
  py: "python",
  rb: "ruby",
  json: "json",
  css: "css",
  html: "xml",
  vue: "xml",
  md: "markdown",
  sh: "bash",
  yml: "yaml",
  yaml: "yaml",
  sql: "sql",
  java: "java",
  c: "c",
  h: "c",
  cpp: "cpp",
  toml: "ini",
};
function Highlight({ text, language }: { text: string; language?: string }) {
  if (!language || !hljs.getLanguage(language) || text.length > 2000)
    return <>{text}</>;
  return (
    <span
      dangerouslySetInnerHTML={{
        __html: hljs.highlight(text, { language, ignoreIllegals: true }).value,
      }}
    />
  );
}
function CodeCell({
  line,
  other,
  side,
  changed,
  language,
  lineNumbers,
}: {
  line?: Line;
  other?: Line;
  side: "left" | "right";
  changed: boolean;
  language?: string;
  lineNumbers?: { old?: number; new?: number };
}) {
  const kind = line
    ? changed
      ? side === "left"
        ? "deletion"
        : "addition"
      : "context"
    : "empty";
  const range =
    line && other && changed
      ? side === "left"
        ? changedSpan(line.text, other.text)
        : changedSpan(other.text, line.text)
      : null;
  const end = range?.[side === "left" ? 1 : 2];
  return (
    <>
      {lineNumbers ? (
        <>
          <td
            className={`line-number ${kind}`}
            aria-label={
              lineNumbers.old !== undefined
                ? `Old line ${lineNumbers.old}`
                : undefined
            }
          >
            {lineNumbers.old}
          </td>
          <td
            className={`line-number ${kind}`}
            aria-label={
              lineNumbers.new !== undefined
                ? `New line ${lineNumbers.new}`
                : undefined
            }
          >
            {lineNumbers.new}
          </td>
        </>
      ) : (
        <td
          className={`line-number ${kind}`}
          aria-label={
            line
              ? `${side === "left" ? "Old" : "New"} line ${line.number}`
              : undefined
          }
        >
          {line?.number}
        </td>
      )}
      <td className={`code-cell ${kind}`}>
        <span className="line-sign" aria-hidden="true">
          {changed && line ? (side === "left" ? "−" : "+") : " "}
        </span>
        <code>
          {line &&
            (range && end !== undefined ? (
              <>
                <Highlight
                  text={line.text.slice(0, range[0])}
                  language={language}
                />
                <mark>
                  <Highlight
                    text={line.text.slice(range[0], end)}
                    language={language}
                  />
                </mark>
                <Highlight text={line.text.slice(end)} language={language} />
              </>
            ) : (
              <Highlight text={line.text} language={language} />
            ))}
          {line?.noNewline && (
            <span className="no-newline" title="No newline at end of file">
              {" "}
              ⏎ No newline
            </span>
          )}
        </code>
      </td>
    </>
  );
}
function FileCard({
  file,
  comparison,
  headLabel,
  context,
  ignoreWhitespace,
  wrap,
  layout,
  viewed,
  onViewed,
}: {
  file: ChangedFile;
  comparison: Comparison;
  headLabel: string;
  context: string;
  ignoreWhitespace: boolean;
  wrap: boolean;
  layout: DiffLayout;
  viewed: boolean;
  onViewed: () => void;
}) {
  const [collapsed, setCollapsed] = useState(false);
  const [visible, setVisible] = useState(false);
  const [patch, setPatch] = useState<Patch>();
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  const element = useRef<HTMLElement>(null);
  const leftScroll = useRef<HTMLDivElement>(null);
  const rightScroll = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) {
          setVisible(true);
          observer.disconnect();
        }
      },
      { rootMargin: "700px" },
    );
    if (element.current) observer.observe(element.current);
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    if (!visible) return;
    const controller = new AbortController();
    setPatch(undefined);
    setError("");
    api<Patch>(
      "file",
      {
        repo: comparison.repository,
        base: comparison.base,
        head: comparison.head,
        path: file.path,
        context,
        whitespace: ignoreWhitespace ? "ignore" : "show",
      },
      controller.signal,
    )
      .then(setPatch)
      .catch((e) => {
        if (!controller.signal.aborted) setError(e.message);
      });
    return () => controller.abort();
  }, [visible, comparison, file.path, context, ignoreWhitespace, retry]);
  const hunks = useMemo(() => parsePatch(patch?.patch ?? ""), [patch]);
  const unifiedHunks = useMemo(
    () =>
      layout === "unified"
        ? hunks.map((hunk) => ({
            heading: hunk.heading,
            rows: toUnifiedRows(hunk.rows),
          }))
        : [],
    [hunks, layout],
  );
  const language = languages[file.path.split(".").pop() || ""];
  const longestLine = useMemo(
    () =>
      hunks.reduce(
        (longest, hunk) =>
          hunk.rows.reduce(
            (n, row) =>
              Math.max(
                n,
                (row.left?.text.replace(/\t/g, "    ").length ?? 0) +
                  (row.left?.noNewline ? 18 : 0),
                (row.right?.text.replace(/\t/g, "    ").length ?? 0) +
                  (row.right?.noNewline ? 18 : 0),
              ),
            longest,
          ),
        0,
      ),
    [hunks],
  );
  return (
    <article
      ref={element}
      id={`file-${encodeURIComponent(file.path)}`}
      className={`file-card ${viewed ? "viewed" : ""}`}
    >
      <header className="file-header">
        <button
          className="icon-button collapse"
          aria-label={`${collapsed ? "Expand" : "Collapse"} ${file.path}`}
          aria-expanded={!collapsed}
          onClick={() => setCollapsed(!collapsed)}
        >
          {collapsed ? <ChevronRight size={16} /> : <ChevronDown size={16} />}
        </button>
        <FileCode2 size={16} className="file-icon" />
        <span className="file-path" title={file.path}>
          {file.oldPath !== file.path && (
            <span className="old-path">{file.oldPath} → </span>
          )}
          {file.path}
        </span>
        <span className={`status-label status-${file.status}`}>
          {statusNames[file.status] || file.status}
        </span>
        <Stats additions={file.additions} deletions={file.deletions} />
        <label className="viewed-checkbox">
          <input type="checkbox" checked={viewed} onChange={onViewed} />
          <span>Viewed</span>
        </label>
      </header>
      {!collapsed && (
        <>
          {file.oldMode !== file.newMode &&
            file.status !== "A" &&
            file.status !== "D" && (
              <div className="mode-change">
                File mode changed: {file.oldMode} → {file.newMode}
              </div>
            )}
          {error ? (
            <div className="patch-message error-message">
              {error}
              <button className="button" onClick={() => setRetry((r) => r + 1)}>
                Retry
              </button>
            </div>
          ) : !patch ? (
            <div className="patch-message">
              <LoaderCircle size={16} className="spin" />
              Loading diff…
            </div>
          ) : hunks.length && layout === "unified" ? (
            <div
              className={`diff-scroll unified-diff ${wrap ? "wrap-lines" : ""}`}
              tabIndex={0}
              aria-label="Unified code, scroll horizontally for long lines"
            >
              <table
                className="diff-table unified-table"
                style={{
                  minWidth: wrap ? 0 : Math.max(310, longestLine * 7.2 + 118),
                }}
                aria-label={`Unified diff for ${file.path}`}
              >
                <colgroup>
                  <col className="number-col" />
                  <col className="number-col" />
                  <col />
                </colgroup>
                <thead>
                  <tr>
                    <th scope="col">Old</th>
                    <th scope="col">New</th>
                    <th scope="col">
                      Changes{" "}
                      <code>
                        {short(comparison.base)} → {headLabel}
                      </code>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {unifiedHunks.map((hunk, index) => (
                    <React.Fragment key={index}>
                      <tr className="hunk-header">
                        <td colSpan={3}>{hunk.heading}</td>
                      </tr>
                      {hunk.rows.map((row, rowIndex) => (
                        <tr key={rowIndex}>
                          <CodeCell
                            line={row.line}
                            other={row.other}
                            side={row.kind === "deletion" ? "left" : "right"}
                            changed={row.kind !== "context"}
                            language={language}
                            lineNumbers={{
                              old: row.oldNumber,
                              new: row.newNumber,
                            }}
                          />
                        </tr>
                      ))}
                    </React.Fragment>
                  ))}
                </tbody>
              </table>
            </div>
          ) : hunks.length && !wrap ? (
            <div
              className="split-diff"
              role="group"
              aria-label={`Side-by-side diff for ${file.path}`}
            >
              {(["left", "right"] as const).map((side) => (
                <div
                  className="diff-half"
                  key={side}
                  ref={side === "left" ? leftScroll : rightScroll}
                  tabIndex={0}
                  aria-label={`${side === "left" ? "Before" : "After"} code, scroll horizontally for long lines`}
                  onScroll={(event) => {
                    const other =
                      side === "left"
                        ? rightScroll.current
                        : leftScroll.current;
                    if (
                      other &&
                      other.scrollLeft !== event.currentTarget.scrollLeft
                    )
                      other.scrollLeft = event.currentTarget.scrollLeft;
                  }}
                >
                  <table
                    className="diff-table"
                    style={{ minWidth: Math.max(310, longestLine * 7.2 + 76) }}
                  >
                    <colgroup>
                      <col className="number-col" />
                      <col />
                    </colgroup>
                    <thead>
                      <tr>
                        <th colSpan={2}>
                          {side === "left" ? "Before" : "After"}{" "}
                          <code>
                            {side === "left"
                              ? short(comparison.base)
                              : headLabel}
                          </code>
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {hunks.map((hunk, index) => (
                        <React.Fragment key={index}>
                          <tr className="hunk-header">
                            <td colSpan={2}>{hunk.heading}</td>
                          </tr>
                          {hunk.rows.map((row, rowIndex) => (
                            <tr key={rowIndex}>
                              <CodeCell
                                line={row[side]}
                                other={row[side === "left" ? "right" : "left"]}
                                side={side}
                                changed={row.kind === "change"}
                                language={language}
                              />
                            </tr>
                          ))}
                        </React.Fragment>
                      ))}
                    </tbody>
                  </table>
                </div>
              ))}
            </div>
          ) : hunks.length ? (
            <div className={`diff-scroll ${wrap ? "wrap-lines" : ""}`}>
              <table
                className="diff-table"
                aria-label={`Side-by-side diff for ${file.path}`}
              >
                <colgroup>
                  <col className="number-col" />
                  <col />
                  <col className="number-col" />
                  <col />
                </colgroup>
                <thead>
                  <tr>
                    <th colSpan={2}>
                      Before <code>{short(comparison.base)}</code>
                    </th>
                    <th colSpan={2}>
                      After <code>{headLabel}</code>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {hunks.map((hunk, index) => (
                    <React.Fragment key={index}>
                      <tr className="hunk-header">
                        <td colSpan={4}>{hunk.heading}</td>
                      </tr>
                      {hunk.rows.map((row, rowIndex) => (
                        <tr key={rowIndex}>
                          <CodeCell
                            line={row.left}
                            other={row.right}
                            side="left"
                            changed={row.kind === "change"}
                            language={language}
                          />
                          <CodeCell
                            line={row.right}
                            other={row.left}
                            side="right"
                            changed={row.kind === "change"}
                            language={language}
                          />
                        </tr>
                      ))}
                    </React.Fragment>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <div className="patch-message">
              <FileDiff size={20} />
              {patch.message ||
                (ignoreWhitespace
                  ? "No content changes with whitespace ignored."
                  : file.status === "R"
                    ? "File renamed without content changes."
                    : "No text changes.")}
            </div>
          )}
        </>
      )}
    </article>
  );
}

createRoot(document.getElementById("root")!).render(<App />);
