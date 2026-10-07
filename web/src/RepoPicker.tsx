import React, { useEffect, useRef, useState } from "react";
import {
  ArrowUp,
  Clock,
  Folder,
  FolderGit2,
  House,
  LoaderCircle,
  X,
} from "lucide-react";
import { api } from "./api";
import type { Listing } from "./types";

const recentKey = "differ:recent-repos";

export function recentRepositories(): string[] {
  try {
    const saved = JSON.parse(localStorage.getItem(recentKey) || "[]");
    return Array.isArray(saved)
      ? saved.filter((p) => typeof p === "string")
      : [];
  } catch {
    return [];
  }
}
export function rememberRepository(path: string) {
  try {
    const next = [path, ...recentRepositories().filter((p) => p !== path)];
    localStorage.setItem(recentKey, JSON.stringify(next.slice(0, 8)));
  } catch {
    /* Recent repositories are a convenience only. */
  }
}
const parentOf = (path: string) =>
  path.replace(/[\\/][^\\/]*$/, "") || path.slice(0, 1);

export function RepoPicker({
  current,
  onChoose,
  onClose,
}: {
  current: string;
  onChoose: (path: string) => void;
  onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [listing, setListing] = useState<Listing>();
  const [path, setPath] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const request = useRef<AbortController | undefined>(undefined);
  const recent = recentRepositories();

  function open(target: string) {
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    setLoading(true);
    setError("");
    api<Listing>("browse", { path: target }, controller.signal)
      .then((result) => {
        setListing(result);
        setPath(result.path);
      })
      .catch((e) => {
        if (!controller.signal.aborted) setError(e.message);
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
  }
  useEffect(() => {
    dialog.current?.showModal();
    // Start beside the current repository so sibling checkouts are one click away.
    open(current ? parentOf(current) : "");
    return () => request.current?.abort();
  }, []);

  return (
    <dialog
      ref={dialog}
      className="repo-picker"
      aria-label="Choose a repository"
      onClose={onClose}
      onClick={(e) => {
        if (e.target === dialog.current) dialog.current.close();
      }}
    >
      <header>
        <h2>Choose a repository</h2>
        <button
          className="icon-button"
          aria-label="Close"
          onClick={() => dialog.current?.close()}
        >
          <X size={16} />
        </button>
      </header>
      {!!recent.length && (
        <section className="picker-recent">
          <div className="sidebar-label">RECENT</div>
          {recent.map((repo) => (
            <button
              key={repo}
              className="picker-row repository"
              title={repo}
              onClick={() => onChoose(repo)}
            >
              <Clock size={15} />
              <span>{repo}</span>
            </button>
          ))}
        </section>
      )}
      <form
        className="picker-path"
        onSubmit={(e) => {
          e.preventDefault();
          open(path);
        }}
      >
        <button
          type="button"
          className="icon-button bordered"
          aria-label="Parent folder"
          title="Parent folder"
          disabled={!listing?.parent}
          onClick={() => listing?.parent && open(listing.parent)}
        >
          <ArrowUp size={15} />
        </button>
        <button
          type="button"
          className="icon-button bordered"
          aria-label="Home folder"
          title="Home folder"
          onClick={() => open("")}
        >
          <House size={15} />
        </button>
        <div className="input-wrap">
          <Folder size={15} />
          <input
            aria-label="Folder path"
            value={path}
            onChange={(e) => setPath(e.target.value)}
            spellCheck={false}
          />
        </div>
        <button className="button">Go</button>
      </form>
      {error && <div className="error">{error}</div>}
      <nav className="picker-list" aria-label="Folders" aria-busy={loading}>
        {loading && !listing ? (
          <div className="loading-state">
            <LoaderCircle size={18} className="spin" />
          </div>
        ) : (
          listing?.entries.map((entry) => (
            <button
              key={entry.path}
              className={`picker-row${entry.repository ? " repository" : ""}`}
              title={
                entry.repository ? `Review ${entry.path}` : `Open ${entry.path}`
              }
              onClick={() =>
                entry.repository ? onChoose(entry.path) : open(entry.path)
              }
            >
              {entry.repository ? (
                <FolderGit2 size={15} />
              ) : (
                <Folder size={15} />
              )}
              <span>{entry.name}</span>
              {entry.repository && <small>Git repository</small>}
            </button>
          ))
        )}
        {listing && !listing.entries.length && (
          <p className="picker-empty">No folders here.</p>
        )}
        {listing?.truncated && (
          <p className="picker-empty">
            Showing the first {listing.entries.length} folders. Type a path to
            go further.
          </p>
        )}
      </nav>
      <footer>
        <span>
          {listing?.repository
            ? "This folder is a Git repository."
            : "Choose a folder marked as a Git repository."}
        </span>
        <button
          className="button primary"
          disabled={!listing?.repository}
          onClick={() => listing && onChoose(listing.path)}
        >
          <FolderGit2 size={15} />
          Review this folder
        </button>
      </footer>
    </dialog>
  );
}
