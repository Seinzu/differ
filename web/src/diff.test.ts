import { describe, expect, it } from "vitest";
import {
  changedSpan,
  expandHunks,
  fileLines,
  parsePatch,
  toUnifiedRows,
} from "./diff";

describe("side-by-side patch parsing", () => {
  it("aligns replacement blocks of different lengths and retains line numbers", () => {
    const [hunk] = parsePatch(
      "@@ -8,4 +8,5 @@ function run()\n context\n-old\n-removed\n+new\n+extra\n+third\n tail\n",
    );
    expect(hunk.rows).toHaveLength(5);
    expect(hunk.rows[1]).toEqual({
      kind: "change",
      left: { number: 9, text: "old" },
      right: { number: 9, text: "new" },
    });
    expect(hunk.rows[3].left).toBeUndefined();
    expect(hunk.rows[4].left?.number).toBe(11);
    expect(hunk.rows[4].right?.number).toBe(12);
  });
  it("keeps EOF markers attached to the correct side", () => {
    const [hunk] = parsePatch(
      "@@ -1 +1 @@\n-before\n\\ No newline at end of file\n+after\n",
    );
    expect(hunk.rows[0].left?.noNewline).toBe(true);
    expect(hunk.rows[0].right?.noNewline).toBeUndefined();
  });
  it("handles blank additions and separate hunks without treating headers as code", () => {
    const hunks = parsePatch(
      "diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -0,0 +1,2 @@\n+\n+value\n@@ -10 +12 @@\n-old\n+new\n",
    );
    expect(hunks).toHaveLength(2);
    expect(hunks[0].rows[0].right?.text).toBe("");
    expect(hunks[1].rows[0].right?.number).toBe(12);
  });
  it("finds changed spans for replacements, insertions, and repeated text", () => {
    expect(changedSpan("const count = 1;", "const count = 12;")).toEqual([
      15, 15, 16,
    ]);
    expect(changedSpan("same", "same")).toBeNull();
    expect(changedSpan("aaa", "a")).toEqual([1, 3, 1]);
  });
});

describe("unified diff rows", () => {
  it("renders complete deletion blocks before additions, with context shown once", () => {
    const [hunk] = parsePatch(
      "@@ -8,4 +8,5 @@\n context\n-old\n-removed\n+new\n+extra\n+third\n tail\n",
    );
    const rows = toUnifiedRows(hunk.rows);
    expect(
      rows.map((row) => [
        row.kind,
        row.line.text,
        row.oldNumber,
        row.newNumber,
      ]),
    ).toEqual([
      ["context", "context", 8, 8],
      ["deletion", "old", 9, undefined],
      ["deletion", "removed", 10, undefined],
      ["addition", "new", undefined, 9],
      ["addition", "extra", undefined, 10],
      ["addition", "third", undefined, 11],
      ["context", "tail", 11, 12],
    ]);
    expect(rows[1].other?.text).toBe("new");
    expect(rows[3].other?.text).toBe("old");
    expect(rows[5].other).toBeUndefined();
  });

  it("keeps separate change blocks in place and preserves EOF markers", () => {
    const [hunk] = parsePatch(
      "@@ -1,3 +1,3 @@\n-before\n+after\n unchanged\n-old end\n\\ No newline at end of file\n+new end\n",
    );
    const rows = toUnifiedRows(hunk.rows);
    expect(rows.map((row) => row.line.text)).toEqual([
      "before",
      "after",
      "unchanged",
      "old end",
      "new end",
    ]);
    expect(rows[3].line.noNewline).toBe(true);
    expect(rows[4].line.noNewline).toBeUndefined();
    expect(rows[2]).toMatchObject({
      kind: "context",
      oldNumber: 2,
      newNumber: 2,
    });
  });

  it("handles added and deleted files, blank lines, and multiple hunks", () => {
    const [added] = parsePatch("@@ -0,0 +1,2 @@\n+\n+value\n");
    expect(
      toUnifiedRows(added.rows).map((row) => [
        row.kind,
        row.line.text,
        row.oldNumber,
        row.newNumber,
      ]),
    ).toEqual([
      ["addition", "", undefined, 1],
      ["addition", "value", undefined, 2],
    ]);
    const hunks = parsePatch(
      "@@ -1 +0,0 @@\n-gone\n@@ -10 +8,0 @@\n-also gone\n",
    );
    expect(hunks.map((hunk) => toUnifiedRows(hunk.rows)[0])).toMatchObject([
      { kind: "deletion", oldNumber: 1, line: { text: "gone" } },
      { kind: "deletion", oldNumber: 10, line: { text: "also gone" } },
    ]);
    expect(toUnifiedRows([])).toEqual([]);
  });
});

describe("expanding context", () => {
  const file = Array.from({ length: 60 }, (_, i) => `line ${i + 1}`);
  // Line 10 changed, line 50 deleted.
  const patch =
    "@@ -9,3 +9,3 @@ top\n line 9\n-old 10\n+line 10\n line 11\n" +
    "@@ -48,5 +48,4 @@ bottom\n line 48\n line 49\n-gone\n line 50\n line 51\n";
  const hunks = parsePatch(patch);
  const content = {
    old: [
      ...file.slice(0, 9),
      "old 10",
      ...file.slice(10, 49),
      "gone",
      ...file.slice(49),
    ],
    new: file,
  };

  it("records where each hunk starts and ends", () => {
    expect(
      hunks.map((h) => [h.oldStart, h.oldEnd, h.newStart, h.newEnd]),
    ).toEqual([
      [9, 12, 9, 12],
      [48, 53, 48, 52],
    ]);
    const [added] = parsePatch("@@ -0,0 +1,2 @@\n+a\n+b\n");
    expect([
      added.oldStart,
      added.oldEnd,
      added.newStart,
      added.newEnd,
    ]).toEqual([1, 1, 1, 3]);
  });

  it("describes the hidden gaps without content", () => {
    const blocks = expandHunks(hunks, {});
    expect(blocks.map((b) => b.gap)).toEqual([
      { index: 0, hidden: 8, down: false, up: true },
      { index: 1, hidden: 36, down: true, up: true },
      { index: 2, hidden: undefined, down: true, up: false },
    ]);
    expect(blocks[0].heading).toBe("@@ -9,3 +9,3 @@ top");
    expect(expandHunks(hunks, {}, undefined, true)).toHaveLength(2);
  });

  it("reveals lines from both ends of a gap with matching numbers", () => {
    const blocks = expandHunks(hunks, { 1: { top: 2, bottom: 3 } }, content);
    const first = blocks[0].rows;
    expect(first.at(-1)).toEqual({
      kind: "context",
      left: { number: 13, text: "line 13" },
      right: { number: 13, text: "line 13" },
    });
    expect(blocks[1].gap?.hidden).toBe(31);
    expect(blocks[1].rows[0].right).toEqual({ number: 45, text: "line 45" });
    expect(blocks[1].heading).toBe("@@ -45,8 +45,7 @@ bottom");
    // After the deletion, old numbers run one ahead.
    const trailing = expandHunks(hunks, { 2: { top: 1, bottom: 0 } }, content);
    expect(trailing[1].rows.at(-1)?.left).toEqual({
      number: 53,
      text: "line 52",
    });
    expect(trailing[2].gap?.hidden).toBe(8);
  });

  it("joins hunks once the gap between them is fully shown", () => {
    const blocks = expandHunks(
      hunks,
      {
        0: { top: 0, bottom: 100 },
        1: { top: 40, bottom: 0 },
        2: { top: 100, bottom: 0 },
      },
      content,
    );
    expect(blocks).toHaveLength(1);
    expect(blocks[0].gap).toBeUndefined();
    expect(blocks[0].rows.map((r) => r.right?.number).filter(Boolean)).toEqual(
      file.map((_, i) => i + 1),
    );
    expect(blocks[0].heading).toBe("@@ -1,61 +1,60 @@ top");
  });

  it("splits file text into lines", () => {
    expect(fileLines("")).toEqual([]);
    expect(fileLines("a\nb\n")).toEqual(["a", "b"]);
    expect(fileLines("a\nb")).toEqual(["a", "b"]);
  });
});
