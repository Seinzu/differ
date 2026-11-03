export interface Line {
  number: number;
  text: string;
  noNewline?: boolean;
}
export interface Row {
  left?: Line;
  right?: Line;
  kind: "context" | "change";
}
export interface Hunk {
  heading: string;
  rows: Row[];
  // First line number on each side, and the number just past the hunk's
  // last line, so the unchanged lines between hunks can be located.
  oldStart: number;
  newStart: number;
  oldEnd: number;
  newEnd: number;
  // The text after the closing @@, usually the enclosing function.
  section: string;
}

export interface UnifiedRow {
  kind: "context" | "deletion" | "addition";
  line: Line;
  oldNumber?: number;
  newNumber?: number;
  other?: Line;
}

/** Keep each change block in patch order: all deletions, then all additions. */
export function toUnifiedRows(rows: Row[]): UnifiedRow[] {
  const result: UnifiedRow[] = [];
  let changes: Row[] = [];
  function flush() {
    for (const row of changes) {
      if (row.left)
        result.push({
          kind: "deletion",
          line: row.left,
          oldNumber: row.left.number,
          other: row.right,
        });
    }
    for (const row of changes) {
      if (row.right)
        result.push({
          kind: "addition",
          line: row.right,
          newNumber: row.right.number,
          other: row.left,
        });
    }
    changes = [];
  }
  for (const row of rows) {
    if (row.kind === "change") {
      changes.push(row);
    } else {
      flush();
      const line = row.right ?? row.left;
      if (line)
        result.push({
          kind: "context",
          line,
          oldNumber: row.left?.number,
          newNumber: row.right?.number,
        });
    }
  }
  flush();
  return result;
}

/** Align each contiguous deletion/addition group, preserving blank lines and EOF markers. */
export function parsePatch(patch: string): Hunk[] {
  const hunks: Hunk[] = [];
  let hunk: Hunk | undefined;
  let oldNumber = 0,
    newNumber = 0;
  let removed: Line[] = [],
    added: Line[] = [];
  let lastLines: Line[] = [];
  function finish() {
    if (!hunk) return;
    hunk.oldEnd = oldNumber;
    hunk.newEnd = newNumber;
  }
  function flush() {
    if (!hunk) return;
    for (let i = 0; i < Math.max(removed.length, added.length); i++) {
      hunk.rows.push({ kind: "change", left: removed[i], right: added[i] });
    }
    removed = [];
    added = [];
  }
  for (const line of patch.split("\n")) {
    const header = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(.*)$/.exec(
      line,
    );
    if (header) {
      flush();
      finish();
      // A side with no lines names the line before the hunk.
      oldNumber = Number(header[1]) + (header[2] === "0" ? 1 : 0);
      newNumber = Number(header[3]) + (header[4] === "0" ? 1 : 0);
      hunk = {
        heading: line,
        rows: [],
        oldStart: oldNumber,
        newStart: newNumber,
        oldEnd: oldNumber,
        newEnd: newNumber,
        section: header[5],
      };
      hunks.push(hunk);
      lastLines = [];
      continue;
    }
    if (!hunk) continue;
    if (line.startsWith("\\ No newline")) {
      lastLines.forEach((l) => {
        l.noNewline = true;
      });
      continue;
    }
    if (line.startsWith("-")) {
      const l = { number: oldNumber++, text: line.slice(1) };
      removed.push(l);
      lastLines = [l];
    } else if (line.startsWith("+")) {
      const l = { number: newNumber++, text: line.slice(1) };
      added.push(l);
      lastLines = [l];
    } else if (line.startsWith(" ")) {
      flush();
      const left = { number: oldNumber++, text: line.slice(1) },
        right = { number: newNumber++, text: line.slice(1) };
      hunk.rows.push({ kind: "context", left, right });
      lastLines = [left, right];
    }
  }
  flush();
  finish();
  return hunks;
}

// How many hidden lines have been revealed in each gap: from its top (just
// below the previous hunk) and from its bottom (just above the next one).
// Gap i is the one before hunk i; gap hunks.length follows the last hunk.
export type Expansions = Record<number, { top: number; bottom: number }>;

/** The unchanged lines hidden in a gap, or undefined while the file length is unknown. */
export interface Gap {
  index: number;
  hidden?: number;
  // Which ways the gap can be opened: down from the hunk above, up from the hunk below.
  down: boolean;
  up: boolean;
}

export interface ExpandedHunk extends Hunk {
  // The hidden lines just above this block, shown as an expander row.
  gap?: Gap;
}

export const expandStep = 20;

/** Split file text into lines the way a patch counts them. */
export function fileLines(text: string): string[] {
  if (!text) return [];
  return text.replace(/\n$/, "").split("\n");
}

/**
 * Merge revealed unchanged lines into the patch's hunks. Blocks whose gap has
 * been fully revealed join the block above; a final block with no rows holds
 * the gap after the last hunk. Without file content (before it is loaded) the
 * size of the trailing gap is unknown, so endOfFile says whether the patch
 * already reaches the end of the file.
 */
export function expandHunks(
  hunks: Hunk[],
  expansions: Expansions,
  content?: { old: string[]; new: string[] },
  endOfFile = false,
): ExpandedHunk[] {
  if (!hunks.length) return [];
  const result: ExpandedHunk[] = [];
  // Blocks whose extent changed get a recomputed heading.
  const changed = new Set<ExpandedHunk>();
  const contextRow = (oldNumber: number, newNumber: number): Row => ({
    kind: "context",
    left: {
      number: oldNumber,
      text: content?.old[oldNumber - 1] ?? content?.new[newNumber - 1] ?? "",
    },
    right: { number: newNumber, text: content?.new[newNumber - 1] ?? "" },
  });
  for (let i = 0; i <= hunks.length; i++) {
    const above = hunks[i - 1],
      below = hunks[i];
    // Gap lines are numbered from the new side; old numbers keep the offset
    // of the hunk they are attached to.
    const start = above ? above.newEnd : 1;
    const end = below
      ? below.newStart
      : content
        ? content.new.length + 1
        : endOfFile
          ? start
          : undefined;
    const size = end === undefined ? undefined : Math.max(0, end - start);
    const shown = expansions[i] ?? { top: 0, bottom: 0 };
    const top = size === undefined ? 0 : Math.min(shown.top, size);
    const bottom = size === undefined ? 0 : Math.min(shown.bottom, size - top);
    const hidden = size === undefined ? undefined : size - top - bottom;
    if (above && top) {
      const block = result[result.length - 1];
      const offset = above.oldEnd - above.newEnd;
      for (let n = start; n < start + top; n++)
        block.rows.push(contextRow(n + offset, n));
      block.oldEnd += top;
      block.newEnd += top;
      changed.add(block);
    }
    const rows: Row[] = [];
    if (below && bottom) {
      const offset = below.oldStart - below.newStart;
      for (let n = below.newStart - bottom; n < below.newStart; n++)
        rows.push(contextRow(n + offset, n));
    }
    if (below) rows.push(...below.rows);
    const gap: Gap | undefined =
      hidden === 0
        ? undefined
        : { index: i, hidden, down: !!above, up: !!below };
    if (!below) {
      if (gap)
        result.push({
          heading: "",
          rows: [],
          oldStart: above.oldEnd,
          newStart: above.newEnd,
          oldEnd: above.oldEnd,
          newEnd: above.newEnd,
          section: "",
          gap,
        });
      break;
    }
    const block: ExpandedHunk = {
      ...below,
      rows,
      oldStart: below.oldStart - bottom,
      newStart: below.newStart - bottom,
      gap,
    };
    if (!gap && above) {
      const previous = result[result.length - 1];
      previous.rows.push(...rows);
      previous.oldEnd = below.oldEnd;
      previous.newEnd = below.newEnd;
      changed.add(previous);
    } else {
      result.push(block);
      if (bottom) changed.add(block);
    }
  }
  for (const block of changed)
    block.heading = `@@ -${block.oldStart},${block.oldEnd - block.oldStart} +${block.newStart},${block.newEnd - block.newStart} @@${block.section}`;
  return result;
}

/** Highlight the changed span within paired lines, without disturbing syntax markup. */
export function changedSpan(
  left: string,
  right: string,
): [number, number, number] | null {
  if (left === right) return null;
  let prefix = 0,
    suffix = 0;
  while (
    prefix < Math.min(left.length, right.length) &&
    left[prefix] === right[prefix]
  )
    prefix++;
  while (
    suffix < Math.min(left.length, right.length) - prefix &&
    left[left.length - 1 - suffix] === right[right.length - 1 - suffix]
  )
    suffix++;
  return [prefix, left.length - suffix, right.length - suffix];
}
