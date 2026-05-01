// Fenced code block decoration: applies line-level classes to every line
// covered by a `FencedCode` Lezer node so the editor can render the block
// with monospace styling, gutter chrome, and an optional language tag —
// without ever replacing the source. The cursor remains free to enter and
// edit the code; nothing is hidden.
//
// Satisfies: T1 (line decorations are visual-only — no doc mutations),
//            T4 / TN2 (sibling block pattern — same skeleton as the
//            inline families, different Decoration shape: line, not
//            replace),
//            U1 / U4 (raw markdown source stays visible regardless of
//            cursor position; styling is additive only),
//            S2 (no widgets here, but the language tag is exposed via
//            a data-lang attribute set with setAttribute / dataset, never
//            via innerHTML).
//
// Lezer node shape (per @lezer/markdown GFM tree):
//   FencedCode
//     CodeMark   ← opening ``` (or ~~~) line
//     CodeInfo   ← optional language tag on the opener (e.g. "javascript")
//     CodeText   ← body text, may span multiple lines
//     CodeMark   ← closing ``` line
//
// Class contract (asserted by follow-up e2e wave):
//   .cm-md-fenced-code-line       on every line in the FencedCode range
//   .cm-md-fenced-code-fence      on the opening and closing CodeMark lines
//   .cm-md-fenced-code-language   on the first line, when CodeInfo present
//                                 (carries data-lang="<language>")

import type { Range } from '@codemirror/state';
import { Decoration, treeFamily } from '../base';
import type { DecorationFamily } from '../base';

const NODE_TYPES = ['FencedCode'] as const;

export function fencedCodeFamily(): DecorationFamily {
  return treeFamily({
    name: 'fenced-code',
    nodeTypes: NODE_TYPES,
    build({ node, state }) {
      const out: Array<Range<Decoration>> = [];

      const fromLine = state.doc.lineAt(node.from).number;
      const toLine = state.doc.lineAt(node.to).number;

      // Collect fence-marker line numbers and the language tag (if any) by
      // walking the children of the matched FencedCode node. Using the
      // parsed tree avoids re-tokenizing the source and keeps S2 honest —
      // the language string is set via `dataset`, not interpolated HTML.
      const fenceLines = new Set<number>();
      let language: string | null = null;

      const stable = node.node;
      let child = stable.firstChild;
      while (child) {
        if (child.name === 'CodeMark') {
          // CodeMark spans the ``` characters only, but those characters
          // always sit on their own line, so the line number is unique.
          fenceLines.add(state.doc.lineAt(child.from).number);
        } else if (child.name === 'CodeInfo' && language === null) {
          language = state.sliceDoc(child.from, child.to).trim();
        }
        child = child.nextSibling;
      }

      for (let lineNo = fromLine; lineNo <= toLine; lineNo++) {
        const line = state.doc.line(lineNo);
        out.push(
          Decoration.line({ class: 'cm-md-fenced-code-line' }).range(line.from),
        );
        if (fenceLines.has(lineNo)) {
          out.push(
            Decoration.line({ class: 'cm-md-fenced-code-fence' }).range(line.from),
          );
        }
        if (lineNo === fromLine && language) {
          out.push(
            Decoration.line({
              class: 'cm-md-fenced-code-language',
              attributes: { 'data-lang': language },
            }).range(line.from),
          );
        }
      }
      return out;
    },
  });
}
