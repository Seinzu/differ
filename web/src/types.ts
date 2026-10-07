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
  relationship: "forward" | "reverse" | "equal" | "diverged" | "snapshot";
  commits: Commit[];
  files: ChangedFile[];
  additions: number;
  deletions: number;
  range?: Range;
  worktree?: boolean;
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
}
export interface RepoInfo {
  repository: string;
  name: string;
  currentBranch: string;
  defaultBranch: string;
  detached: boolean;
  dirty: boolean;
  branches: Branch[];
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
