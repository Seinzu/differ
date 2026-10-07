import React, { useState } from "react";
import {
  Bot,
  FileCode2,
  GitBranch,
  GitCommitHorizontal,
  MessageSquare,
  Terminal,
  X,
} from "lucide-react";
import type { Commit, ConversationLink, ConversationList, Turn } from "./types";

const matchNames: Record<ConversationLink["match"], string> = {
  commit: "Committed during this turn",
  content: "Contains the exact file contents this turn produced",
  head: "HEAD while this turn ran (followed through amends and rebases)",
  path: "First later commit changing a file this turn edited",
};

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

export function Conversations({
  list,
  error,
  commits,
  commitFilter,
  onClearFilter,
  onOpenCommit,
}: {
  list?: ConversationList;
  error: string;
  commits: Commit[];
  commitFilter: string;
  onClearFilter: () => void;
  onOpenCommit: (index: number) => void;
}) {
  const [showAll, setShowAll] = useState(false);
  if (error)
    return (
      <div className="patch-message error-message conversations-message">
        {error}
      </div>
    );
  if (!list) return <div className="loading-state">Loading conversations…</div>;
  if (!list.enabled)
    return (
      <div className="empty-files conversations-setup">
        <MessageSquare size={28} />
        <h2>No conversations captured yet</h2>
        <p>
          Record your Claude Code prompts and responses for this repository,
          then review them beside the commits they produced:
        </p>
        <pre>
          <Terminal size={13} /> differ install-hooks -repo /path/to/repo
        </pre>
        <p>
          Conversations are saved to <code>{list.database}</code>.
        </p>
      </div>
    );
  const index = new Map(commits.map((c, i) => [c.sha, i]));
  const linked = list.turns.filter(
    (t) => t.onBranch || t.links.some((l) => index.has(l.sha)),
  );
  let turns = showAll ? list.turns : linked;
  if (commitFilter)
    turns = turns.filter((t) => t.links.some((l) => l.sha === commitFilter));
  const filterCommit = commits.find((c) => c.sha === commitFilter);
  return (
    <section className="conversations" aria-label="Conversations">
      <div className="conversations-toolbar">
        <strong>
          {turns.length} {turns.length === 1 ? "turn" : "turns"}
        </strong>
        {filterCommit ? (
          <span className="conversation-filter">
            for{" "}
            {filterCommit.uncommitted
              ? "uncommitted changes"
              : filterCommit.subject}
            <button
              className="icon-button"
              aria-label="Show all commits"
              onClick={onClearFilter}
            >
              <X size={13} />
            </button>
          </span>
        ) : (
          <span className="conversation-filter">
            {showAll
              ? "Every captured turn in this repository"
              : "On this branch or linked to its commits"}
          </span>
        )}
        <label className="conversations-toggle">
          <input
            type="checkbox"
            checked={showAll}
            onChange={(e) => setShowAll(e.target.checked)}
          />
          Include unlinked turns ({list.turns.length - linked.length})
        </label>
      </div>
      {!turns.length && (
        <div className="empty-files">
          <MessageSquare size={28} />
          <h2>No matching conversations</h2>
          <p>
            {list.turns.length
              ? "No captured turn links to these commits. Include unlinked turns to see them all."
              : "Hooks are installed elsewhere, but nothing has been captured in this repository yet."}
          </p>
        </div>
      )}
      <ol className="turn-list">
        {turns.map((turn) => (
          <TurnCard
            key={turn.id}
            turn={turn}
            commits={commits}
            index={index}
            onOpenCommit={onOpenCommit}
          />
        ))}
      </ol>
    </section>
  );
}

function TurnCard({
  turn,
  commits,
  index,
  onOpenCommit,
}: {
  turn: Turn;
  commits: Commit[];
  index: Map<string, number>;
  onOpenCommit: (index: number) => void;
}) {
  const [expanded, setExpanded] = useState(false);
  const long = turn.response.length > 700;
  return (
    <li className="turn-card">
      <header>
        <time dateTime={turn.promptedAt}>{formatTime(turn.promptedAt)}</time>
        {turn.branch && (
          <span className="turn-meta">
            <GitBranch size={12} />
            {turn.branch}
          </span>
        )}
        <span className="turn-meta" title={`Session ${turn.sessionId}`}>
          session {turn.sessionId.slice(0, 8)}
        </span>
        <span className="turn-links">
          {turn.onBranch && (
            <span
              className="commit-chip match-branch"
              title="Happened on this branch, at a commit that is no longer part of it"
            >
              <GitBranch size={13} />
              on {turn.branch}
            </span>
          )}
          {turn.links
            .filter((l) => index.has(l.sha))
            .map((link) => {
              const i = index.get(link.sha)!;
              const commit = commits[i];
              return (
                <button
                  key={link.sha}
                  className={`commit-chip match-${link.match}`}
                  title={`${matchNames[link.match]}. Open ${commit.uncommitted ? "the uncommitted changes" : commit.subject}.`}
                  onClick={() => onOpenCommit(i)}
                >
                  <GitCommitHorizontal size={13} />
                  {commit.uncommitted ? "working tree" : link.sha.slice(0, 7)}
                </button>
              );
            })}
        </span>
      </header>
      <div className="turn-prompt">
        {turn.prompt || <em>Prompt not captured</em>}
      </div>
      <div className={`turn-response${long && !expanded ? " clipped" : ""}`}>
        <Bot size={15} />
        <div>
          {turn.respondedAt ? (
            turn.response || <em>No text response</em>
          ) : (
            <em>Still responding, or the session ended first.</em>
          )}
        </div>
      </div>
      {long && (
        <button
          className="button subtle"
          onClick={() => setExpanded(!expanded)}
        >
          {expanded ? "Show less" : "Show the full response"}
        </button>
      )}
      {!!turn.files.length && (
        <div className="turn-files">
          {turn.files.map((f) => (
            <span
              key={f.path}
              title={f.blob ? `Ended as blob ${f.blob}` : "Deleted"}
            >
              <FileCode2 size={12} />
              {f.path}
            </span>
          ))}
        </div>
      )}
    </li>
  );
}
