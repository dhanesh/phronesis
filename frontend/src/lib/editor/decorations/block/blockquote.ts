// Blockquote decoration: applies line-level classes to every line in a
// `Blockquote` Lezer range so the editor can render the indented bar,
// padding, and softer text colour. Each `>` glyph also gets a mark
// decoration so its colour can drift independently of the quote body.
// No source is replaced — the user keeps editing raw markdown.
//
// Satisfies: T1 (line + mark decorations only — no doc mutations),
//            T4 / TN2 (sibling block pattern — same skeleton as inline
//            families, different Decoration shape: line + mark, not
//            replace),
//            U1 / U4 (cursor-in does not change behaviour because there
//            is no replacement to suppress; raw `>` characters remain
//            visible at all times),
//            S2 (no widgets — purely class-based styling).
//
// Lezer node shape (per @lezer/markdown):
//   Blockquote
//     QuoteMark   ← one per `>` glyph; may appear once per line for
//                   nested or continued quotes
//     <inline children>
//     Blockquote  ← nested quotes are children of the outer quote
//
// Class contract (asserted by follow-up e2e wave):
//   .cm-md-blockquote          on every line covered by the Blockquote
//   .cm-md-blockquote-marker   on each `>` character range

import type { Range } from '@codemirror/state';
import { Decoration, treeFamily } from '../base';
import type { DecorationFamily } from '../base';

const NODE_TYPES = ['Blockquote'] as const;

export function blockquoteFamily(): DecorationFamily {
  return treeFamily({
    name: 'blockquote',
    kind: 'block',
    nodeTypes: NODE_TYPES,
    build({ node, state }) {
      const out: Array<Range<Decoration>> = [];

      const fromLine = state.doc.lineAt(node.from).number;
      const toLine = state.doc.lineAt(node.to).number;

      for (let lineNo = fromLine; lineNo <= toLine; lineNo++) {
        const line = state.doc.line(lineNo);
        out.push(
          Decoration.line({ class: 'cm-md-blockquote' }).range(line.from),
        );
      }

      // Mark each direct QuoteMark child. Nested Blockquote nodes are
      // visited again by the iterator (treeFamily.iterate descends), so
      // their `>` markers are picked up on their own pass — we only need
      // direct children here to avoid double-marking.
      const stable = node.node;
      let child = stable.firstChild;
      while (child) {
        if (child.name === 'QuoteMark' && child.to > child.from) {
          out.push(
            Decoration.mark({ class: 'cm-md-blockquote-marker' })
              .range(child.from, child.to),
          );
        }
        child = child.nextSibling;
      }
      return out;
    },
  });
}
