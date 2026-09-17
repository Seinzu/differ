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
  PanelLeftClose,
  PanelLeftOpen,
  Search,
  Settings2,
  X,
} from "lucide-react";
import hljs from "highlight.js/lib/common";
import type { ChangedFile, Comparison, Config, Patch } from "./types";
import { changedSpan, parsePatch, type Line } from "./diff";
import "./style.css";

const short = (sha: string) =>
  sha === ":empty" ? "empty tree" : sha.slice(0, 7);
const statusNames: Record<string, string> = {
  A: "Added",
  D: "Deleted",
  M: "Modified",
  R: "Renamed",
  T: "Type changed",
  C: "Copied",
};
async function api<T>(
  route: string,
  params: Record<string, string> = {},
  signal?: AbortSignal,
): Promise<T> {
  const response = await fetch(`/api/${route}?${new URLSearchParams(params)}`, {
    signal,
  });
  const data = await response.json();
  if (!response.ok)
    throw new Error(data.error || "The request could not be completed.");
  return data;
}
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
function App() {
  const [form, setForm] = useState<Config>({
    repository: "",
    base: "",
    head: "",
  });
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

  async function compare(config: Config) {
    request.current?.abort();
    commitRequest.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    setLoading(true);
    setError("");
    try {
      const result = await api<Comparison>(
        "compare",
        { repo: config.repository, base: config.base, head: config.head },
        controller.signal,
      );
      setComparison(result);
      setViewComparison(undefined);
      setMode("total");
      setCommitIndex(0);
      setFilter("");
      setActivePath("");
      setForm({ ...config, repository: result.repository });
      const query = new URLSearchParams({
        repo: result.repository,
        base: config.base,
        head: config.head,
      });
      window.history.replaceState(null, "", `?${query}`);
    } catch (e) {
      if (!controller.signal.aborted) setError((e as Error).message);
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
  }
  useEffect(() => {
    const controller = new AbortController();
    api<Config>("config", {}, controller.signal)
      .then((config) => {
        const query = new URLSearchParams(location.search);
        const next = {
          repository: query.get("repo") ?? config.repository,
          base: query.get("base") ?? config.base,
          head: query.get("head") ?? config.head,
        };
        setForm(next);
        void compare(next);
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
              void compare(form);
            }}
          >
            <label className="repo-input">
              <span>Working copy</span>
              <div className="input-wrap">
                <FolderGit2 size={16} />
                <input
                  required
                  aria-label="Working copy"
                  placeholder="/path/to/repository"
                  value={form.repository}
                  onChange={(e) =>
                    setForm({ ...form, repository: e.target.value })
                  }
                  spellCheck={false}
                />
              </div>
            </label>
            <label className="ref-input">
              <span>Base</span>
              <div className="input-wrap">
                <GitBranch size={15} />
                <input
                  required
                  aria-label="Base commit"
                  placeholder="Commit SHA or ref"
                  value={form.base}
                  onChange={(e) => setForm({ ...form, base: e.target.value })}
                  spellCheck={false}
                />
              </div>
            </label>
            <button
              type="button"
              className="icon-button swap"
              title="Swap base and head"
              aria-label="Swap base and head"
              onClick={() =>
                setForm({ ...form, base: form.head, head: form.base })
              }
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
                  value={form.head}
                  onChange={(e) => setForm({ ...form, head: e.target.value })}
                  spellCheck={false}
                />
              </div>
            </label>
            <button
              className="button primary compare-button"
              disabled={loading}
            >
              {loading ? (
                <LoaderCircle size={16} className="spin" />
              ) : (
                <GitCompareArrows size={16} />
              )}
              {loading ? "Comparing…" : "Compare"}
            </button>
          </form>
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
                <code>{short(comparison.head)}</code>
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
                        {short(commit.sha)} · {commit.subject}
                      </option>
                    ))}
                  </select>
                </div>
                <span className="commit-author">
                  {activeCommit.author}
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
                        context={context}
                        ignoreWhitespace={ignoreWhitespace}
                        wrap={wrap}
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
              Choose a local Git working copy and enter two commit SHAs,
              branches, or tags. Differ will take care of the rest.
            </p>
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
}: {
  line?: Line;
  other?: Line;
  side: "left" | "right";
  changed: boolean;
  language?: string;
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
  context,
  ignoreWhitespace,
  wrap,
  viewed,
  onViewed,
}: {
  file: ChangedFile;
  comparison: Comparison;
  context: string;
  ignoreWhitespace: boolean;
  wrap: boolean;
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
                            {short(
                              side === "left"
                                ? comparison.base
                                : comparison.head,
                            )}
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
                      After <code>{short(comparison.head)}</code>
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
