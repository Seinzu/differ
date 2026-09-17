import { describe, expect, it } from "vitest";
import { changedSpan, parsePatch } from "./diff";

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
