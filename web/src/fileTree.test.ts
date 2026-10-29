import { describe, expect, it } from "vitest";
import { buildFileTree, splitPath, treeFiles } from "./fileTree";

const files = (...paths: string[]) => paths.map((path) => ({ path }));

describe("file tree", () => {
  it("groups files by directory and compacts single-child chains", () => {
    const tree = buildFileTree(
      files(
        "README.md",
        "internal/server/mcp.go",
        "internal/server/server.go",
        "web/src/main.tsx",
      ),
    );
    expect(tree.map((node) => node.name)).toEqual([
      "README.md",
      "internal/server",
      "web/src",
    ]);
    const server = tree[1];
    expect(server.kind === "dir" && server.path).toBe("internal/server");
    expect(server.kind === "dir" && server.fileCount).toBe(2);
    expect(
      server.kind === "dir" && server.children.map((node) => node.name),
    ).toEqual(["mcp.go", "server.go"]);
  });
  it("stops compacting where a directory branches or holds files", () => {
    const tree = buildFileTree(
      files("a/b/c/one.go", "a/b/d/two.go", "a/b/three.go"),
    );
    expect(tree).toHaveLength(1);
    const ab = tree[0];
    expect(ab.name).toBe("a/b");
    expect(ab.kind === "dir" && ab.children.map((node) => node.name)).toEqual([
      "c",
      "d",
      "three.go",
    ]);
  });
  it("lists files in tree order", () => {
    const tree = buildFileTree(files("x/1", "y/2", "x/3"));
    expect(treeFiles(tree).map((file) => file.path)).toEqual([
      "x/1",
      "x/3",
      "y/2",
    ]);
  });
  it("splits paths into directory and name", () => {
    expect(splitPath("a/b/c.go")).toEqual({ dir: "a/b/", name: "c.go" });
    expect(splitPath("c.go")).toEqual({ dir: "", name: "c.go" });
  });
});
