import React from "react";
import {
  Check,
  ChevronDown,
  ChevronRight,
  FileCode2,
  Folder,
  FolderOpen,
  MessageSquare,
} from "lucide-react";
import type { ChangedFile } from "./types";
import { buildFileTree, splitPath, type TreeNode } from "./fileTree";

export type FileView = "tree" | "list";

interface Props {
  files: ChangedFile[];
  view: FileView;
  activePath: string;
  reviewed: Set<string>;
  openThreadCount: (path: string) => number;
  onSelect: (path: string) => void;
  /** Directory paths the reviewer has folded away. */
  collapsedDirs: Set<string>;
  onToggleDir: (path: string) => void;
  /** Show every directory open, e.g. while a filter narrows the list. */
  expandAll: boolean;
}

export function FileNav(props: Props) {
  if (props.view === "list")
    return (
      <nav aria-label="Changed files" className="file-nav-list">
        {props.files.map((file) => (
          <FileRow key={file.path} {...props} file={file} depth={0} showDir />
        ))}
      </nav>
    );
  return (
    <nav aria-label="Changed files" className="file-tree">
      <TreeRows {...props} nodes={buildFileTree(props.files)} depth={0} />
    </nav>
  );
}

function TreeRows(
  props: Props & { nodes: TreeNode<ChangedFile>[]; depth: number },
) {
  const { nodes, depth } = props;
  return nodes.map((node) => {
    if (node.kind === "file")
      return (
        <FileRow
          key={node.file.path}
          {...props}
          file={node.file}
          depth={depth}
        />
      );
    const open = props.expandAll || !props.collapsedDirs.has(node.path);
    const holdsActive = props.activePath.startsWith(`${node.path}/`);
    return (
      <React.Fragment key={node.path}>
        <button
          className={`file-nav dir-nav${!open && holdsActive ? " active" : ""}`}
          style={{ paddingLeft: 7 + depth * 12 }}
          title={node.path}
          aria-expanded={open}
          onClick={() => props.onToggleDir(node.path)}
        >
          {open ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
          {open ? <FolderOpen size={14} /> : <Folder size={14} />}
          <span>{node.name}</span>
          {!open && <span className="dir-count">{node.fileCount}</span>}
        </button>
        {open && (
          <TreeRows {...props} nodes={node.children} depth={depth + 1} />
        )}
      </React.Fragment>
    );
  });
}

function FileRow({
  file,
  depth,
  showDir,
  activePath,
  reviewed,
  openThreadCount,
  onSelect,
}: Props & { file: ChangedFile; depth: number; showDir?: boolean }) {
  const { dir, name } = splitPath(file.path);
  const threads = openThreadCount(file.path);
  return (
    <button
      className={`file-nav ${activePath === file.path ? "active" : ""}`}
      // Files sit under their directory's chevron, so indent past it.
      style={depth ? { paddingLeft: 7 + depth * 12 + 13 } : undefined}
      title={file.path}
      onClick={() => onSelect(file.path)}
    >
      <FileCode2 size={15} />
      <span className="file-nav-name">
        <span className="file-name">{name}</span>
        {showDir && dir && <span className="file-dir">{dir.slice(0, -1)}</span>}
      </span>
      {!!threads && (
        <span className="comment-count" title="Open comment threads">
          <MessageSquare size={11} />
          {threads}
        </span>
      )}
      {reviewed.has(file.path) ? (
        <Check size={13} className="added" />
      ) : (
        <span className={`file-status status-${file.status}`}>
          {file.status}
        </span>
      )}
    </button>
  );
}
