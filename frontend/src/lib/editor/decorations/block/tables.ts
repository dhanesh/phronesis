// GFM table decoration: when the cursor is OUTSIDE the entire `Table`
// node, replace the source range with a real `<table>` widget rendered
// from the parsed Lezer subtree. When the cursor is INSIDE, no
// decoration is emitted — the raw pipe-and-dash markdown source remains
// fully editable (U1).
//
// Satisfies: T1 (the source is never mutated — we only emit a visual
//            block-replace decoration; cut/copy and `state.doc` keep
//            seeing raw markdown),
//            T4 / TN2 (sibling block pattern — same skeleton as inline
//            families, different Decoration shape: replace with
//            block: true and a widget),
//            TN3 (widget memoization — `eq()` compares a stable hash of
//            the source slice so unchanged tables don't rebuild DOM on
//            every adjacent edit),
//            U1 (cursor-in suppresses the replacement; raw source
//            visible),
//            U2 (GFM tables ship in V1),
//            S2 (cell text is set via textContent only — no innerHTML,
//            so any `<script>` written into a cell stays inert).
//
// Lezer node shape (per @lezer/markdown GFM tree):
//   Table
//     TableHeader      ← first row, contains TableCell children
//     TableDelimiter   ← `| --- | --- |` separator (NOT rendered)
//     TableRow         ← body rows, contain TableCell children
//     TableRow
//     ...

import { WidgetType } from '@codemirror/view';
import type { EditorState, Range } from '@codemirror/state';
import { Decoration, treeFamily } from '../base';
import type { DecorationFamily, SyntaxNodeRef } from '../base';

const NODE_TYPES = ['Table'] as const;

interface ParsedRow {
  kind: 'header' | 'body';
  cells: string[];
}

// Walk the Table subtree once and pull out the parsed rows. We work
// directly off the Lezer tree so we never re-tokenize the source — that
// would risk accidentally lighting up `innerHTML`-style code paths and
// would also miss whatever GFM idiosyncrasies the parser already
// resolved (escaped pipes, leading/trailing whitespace).
function parseTable(state: EditorState, tableNode: SyntaxNodeRef): ParsedRow[] {
  const rows: ParsedRow[] = [];
  const stable = tableNode.node;

  let child = stable.firstChild;
  while (child) {
    if (child.name === 'TableHeader' || child.name === 'TableRow') {
      const kind: 'header' | 'body' =
        child.name === 'TableHeader' ? 'header' : 'body';
      const cells: string[] = [];
      let cell = child.firstChild;
      while (cell) {
        if (cell.name === 'TableCell') {
          cells.push(state.sliceDoc(cell.from, cell.to).trim());
        }
        cell = cell.nextSibling;
      }
      rows.push({ kind, cells });
    }
    // TableDelimiter (the `| --- |` separator) is skipped intentionally.
    child = child.nextSibling;
  }
  return rows;
}

class TableWidget extends WidgetType {
  // The full source slice is kept as the identity key. Any edit inside
  // the table changes this string and forces a rebuild; edits adjacent
  // to but outside the table leave it byte-identical and `eq()` returns
  // true so CodeMirror keeps the existing DOM (TN3).
  constructor(
    private readonly source: string,
    private readonly rows: ParsedRow[],
  ) {
    super();
  }

  eq(other: TableWidget): boolean {
    return other.source === this.source;
  }

  toDOM(): HTMLElement {
    const table = document.createElement('table');
    table.className = 'cm-md-table';

    const thead = document.createElement('thead');
    const tbody = document.createElement('tbody');

    for (const row of this.rows) {
      const tr = document.createElement('tr');
      tr.className = 'cm-md-table-row';
      for (const cellText of row.cells) {
        const cell =
          row.kind === 'header'
            ? document.createElement('th')
            : document.createElement('td');
        cell.className =
          row.kind === 'header' ? 'cm-md-table-header' : 'cm-md-table-cell';
        // S2: textContent only. The cell text may include raw HTML the
        // user wrote into the markdown source — we render it as literal
        // characters, never as live markup.
        cell.textContent = cellText;
        tr.appendChild(cell);
      }
      (row.kind === 'header' ? thead : tbody).appendChild(tr);
    }

    if (thead.childNodes.length > 0) table.appendChild(thead);
    if (tbody.childNodes.length > 0) table.appendChild(tbody);
    return table;
  }

  // Allow the user to click into a rendered cell — the click lands in
  // the source range, the cursor moves into the table, and the next
  // rebuild suppresses this decoration so the source becomes editable.
  ignoreEvent(): boolean {
    return false;
  }
}

export function tablesFamily(): DecorationFamily {
  return treeFamily({
    name: 'tables',
    nodeTypes: NODE_TYPES,
    build({ node, state, isCursorInRange }) {
      // U1: cursor inside the table → no decoration; raw source visible.
      if (isCursorInRange) return null;

      const source = state.sliceDoc(node.from, node.to);
      const rows = parseTable(state, node);
      // Defensive: if the parse yielded no rows, fall through to raw
      // source rather than render an empty <table>. Honors U4 — half
      // styling on malformed input is worse than no styling.
      if (rows.length === 0) return null;

      const widget = new TableWidget(source, rows);
      const range: Range<Decoration> = Decoration.replace({
        widget,
        block: true,
      }).range(node.from, node.to);
      return [range];
    },
  });
}
