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
  GitMerge,
  List,
  ListTree,
  LoaderCircle,
  MessageSquare,
  MessageSquarePlus,
  MessagesSquare,
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
import type {
  ChangedFile,
  Comparison,
  Config,
  ConversationList,
  Patch,
  RepoInfo,
  Side,
  Thread,
} from "./types";
import { RepoPicker, rememberRepository } from "./RepoPicker";
import { Conversations } from "./Conversations";
import { FileNav, type FileView } from "./FileNav";
import { buildFileTree, splitPath, treeFiles } from "./fileTree";
import {
  CommentsPanel,
  Composer,
  ThreadCard,
  createThread,
  lineLabel,
  openThreads,
  threadsByFile,
  useThreads,
  type CommentTarget,
} from "./Comments";
import { api, send } from "./api";
import { onServerEvent, tabId } from "./events";
import {
  changedSpan,
  parsePatch,
  toUnifiedRows,
  type Line,
  type Row,
} from "./diff";
import "./style.css";

type DiffLayout = "split" | "unified";
// A place in a diff to scroll to.
interface FocusTarget {
  path: string;
  line?: number;
  side?: Side;
}

const short = (sha: string) =>
  sha === ":empty" ? "empty tree" : sha.slice(0, 7);
// A re-merge base is a tree Git wrote for the merge commit's parents.
const baseLabel = (comparison: Comparison) =>
  comparison.relationship === "remerge"
    ? "automatic merge"
    : short(comparison.base);
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
  const [mode, setMode] = useState<
    "total" | "commits" | "conversations" | "comments"
  >("total");
  const [conversations, setConversations] = useState<ConversationList>();
  const [conversationsError, setConversationsError] = useState("");
  const [conversationCommit, setConversationCommit] = useState("");
  const [commitIndex, setCommitIndex] = useState(0);
  // How merge commits are shown in commit-by-commit review: against their
  // first parent, or against Git's automatic re-merge of both parents.
  const [mergeView, setMergeView] = useState<"first-parent" | "remerge">(
    "first-parent",
  );
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
  const [fileView, setFileView] = useState<FileView>(() => {
    try {
      return localStorage.getItem("differ:file-view") === "list"
        ? "list"
        : "tree";
    } catch {
      return "tree";
    }
  });
  const [collapsedDirs, setCollapsedDirs] = useState<Set<string>>(new Set());
  const [copied, setCopied] = useState(false);
  const request = useRef<AbortController | undefined>(undefined);
  const commitRequest = useRef<AbortController | undefined>(undefined);
  const active = mode === "total" ? comparison : viewComparison;
  const activeCommit = comparison?.commits[commitIndex];
  const canRemerge = activeCommit?.parents.length === 2;
  const remerge = canRemerge && mergeView === "remerge";
  const reviewKey = active
    ? `differ:review:${active.repository}:${active.base}:${active.head}`
    : "";
  const range = comparison?.range;
  // Turns linked to each commit in this comparison, for counts and badges.
  const linkedTurns = useMemo(() => {
    const shas = new Set(comparison?.commits.map((c) => c.sha));
    const byCommit = new Map<string, number>();
    let size = 0;
    for (const turn of conversations?.turns ?? []) {
      const links = turn.links.filter((link) => shas.has(link.sha));
      if (links.length || turn.onBranch) size++;
      for (const link of links)
        byCommit.set(link.sha, (byCommit.get(link.sha) ?? 0) + 1);
    }
    return { size, byCommit };
  }, [comparison, conversations]);
  const commitTurns = activeCommit
    ? (linkedTurns.byCommit.get(activeCommit.sha) ?? 0)
    : 0;
  const totalTarget: CommentTarget | undefined = comparison && {
    repository: comparison.repository,
    base: comparison.base,
    head: comparison.head,
    branch: comparison.range?.branch,
  };
  const totalThreads = useThreads(totalTarget);
  const viewThreads = useThreads(
    mode === "commits" && viewComparison
      ? {
          repository: viewComparison.repository,
          base: viewComparison.base,
          head: viewComparison.head,
        }
      : undefined,
  );
  const activeThreads = mode === "commits" ? viewThreads : totalThreads;
  const activeByFile = useMemo(
    () => threadsByFile(activeThreads.list?.threads),
    [activeThreads.list],
  );
  const author = totalThreads.list?.author || "You";
  // A file, and optionally a line, to scroll to once the diff showing it has
  // rendered: from the Comments tab or from a link an agent opened.
  const [pending, setPending] = useState<FocusTarget>();
  const pendingShown =
    !!pending &&
    !!active?.files.some(
      (f) => f.path === pending.path || f.oldPath === pending.path,
    );
  useEffect(() => {
    if (!pending || !pendingShown) return;
    const timer = setTimeout(() => navigateTo(pending.path));
    if (!pending.line) setPending(undefined);
    return () => clearTimeout(timer);
  }, [pending, pendingShown]);
  // A link's commit and file, applied once its comparison has loaded.
  const deepLink = useRef<{ commit: string; focus?: FocusTarget }>(undefined);
  // The file at the top of the diff, reported as the one in view.
  const [fileInView, setFileInView] = useState("");
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const update = () => {
      clearTimeout(timer);
      timer = setTimeout(() => {
        const card = [
          ...document.querySelectorAll<HTMLElement>(".file-card"),
        ].find((c) => c.getBoundingClientRect().bottom > 120);
        const id = card?.id.replace(/^file-/, "");
        setFileInView(id ? decodeURIComponent(id) : "");
      }, 300);
    };
    window.addEventListener("scroll", update, { passive: true });
    update();
    return () => {
      clearTimeout(timer);
      window.removeEventListener("scroll", update);
    };
  }, [active]);
  // Tell the server what this tab shows, so agents can see it and send this
  // tab to other diffs; the most recently used tab is the one they reach.
  const visibleCommit = mode === "commits" ? activeCommit?.sha : undefined;
  const report = useRef(() => {});
  report.current = () => {
    if (!comparison) return;
    const view =
      mode === "commits" && viewComparison ? viewComparison : comparison;
    void send("POST", "view", {
      tab: tabId,
      query: location.search,
      repository: comparison.repository,
      branch: comparison.range?.branch ?? "",
      base: comparison.base,
      head: comparison.head,
      mode,
      commit: visibleCommit ?? "",
      viewBase: view.base,
      viewHead: view.head,
      file: mode === "total" || mode === "commits" ? fileInView : "",
    }).catch(() => {
      // Agents simply see an older view.
    });
  };
  useEffect(() => {
    if (!comparison) return;
    const query = new URLSearchParams(location.search);
    for (const key of ["file", "line", "side"]) query.delete(key);
    if (visibleCommit) {
      query.set("mode", "commits");
      query.set("commit", visibleCommit);
    } else {
      query.delete("mode");
      query.delete("commit");
    }
    if (`?${query}` !== location.search)
      window.history.replaceState(null, "", `?${query}`);
    report.current();
  }, [comparison, viewComparison, mode, visibleCommit, fileInView]);
  useEffect(() => {
    const onFocus = () => {
      if (document.visibilityState === "visible") report.current();
    };
    window.addEventListener("focus", onFocus);
    document.addEventListener("visibilitychange", onFocus);
    const stop = onServerEvent((event) => {
      if (event.type === "navigate" && event.tab === tabId) {
        window.focus();
        location.search = event.query;
      }
    });
    return () => {
      window.removeEventListener("focus", onFocus);
      document.removeEventListener("visibilitychange", onFocus);
      stop();
    };
  }, []);

  useEffect(() => {
    if (!comparison) return;
    const controller = new AbortController();
    const committed = comparison.commits.filter((c) => !c.uncommitted);
    const worktree = comparison.commits.find((c) => c.uncommitted);
    setConversations(undefined);
    setConversationsError("");
    api<ConversationList>(
      "conversations",
      {
        repo: comparison.repository,
        commits: committed.map((c) => c.sha).join(","),
        // Turns on the reviewed branch stay visible even without a commit link.
        branch: comparison.range?.branch ?? "",
        ...(worktree
          ? { tree: worktree.sha, treeParent: worktree.parents[0] }
          : {}),
      },
      controller.signal,
    )
      .then(setConversations)
      .catch((e) => {
        if (!controller.signal.aborted) setConversationsError(e.message);
      });
    return () => controller.abort();
  }, [comparison]);

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
      localStorage.setItem("differ:file-view", fileView);
    } catch {
      // The view toggle still works when browser storage is unavailable.
    }
  }, [fileView]);

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
      const link = deepLink.current;
      deepLink.current = undefined;
      const linked = link?.commit
        ? result.commits.findIndex((c) => c.sha === link.commit)
        : -1;
      if (linked >= 0) {
        setMode("commits");
        setCommitIndex(linked);
      }
      setPending(link?.focus);
    } catch (e) {
      if (!controller.signal.aborted) setError((e as Error).message);
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
  }
  // The worktree holding a branch's uncommitted changes: wherever it is
  // checked out, or the opened worktree for a detached HEAD.
  const worktreeOf = (branch: string) =>
    branch
      ? info?.branches.find((b) => b.name === branch)?.worktree
      : info?.worktrees.find((w) => w.current && w.detached)?.path;
  const chooseBranch = (branch: string) =>
    void load({
      ...selection,
      branch,
      count: 1,
      uncommitted: selection.uncommitted && !!worktreeOf(branch),
      base: "",
      head: "",
    });
  const branchWorktree = worktreeOf(selection.branch);
  const onCheckedOutBranch = !!branchWorktree;
  const branchDirty = info?.worktrees.some(
    (w) => w.path === branchWorktree && w.dirty,
  );
  const folderName = (path: string) => path.split(/[\\/]/).pop() || path;
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
        const line = Number(query.get("line"));
        deepLink.current = {
          commit:
            query.get("mode") === "commits" ? query.get("commit") || "" : "",
          focus: query.get("file")
            ? {
                path: query.get("file")!,
                line: Number.isInteger(line) && line > 0 ? line : undefined,
                side: query.get("side") === "old" ? "old" : "new",
              }
            : undefined,
        };
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
        base: remerge ? ":remerge" : activeCommit.parents[0] || ":empty",
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
  }, [mode, comparison, activeCommit, remerge]);
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
  const filtered = files.filter(
    (file) =>
      file.path.toLowerCase().includes(filter.toLowerCase()) &&
      (!hideViewed || !reviewed.has(file.path)),
  );
  // Keep the diffs in the same order as the sidebar's tree.
  const visibleFiles =
    fileView === "tree" ? treeFiles(buildFileTree(filtered)) : filtered;
  function toggleDir(path: string) {
    setCollapsedDirs((previous) => {
      const next = new Set(previous);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });
  }
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
                          {branch.current
                            ? " (checked out)"
                            : branch.worktree
                              ? ` (worktree: ${folderName(branch.worktree)})`
                              : ""}
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
                    ? `End the diff at the working tree in ${branchWorktree} instead of the last commit`
                    : "This branch is not checked out in any worktree"
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
                  {branchDirty && (
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
              {comparison?.worktree &&
                (comparison.worktreePath
                  ? ` Ends with the uncommitted changes in ${comparison.worktreePath}.`
                  : " Ends with your uncommitted changes.")}
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
                <button
                  role="tab"
                  aria-selected={mode === "conversations"}
                  title="Claude Code prompts and responses linked to these commits"
                  className={mode === "conversations" ? "selected" : ""}
                  onClick={() => {
                    setConversationCommit("");
                    setMode("conversations");
                  }}
                >
                  <MessageSquare size={17} />
                  Conversations
                  {conversations?.enabled && (
                    <span className="count">{linkedTurns.size}</span>
                  )}
                </button>
                <button
                  role="tab"
                  aria-selected={mode === "comments"}
                  title="Open review comments on the files in this comparison"
                  className={mode === "comments" ? "selected" : ""}
                  onClick={() => setMode("comments")}
                >
                  <MessagesSquare size={17} />
                  Comments
                  {totalThreads.list && (
                    <span className="count">
                      {openThreads(totalThreads.list.threads)}
                    </span>
                  )}
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
                {!!commitTurns && (
                  <button
                    className="button subtle"
                    title="Claude Code turns linked to this commit"
                    onClick={() => {
                      setConversationCommit(activeCommit.sha);
                      setMode("conversations");
                    }}
                  >
                    <MessageSquare size={14} />
                    {commitTurns} {commitTurns === 1 ? "turn" : "turns"}
                  </button>
                )}
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
                    {activeCommit.parents.length > 2
                      ? " · Merge · first parent"
                      : ""}
                  </small>
                </span>
                {canRemerge && (
                  <div
                    className="diff-view-switch merge-view-switch"
                    role="group"
                    aria-label="Merge commit view"
                  >
                    <button
                      type="button"
                      aria-pressed={!remerge}
                      title="Everything the merge brought in, compared with the first parent"
                      onClick={() => setMergeView("first-parent")}
                    >
                      All changes
                    </button>
                    <button
                      type="button"
                      aria-pressed={remerge}
                      title="Only what changed while merging, such as conflict resolutions, compared with Git's automatic merge of both parents"
                      onClick={() => setMergeView("remerge")}
                    >
                      Merge fixes
                    </button>
                  </div>
                )}
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
            {mode === "commits" && remerge && viewComparison && (
              <div className="notice merge-notice">
                <GitMerge size={16} />
                <div>
                  Compared with Git re-merging{" "}
                  <code>{short(activeCommit!.parents[0])}</code> and{" "}
                  <code>{short(activeCommit!.parents[1])}</code>. Removed
                  conflict markers show how each conflict was resolved.
                  {!!viewComparison.conflicts?.length && (
                    <ul>
                      {viewComparison.conflicts.map((conflict) => (
                        <li key={conflict}>{conflict}</li>
                      ))}
                    </ul>
                  )}
                </div>
              </div>
            )}
            {mode === "comments" && totalTarget ? (
              <CommentsPanel
                list={totalThreads.list}
                error={totalThreads.error}
                target={totalTarget}
                files={comparison.files}
                onChanged={totalThreads.reload}
                onOpenFile={(path) => {
                  setMode("total");
                  setPending({ path });
                }}
              />
            ) : mode === "conversations" ? (
              <Conversations
                list={conversations}
                error={conversationsError}
                commits={comparison.commits}
                commitFilter={conversationCommit}
                onClearFilter={() => setConversationCommit("")}
                onOpenCommit={(index) => {
                  setCommitIndex(index);
                  setMode("commits");
                }}
              />
            ) : (
              <>
                <div className="review-toolbar">
                  <button
                    type="button"
                    className="button subtle file-tree-toggle"
                    aria-label={
                      sidebarCollapsed ? "Show file tree" : "Hide file tree"
                    }
                    title={
                      sidebarCollapsed ? "Show file tree" : "Hide file tree"
                    }
                    aria-expanded={!sidebarCollapsed}
                    aria-controls="changed-files-sidebar"
                    onClick={() =>
                      setSidebarCollapsed((collapsed) => !collapsed)
                    }
                  >
                    {sidebarCollapsed ? (
                      <PanelLeftOpen size={16} />
                    ) : (
                      <PanelLeftClose size={16} />
                    )}
                  </button>
                  <div className="change-summary">
                    <strong>
                      {files.length} changed{" "}
                      {files.length === 1 ? "file" : "files"}
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
                          onChange={(e) =>
                            setIgnoreWhitespace(e.target.checked)
                          }
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
                        <button
                          className="icon-button view-toggle"
                          aria-label={
                            fileView === "tree"
                              ? "Show files as a flat list"
                              : "Show files as a directory tree"
                          }
                          title={
                            fileView === "tree"
                              ? "Show files as a flat list"
                              : "Show files as a directory tree"
                          }
                          onClick={() =>
                            setFileView(fileView === "tree" ? "list" : "tree")
                          }
                        >
                          {fileView === "tree" ? (
                            <List size={13} />
                          ) : (
                            <ListTree size={13} />
                          )}
                        </button>
                      </div>
                      <FileNav
                        files={visibleFiles}
                        view={fileView}
                        activePath={activePath}
                        reviewed={reviewed}
                        openThreadCount={(path) =>
                          openThreads(activeByFile.get(path))
                        }
                        onSelect={navigateTo}
                        collapsedDirs={collapsedDirs}
                        onToggleDir={toggleDir}
                        expandAll={!!filter}
                      />
                      <div className="sidebar-footnote">
                        <span className="connection-dot" />
                        Files untouched · Nothing leaves your machine
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
                            focus={
                              pending?.line &&
                              (pending.path === file.path ||
                                pending.path === file.oldPath)
                                ? pending
                                : undefined
                            }
                            onFocused={() => setPending(undefined)}
                            threads={activeByFile.get(file.path) ?? []}
                            author={author}
                            commentTarget={{
                              repository: active.repository,
                              base: active.base,
                              head: active.head,
                              branch:
                                mode === "total"
                                  ? comparison.range?.branch
                                  : undefined,
                            }}
                            onCommentsChanged={activeThreads.reload}
                          />
                        ))}
                      {!visibleFiles.length && (
                        <div className="empty-files">
                          <Check size={28} />
                          <h2>
                            {files.length
                              ? "No files to show"
                              : active?.relationship === "remerge"
                                ? "Nothing changed during this merge"
                                : "No changes between these commits"}
                          </h2>
                          <p>
                            {files.length
                              ? "Adjust your filter or show viewed files in diff settings."
                              : active?.relationship === "remerge"
                                ? "The commit matches Git’s automatic merge of its parents."
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
                <MessagesSquare size={16} />
                Comments that stay local
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
  selected,
  onComment,
}: {
  line?: Line;
  other?: Line;
  side: "left" | "right";
  changed: boolean;
  language?: string;
  lineNumbers?: { old?: number; new?: number };
  selected?: boolean;
  // Start a comment on this line; extend is set to widen an open draft.
  onComment?: (extend: boolean) => void;
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
      <td
        className={`code-cell ${kind}${selected ? " selected" : ""}`}
        data-line={
          line ? `${side === "left" ? "old" : "new"}:${line.number}` : undefined
        }
      >
        {line && onComment && (
          <button
            type="button"
            className="add-comment"
            aria-label={`Comment on ${side === "left" ? "old" : "new"} line ${line.number}`}
            title="Comment on this line (shift-click to extend a selection)"
            onClick={(e) => onComment(e.shiftKey)}
          >
            <Plus size={12} />
          </button>
        )}
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
// Where an open comment draft sits: one side's line range.
interface Draft {
  side: Side;
  start: number;
  end: number;
}
const lineKey = (side: Side, line?: number) =>
  line === undefined ? "" : `${side}:${line}`;
// Split rows show the old line on the left and the new line on the right.
const rowKeys = (row: Row) => [
  lineKey("old", row.left?.number),
  lineKey("new", row.right?.number),
];
// Each part of a split diff between comment threads, so threads can span
// both halves while the halves keep scrolling together.
interface SplitSegment {
  heading?: string;
  rows: Row[];
  after?: React.ReactNode;
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
  threads,
  author,
  commentTarget,
  onCommentsChanged,
  focus,
  onFocused,
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
  threads: Thread[];
  author: string;
  commentTarget: CommentTarget;
  onCommentsChanged: () => void;
  // A line to scroll to and highlight once the diff has loaded.
  focus?: FocusTarget;
  onFocused: () => void;
}) {
  const [collapsed, setCollapsed] = useState(false);
  const [visible, setVisible] = useState(false);
  const [patch, setPatch] = useState<Patch>();
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  const [draft, setDraft] = useState<Draft>();
  const [fileDraft, setFileDraft] = useState(false);
  const element = useRef<HTMLElement>(null);
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
  useEffect(() => {
    if (focus) setVisible(true);
  }, [focus]);
  useEffect(() => {
    if (!focus?.line || !patch) return;
    const cell = element.current?.querySelector(
      `[data-line="${focus.side ?? "new"}:${focus.line}"]`,
    );
    const row = cell?.closest("tr");
    if (row) {
      cell!.scrollIntoView({ behavior: "smooth", block: "center" });
      row.classList.add("flash");
      setTimeout(() => row.classList.remove("flash"), 2500);
    }
    onFocused();
  }, [focus, patch, layout]);
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
  // Threads appear under the last line they cover when that line is shown;
  // the rest (file comments, outdated threads, lines outside the shown
  // context) are listed above the diff.
  const { inline, elsewhere } = useMemo(() => {
    const shown = new Set(hunks.flatMap((hunk) => hunk.rows.flatMap(rowKeys)));
    const inline = new Map<string, Thread[]>();
    const elsewhere: Thread[] = [];
    for (const thread of threads) {
      const key =
        thread.side && thread.placement?.status !== "outdated"
          ? lineKey(thread.side, thread.placement?.endLine || undefined)
          : "";
      if (key && shown.has(key))
        inline.set(key, [...(inline.get(key) ?? []), thread]);
      else elsewhere.push(thread);
    }
    return { inline, elsewhere };
  }, [hunks, threads]);
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
  const commentOn = (side: Side, line: number) => (extend: boolean) =>
    setDraft(
      extend && draft?.side === side
        ? {
            side,
            start: Math.min(draft.start, line),
            end: Math.max(draft.end, line),
          }
        : { side, start: line, end: line },
    );
  const isSelected = (side: Side, line?: Line) =>
    !!line &&
    draft?.side === side &&
    line.number >= draft.start &&
    line.number <= draft.end;
  // The threads and draft that belong under a row with these line keys.
  function annotations(keys: string[]) {
    const here = keys.flatMap((key) => (key ? (inline.get(key) ?? []) : []));
    const drafting = draft && keys.includes(lineKey(draft.side, draft.end));
    if (!here.length && !drafting) return null;
    return (
      <div className="line-annotations">
        {here.map((thread) => (
          <ThreadCard
            key={thread.id}
            thread={thread}
            author={author}
            onChanged={onCommentsChanged}
          />
        ))}
        {drafting && (
          <div className="thread-card draft">
            <div className="thread-context">
              <span className="thread-location">
                New comment on{" "}
                {lineLabel({
                  path: file.path,
                  side: draft.side,
                  startLine: draft.start,
                  endLine: draft.end,
                } as Thread)}
              </span>
            </div>
            <Composer
              placeholder="Leave a comment… (shift-click + to widen the selection)"
              submitLabel="Comment"
              onCancel={() => setDraft(undefined)}
              onSubmit={async (body) => {
                await createThread(commentTarget, author, body, {
                  path: draft.side === "old" ? file.oldPath : file.path,
                  side: draft.side,
                  startLine: draft.start,
                  endLine: draft.end,
                });
                setDraft(undefined);
                onCommentsChanged();
              }}
            />
          </div>
        )}
      </div>
    );
  }
  const splitSegments: SplitSegment[] = [];
  if (layout === "split" && !wrap) {
    for (const hunk of hunks) {
      let segment: SplitSegment = { heading: hunk.heading, rows: [] };
      for (const row of hunk.rows) {
        segment.rows.push(row);
        const after = annotations(rowKeys(row));
        if (after) {
          segment.after = after;
          splitSegments.push(segment);
          segment = { rows: [] };
        }
      }
      if (segment.rows.length || segment.heading) splitSegments.push(segment);
    }
  }
  function syncScroll(event: React.UIEvent<HTMLDivElement>) {
    const x = event.currentTarget.scrollLeft;
    element.current
      ?.querySelectorAll<HTMLElement>(".diff-half")
      .forEach((half) => {
        if (half !== event.currentTarget && half.scrollLeft !== x)
          half.scrollLeft = x;
      });
  }
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
          <span className="file-path-dir">{splitPath(file.path).dir}</span>
          {splitPath(file.path).name}
        </span>
        <span className={`status-label status-${file.status}`}>
          {statusNames[file.status] || file.status}
        </span>
        <Stats additions={file.additions} deletions={file.deletions} />
        <button
          className="icon-button file-comment-button"
          aria-label={`Comment on ${file.path}`}
          title="Comment on this file"
          onClick={() => {
            setCollapsed(false);
            setFileDraft(true);
          }}
        >
          <MessageSquarePlus size={15} />
        </button>
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
          {(!!elsewhere.length || fileDraft) && (
            <div className="file-threads">
              {elsewhere.map((thread) => (
                <ThreadCard
                  key={thread.id}
                  thread={thread}
                  author={author}
                  onChanged={onCommentsChanged}
                  showSnippet
                />
              ))}
              {fileDraft && (
                <div className="thread-card draft">
                  <Composer
                    placeholder={`Comment on ${file.path}…`}
                    submitLabel="Comment"
                    onCancel={() => setFileDraft(false)}
                    onSubmit={async (body) => {
                      const deleted = file.status === "D";
                      await createThread(commentTarget, author, body, {
                        path: deleted ? file.oldPath : file.path,
                        side: deleted ? "old" : "new",
                      });
                      setFileDraft(false);
                      onCommentsChanged();
                    }}
                  />
                </div>
              )}
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
                        {baseLabel(comparison)} → {headLabel}
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
                      {hunk.rows.map((row, rowIndex) => {
                        const side: Side =
                          row.kind === "deletion" ? "old" : "new";
                        // A context line has both numbers; threads on
                        // either side of it belong under it.
                        const after = annotations([
                          lineKey("old", row.oldNumber),
                          lineKey("new", row.newNumber),
                        ]);
                        return (
                          <React.Fragment key={rowIndex}>
                            <tr>
                              <CodeCell
                                line={row.line}
                                other={row.other}
                                side={side === "old" ? "left" : "right"}
                                changed={row.kind !== "context"}
                                language={language}
                                lineNumbers={{
                                  old: row.oldNumber,
                                  new: row.newNumber,
                                }}
                                selected={isSelected(side, row.line)}
                                onComment={commentOn(side, row.line.number)}
                              />
                            </tr>
                            {after && (
                              <tr className="annotation-row">
                                <td colSpan={3}>{after}</td>
                              </tr>
                            )}
                          </React.Fragment>
                        );
                      })}
                    </React.Fragment>
                  ))}
                </tbody>
              </table>
            </div>
          ) : hunks.length && !wrap ? (
            <div
              className="split-diff-segments"
              role="group"
              aria-label={`Side-by-side diff for ${file.path}`}
            >
              {splitSegments.map((segment, index) => (
                <React.Fragment key={index}>
                  <div className="split-diff">
                    {(["left", "right"] as const).map((side) => (
                      <div
                        className="diff-half"
                        key={side}
                        tabIndex={index === 0 ? 0 : -1}
                        aria-label={`${side === "left" ? "Before" : "After"} code, scroll horizontally for long lines`}
                        onScroll={syncScroll}
                      >
                        <table
                          className="diff-table"
                          style={{
                            minWidth: Math.max(310, longestLine * 7.2 + 76),
                          }}
                        >
                          <colgroup>
                            <col className="number-col" />
                            <col />
                          </colgroup>
                          {index === 0 && (
                            <thead>
                              <tr>
                                <th colSpan={2}>
                                  {side === "left" ? "Before" : "After"}{" "}
                                  <code>
                                    {side === "left"
                                      ? baseLabel(comparison)
                                      : headLabel}
                                  </code>
                                </th>
                              </tr>
                            </thead>
                          )}
                          <tbody>
                            {segment.heading && (
                              <tr className="hunk-header">
                                <td colSpan={2}>{segment.heading}</td>
                              </tr>
                            )}
                            {segment.rows.map((row, rowIndex) => {
                              const line = row[side];
                              const lineSide = side === "left" ? "old" : "new";
                              return (
                                <tr key={rowIndex}>
                                  <CodeCell
                                    line={line}
                                    other={
                                      row[side === "left" ? "right" : "left"]
                                    }
                                    side={side}
                                    changed={row.kind === "change"}
                                    language={language}
                                    selected={isSelected(lineSide, line)}
                                    onComment={
                                      line && commentOn(lineSide, line.number)
                                    }
                                  />
                                </tr>
                              );
                            })}
                          </tbody>
                        </table>
                      </div>
                    ))}
                  </div>
                  {segment.after && (
                    <div className="split-annotation">{segment.after}</div>
                  )}
                </React.Fragment>
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
                      Before <code>{baseLabel(comparison)}</code>
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
                      {hunk.rows.map((row, rowIndex) => {
                        const after = annotations(rowKeys(row));
                        return (
                          <React.Fragment key={rowIndex}>
                            <tr>
                              <CodeCell
                                line={row.left}
                                other={row.right}
                                side="left"
                                changed={row.kind === "change"}
                                language={language}
                                selected={isSelected("old", row.left)}
                                onComment={
                                  row.left && commentOn("old", row.left.number)
                                }
                              />
                              <CodeCell
                                line={row.right}
                                other={row.left}
                                side="right"
                                changed={row.kind === "change"}
                                language={language}
                                selected={isSelected("new", row.right)}
                                onComment={
                                  row.right &&
                                  commentOn("new", row.right.number)
                                }
                              />
                            </tr>
                            {after && (
                              <tr className="annotation-row">
                                <td colSpan={4}>{after}</td>
                              </tr>
                            )}
                          </React.Fragment>
                        );
                      })}
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
