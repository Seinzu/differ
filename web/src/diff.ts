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
  function flush() {
    if (!hunk) return;
    for (let i = 0; i < Math.max(removed.length, added.length); i++) {
      hunk.rows.push({ kind: "change", left: removed[i], right: added[i] });
    }
    removed = [];
    added = [];
  }
  for (const line of patch.split("\n")) {
    const header = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@(.*)$/.exec(line);
    if (header) {
      flush();
      oldNumber = Number(header[1]);
      newNumber = Number(header[2]);
      hunk = { heading: line, rows: [] };
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
  return hunks;
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
