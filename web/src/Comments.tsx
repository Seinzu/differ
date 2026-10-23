import React, { useEffect, useRef, useState } from "react";
import {
  Bot,
  Check,
  FileCode2,
  LoaderCircle,
  MessageSquare,
  Pencil,
  RotateCcw,
  Trash2,
} from "lucide-react";
import type { ChangedFile, Side, Thread, ThreadList } from "./types";
import { api, send } from "./api";

type ServerEvent = { type: string };

// One event stream per tab, shared by every listener. EventSource reconnects
// by itself; listeners hear "reconnected" so they can catch up.
let source: EventSource | undefined;
const listeners = new Set<(event: ServerEvent) => void>();
function onServerEvent(listener: (event: ServerEvent) => void) {
  if (!source && typeof EventSource !== "undefined") {
    let opened = false;
    source = new EventSource("/api/events");
    source.onopen = () => {
      if (opened) listeners.forEach((l) => l({ type: "reconnected" }));
      opened = true;
    };
    source.onmessage = (message) => {
      try {
        const event = JSON.parse(message.data) as ServerEvent;
        listeners.forEach((l) => l(event));
      } catch {
        // Ignore events this version does not understand.
      }
    };
  }
  listeners.add(listener);
  return () => void listeners.delete(listener);
}

export interface CommentTarget {
  repository: string;
  base: string;
  head: string;
  // Comments on the whole comparison follow the branch as it moves.
  branch?: string;
}

/** The threads placed in a comparison, kept current as comments change. */
export function useThreads(target?: CommentTarget) {
  const [list, setList] = useState<ThreadList>();
  const [error, setError] = useState("");
  const [version, setVersion] = useState(0);
  const key = target
    ? [target.repository, target.base, target.head, target.branch ?? ""].join(
        "\0",
      )
    : "";
  useEffect(() => {
    setList(undefined);
    setError("");
  }, [key]);
  useEffect(() => {
    if (!target) return;
    const controller = new AbortController();
    api<ThreadList>(
      "threads",
      {
        repo: target.repository,
        base: target.base,
        head: target.head,
        branch: target.branch ?? "",
      },
      controller.signal,
    )
      .then((next) => {
        setList(next);
        setError("");
      })
      .catch((e) => {
        if (!controller.signal.aborted) setError(e.message);
      });
    return () => controller.abort();
  }, [key, version]);
  useEffect(
    () =>
      onServerEvent((event) => {
        if (event.type === "comments" || event.type === "reconnected")
          setVersion((v) => v + 1);
      }),
    [],
  );
  return { list, error, reload: () => setVersion((v) => v + 1) };
}

export function threadsByFile(threads: Thread[] = []) {
  const byFile = new Map<string, Thread[]>();
  for (const thread of threads) {
    const path = thread.placement?.file || thread.path;
    if (!path) continue;
    byFile.set(path, [...(byFile.get(path) ?? []), thread]);
  }
  return byFile;
}

export const openThreads = (threads: Thread[] = []) =>
  threads.filter((t) => !t.resolvedAt).length;

function formatTime(iso: string) {
  const date = new Date(iso);
  return Number.isNaN(date.getTime())
    ? iso
    : date.toLocaleString(undefined, {
        month: "short",
        day: "numeric",
        hour: "numeric",
        minute: "2-digit",
      });
}

export function lineLabel(thread: Thread) {
  const start = thread.placement?.startLine ?? thread.startLine;
  const end = thread.placement?.endLine ?? thread.endLine;
  if (!thread.path) return "This comparison";
  if (!start) return "This file";
  const lines = start === end ? `line ${start}` : `lines ${start}–${end}`;
  return thread.side === "old" ? `Old ${lines}` : `L${lines.slice(1)}`;
}

export function Composer({
  placeholder,
  submitLabel,
  initial = "",
  autoFocus = true,
  onSubmit,
  onCancel,
}: {
  placeholder: string;
  submitLabel: string;
  initial?: string;
  autoFocus?: boolean;
  onSubmit: (body: string) => Promise<unknown>;
  onCancel?: () => void;
}) {
  const [body, setBody] = useState(initial);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const input = useRef<HTMLTextAreaElement>(null);
  useEffect(() => {
    if (autoFocus) input.current?.focus();
  }, []);
  async function submit() {
    if (!body.trim() || busy) return;
    setBusy(true);
    setError("");
    try {
      await onSubmit(body);
      setBody("");
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <form
      className="comment-composer"
      onSubmit={(e) => {
        e.preventDefault();
        void submit();
      }}
    >
      <textarea
        ref={input}
        aria-label={placeholder}
        placeholder={placeholder}
        value={body}
        rows={3}
        onChange={(e) => setBody(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
            e.preventDefault();
            void submit();
          } else if (e.key === "Escape" && onCancel) {
            e.preventDefault();
            onCancel();
          }
        }}
      />
      {error && <div className="comment-error">{error}</div>}
      <div className="composer-actions">
        {onCancel && (
          <button type="button" className="button subtle" onClick={onCancel}>
            Cancel
          </button>
        )}
        <button
          className="button primary"
          disabled={busy || !body.trim()}
          title="Ctrl+Enter or ⌘+Enter"
        >
          {busy && <LoaderCircle size={14} className="spin" />}
          {submitLabel}
        </button>
      </div>
    </form>
  );
}

/** Start a thread on lines of a file, a whole file, or the whole comparison. */
export function createThread(
  target: CommentTarget,
  author: string,
  body: string,
  anchor: { path?: string; side?: Side; startLine?: number; endLine?: number },
) {
  return send<Thread>(
    "POST",
    "threads",
    {
      base: target.base,
      head: target.head,
      branch: target.branch ?? "",
      path: anchor.path ?? "",
      side: anchor.side ?? "",
      startLine: anchor.startLine ?? 0,
      endLine: anchor.endLine ?? 0,
      body,
      author,
    },
    { repo: target.repository },
  );
}

export function ThreadCard({
  thread,
  author,
  onChanged,
  showSnippet = false,
  onOpen,
}: {
  thread: Thread;
  author: string;
  onChanged: () => void;
  showSnippet?: boolean;
  onOpen?: () => void;
}) {
  const resolved = !!thread.resolvedAt;
  const [expanded, setExpanded] = useState(!resolved);
  const [replying, setReplying] = useState(false);
  const [editing, setEditing] = useState(0);
  const [error, setError] = useState("");
  useEffect(() => setExpanded(!resolved), [resolved]);
  async function act(request: () => Promise<unknown>) {
    setError("");
    try {
      await request();
      onChanged();
    } catch (e) {
      setError((e as Error).message);
    }
  }
  const outdated = thread.placement?.status === "outdated";
  const first = thread.comments[0];
  if (!expanded)
    return (
      <div className="thread-card collapsed">
        <button className="thread-summary" onClick={() => setExpanded(true)}>
          <Check size={13} className="added" />
          Resolved{thread.resolvedBy ? ` by ${thread.resolvedBy}` : ""}
          <span>
            {first?.author}: {first?.body}
          </span>
          <small>
            {thread.comments.length}{" "}
            {thread.comments.length === 1 ? "comment" : "comments"}
          </small>
        </button>
      </div>
    );
  return (
    <div className={`thread-card${resolved ? " resolved" : ""}`}>
      {(showSnippet || outdated) && (
        <div className="thread-context">
          <span className="thread-location">
            {onOpen ? (
              <button className="link-button" onClick={onOpen}>
                <FileCode2 size={12} />
                {thread.path}
              </button>
            ) : null}
            {lineLabel(thread)}
            {outdated && <span className="thread-tag">Outdated</span>}
            {thread.placement?.status === "moved" && (
              <span
                className="thread-tag moved"
                title={`Written on line ${thread.startLine}`}
              >
                Moved
              </span>
            )}
          </span>
          {thread.snippet && (
            <pre className="thread-snippet">{thread.snippet}</pre>
          )}
        </div>
      )}
      {!showSnippet && !outdated && thread.startLine !== thread.endLine && (
        <div className="thread-context">
          <span className="thread-location">{lineLabel(thread)}</span>
        </div>
      )}
      <ol className="thread-comments">
        {thread.comments.map((comment) => (
          <li key={comment.id} className={`comment ${comment.authorKind}`}>
            <header>
              {comment.authorKind === "agent" && <Bot size={14} />}
              <strong>{comment.author}</strong>
              {comment.authorKind === "agent" && (
                <span className="agent-badge">Agent</span>
              )}
              <time dateTime={comment.createdAt}>
                {formatTime(comment.createdAt)}
              </time>
              {comment.editedAt && <small>edited</small>}
              {comment.authorKind === "user" && editing !== comment.id && (
                <span className="comment-actions">
                  <button
                    className="icon-button"
                    aria-label="Edit comment"
                    title="Edit"
                    onClick={() => setEditing(comment.id)}
                  >
                    <Pencil size={12} />
                  </button>
                  <button
                    className="icon-button"
                    aria-label="Delete comment"
                    title="Delete"
                    onClick={() => {
                      if (confirm("Delete this comment?"))
                        void act(() =>
                          send("DELETE", `comments/${comment.id}`),
                        );
                    }}
                  >
                    <Trash2 size={12} />
                  </button>
                </span>
              )}
            </header>
            {editing === comment.id ? (
              <Composer
                placeholder="Edit your comment"
                submitLabel="Save"
                initial={comment.body}
                onCancel={() => setEditing(0)}
                onSubmit={async (body) => {
                  await send("PATCH", `comments/${comment.id}`, { body });
                  setEditing(0);
                  onChanged();
                }}
              />
            ) : (
              <div className="comment-body">{comment.body}</div>
            )}
          </li>
        ))}
      </ol>
      {error && <div className="comment-error">{error}</div>}
      {replying ? (
        <Composer
          placeholder="Reply…"
          submitLabel="Reply"
          onCancel={() => setReplying(false)}
          onSubmit={async (body) => {
            await send("POST", `threads/${thread.id}/comments`, {
              body,
              author,
            });
            setReplying(false);
            onChanged();
          }}
        />
      ) : (
        <div className="thread-actions">
          <button
            className="reply-placeholder"
            onClick={() => setReplying(true)}
          >
            Reply…
          </button>
          <button
            className="button subtle"
            onClick={() =>
              void act(() =>
                send("PATCH", `threads/${thread.id}`, {
                  resolved: !resolved,
                  by: author,
                }),
              )
            }
          >
            {resolved ? <RotateCcw size={13} /> : <Check size={13} />}
            {resolved ? "Reopen" : "Resolve"}
          </button>
        </div>
      )}
    </div>
  );
}

export function CommentsPanel({
  list,
  error,
  target,
  files,
  onChanged,
  onOpenFile,
}: {
  list?: ThreadList;
  error: string;
  target: CommentTarget;
  files: ChangedFile[];
  onChanged: () => void;
  onOpenFile: (path: string) => void;
}) {
  const [showResolved, setShowResolved] = useState(false);
  if (error)
    return (
      <div className="patch-message error-message conversations-message">
        {error}
      </div>
    );
  if (!list) return <div className="loading-state">Loading comments…</div>;
  const author = list.author || "You";
  const shown = list.threads.filter((t) => showResolved || !t.resolvedAt);
  const general = shown.filter((t) => !t.path);
  const order = new Map(files.map((f, i) => [f.path, i]));
  const byFile = [...threadsByFile(shown)].sort(
    ([a], [b]) => (order.get(a) ?? 0) - (order.get(b) ?? 0),
  );
  const resolvedCount = list.threads.filter((t) => t.resolvedAt).length;
  return (
    <section className="conversations comments-panel" aria-label="Comments">
      <div className="conversations-toolbar">
        <strong>
          {openThreads(list.threads)} open{" "}
          {openThreads(list.threads) === 1 ? "thread" : "threads"}
        </strong>
        <span className="conversation-filter">
          On the files in this comparison. Hover a line in the diff and choose +
          to comment on it.
        </span>
        <label className="conversations-toggle">
          <input
            type="checkbox"
            checked={showResolved}
            onChange={(e) => setShowResolved(e.target.checked)}
          />
          Show resolved ({resolvedCount})
        </label>
      </div>
      <div className="comment-group">
        <h3>
          <MessageSquare size={14} />
          This comparison
        </h3>
        {general.map((thread) => (
          <ThreadCard
            key={thread.id}
            thread={thread}
            author={author}
            onChanged={onChanged}
          />
        ))}
        <Composer
          placeholder="Comment on the whole comparison…"
          submitLabel="Comment"
          autoFocus={false}
          onSubmit={async (body) => {
            await createThread(target, author, body, {});
            onChanged();
          }}
        />
      </div>
      {byFile.map(([path, threads]) => (
        <div className="comment-group" key={path}>
          <h3>
            <button className="link-button" onClick={() => onOpenFile(path)}>
              <FileCode2 size={14} />
              {path}
            </button>
          </h3>
          {threads.map((thread) => (
            <ThreadCard
              key={thread.id}
              thread={thread}
              author={author}
              onChanged={onChanged}
              showSnippet
            />
          ))}
        </div>
      ))}
    </section>
  );
}
