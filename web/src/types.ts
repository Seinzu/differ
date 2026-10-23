export interface Commit {
  sha: string;
  parents: string[];
  subject: string;
  author: string;
  date: string;
  uncommitted?: boolean;
}
export interface ChangedFile {
  path: string;
  oldPath: string;
  status: string;
  oldMode: string;
  newMode: string;
  additions: number;
  deletions: number;
  binary: boolean;
}
export interface Comparison {
  repository: string;
  name: string;
  base: string;
  head: string;
  relationship:
    "forward" | "reverse" | "equal" | "diverged" | "snapshot" | "remerge";
  commits: Commit[];
  files: ChangedFile[];
  additions: number;
  deletions: number;
  range?: Range;
  worktree?: boolean;
  worktreePath?: string;
  conflicts?: string[];
}
export interface Patch {
  patch: string;
  binary: boolean;
  tooLarge: boolean;
  message?: string;
}
export interface Config {
  repository: string;
  base: string;
  head: string;
}
export interface Branch {
  name: string;
  sha: string;
  subject: string;
  date: string;
  current: boolean;
  worktree?: string;
}
export interface Checkout {
  path: string;
  branch: string;
  head: string;
  detached: boolean;
  dirty: boolean;
  current: boolean;
}
export interface RepoInfo {
  repository: string;
  name: string;
  bare: boolean;
  currentBranch: string;
  defaultBranch: string;
  detached: boolean;
  dirty: boolean;
  branches: Branch[];
  worktrees: Checkout[];
}
export interface Range {
  mode: "branch" | "recent";
  branch: string;
  defaultBranch: string;
  count: number;
  available: number;
  base: string;
  head: string;
}
export interface DirEntry {
  name: string;
  path: string;
  repository: boolean;
}
export interface Listing {
  path: string;
  parent: string;
  home: string;
  repository: boolean;
  entries: DirEntry[];
  truncated: boolean;
}
export interface ConversationLink {
  sha: string;
  match: "commit" | "content" | "head" | "path";
}
export interface Turn {
  id: number;
  sessionId: string;
  worktree: string;
  branch: string;
  headBefore: string;
  headAfter: string;
  prompt: string;
  response: string;
  files: { path: string; blob: string }[];
  promptedAt: string;
  respondedAt?: string;
  links: ConversationLink[];
  onBranch: boolean;
}
export interface ConversationList {
  enabled: boolean;
  database: string;
  turns: Turn[];
}
export interface ReviewComment {
  id: number;
  threadId: number;
  author: string;
  authorKind: "user" | "agent";
  body: string;
  createdAt: string;
  editedAt?: string;
}
export type Side = "old" | "new";
export interface Placement {
  status: "exact" | "moved" | "outdated";
  // The file's path in the comparison (its new path when renamed).
  file?: string;
  startLine?: number;
  endLine?: number;
}
export interface Thread {
  id: number;
  // Empty for a comment on the whole comparison.
  path: string;
  side: Side | "";
  // Zero for a comment on the whole file.
  startLine: number;
  endLine: number;
  snippet: string;
  base: string;
  head: string;
  branch: string;
  createdAt: string;
  resolvedAt?: string;
  resolvedBy?: string;
  comments: ReviewComment[];
  placement?: Placement;
}
export interface ThreadList {
  author: string;
  threads: Thread[];
}
