export interface TreeFile<F> {
  kind: "file";
  name: string;
  file: F;
}
export interface TreeDir<F> {
  kind: "dir";
  /** Display name; chains of single-child directories are joined with "/". */
  name: string;
  /** Full directory path, used as a stable key for collapse state. */
  path: string;
  children: TreeNode<F>[];
  fileCount: number;
}
export type TreeNode<F> = TreeFile<F> | TreeDir<F>;

/**
 * Group changed files into a directory tree. Children keep the order the
 * files arrived in, which for Git's path-sorted output matches the order of
 * the diffs. Directories holding only one subdirectory are compacted into a
 * single row ("internal/server") so deep paths stay short.
 */
export function buildFileTree<F extends { path: string }>(
  files: F[],
): TreeNode<F>[] {
  const root: TreeDir<F> = {
    kind: "dir",
    name: "",
    path: "",
    children: [],
    fileCount: 0,
  };
  for (const file of files) {
    const parts = file.path.split("/");
    let dir = root;
    dir.fileCount++;
    for (const part of parts.slice(0, -1)) {
      const path = dir.path ? `${dir.path}/${part}` : part;
      let next = dir.children.find(
        (node): node is TreeDir<F> => node.kind === "dir" && node.path === path,
      );
      if (!next) {
        next = { kind: "dir", name: part, path, children: [], fileCount: 0 };
        dir.children.push(next);
      }
      next.fileCount++;
      dir = next;
    }
    dir.children.push({ kind: "file", name: parts[parts.length - 1], file });
  }
  return root.children.map(compact);
}

function compact<F>(node: TreeNode<F>): TreeNode<F> {
  if (node.kind === "file") return node;
  let dir = node;
  let name = dir.name;
  while (dir.children.length === 1 && dir.children[0].kind === "dir") {
    dir = dir.children[0];
    name = `${name}/${dir.name}`;
  }
  return { ...dir, name, children: dir.children.map(compact) };
}

/** Files in the order the tree shows them. */
export function treeFiles<F>(nodes: TreeNode<F>[]): F[] {
  return nodes.flatMap((node) =>
    node.kind === "file" ? [node.file] : treeFiles(node.children),
  );
}

/** Split a path into its directory (with trailing slash) and file name. */
export function splitPath(path: string): { dir: string; name: string } {
  const slash = path.lastIndexOf("/");
  return { dir: path.slice(0, slash + 1), name: path.slice(slash + 1) };
}
