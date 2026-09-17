export interface Commit {
  sha: string;
  parents: string[];
  subject: string;
  author: string;
  date: string;
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
  relationship: "forward" | "reverse" | "equal" | "diverged";
  commits: Commit[];
  files: ChangedFile[];
  additions: number;
  deletions: number;
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
