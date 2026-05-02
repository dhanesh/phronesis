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

import { WidgetType } from '@codemirror/view';
import type { Range } from '@codemirror/state';
import { Decoration, treeFamily } from '../base';
import type { DecorationFamily } from '../base';

const NODE_TYPES = ['FencedCode'] as const;

// Widget rendering a "Copy" button that places the fenced code body
// on the system clipboard. Source range is captured at decoration-build
// time so the click handler does not need EditorView access.
class CopyCodeWidget extends WidgetType {
  constructor(private readonly codeText: string) {
    super();
  }
  eq(other: CopyCodeWidget): boolean {
    return other.codeText === this.codeText;
  }
  toDOM(): HTMLElement {
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'cm-md-fenced-code-copy';
    button.textContent = 'Copy';
    button.title = 'Copy code';
    button.addEventListener('mousedown', (e) => e.stopPropagation());
    button.addEventListener('click', async (e) => {
      e.stopPropagation();
      e.preventDefault();
      try {
        await navigator.clipboard.writeText(this.codeText);
        const previous = button.textContent;
        button.textContent = 'Copied';
        setTimeout(() => {
          if (button.isConnected) button.textContent = previous;
        }, 1200);
      } catch {
        button.textContent = 'Failed';
      }
    });
    return button;
  }
  ignoreEvent(): boolean {
    return false;
  }
}

export function fencedCodeFamily(): DecorationFamily {
  return treeFamily({
    name: 'fenced-code',
    kind: 'block',
    nodeTypes: NODE_TYPES,
    build({ node, state }) {
      const out: Array<Range<Decoration>> = [];

      const fromLine = state.doc.lineAt(node.from).number;
      const toLine = state.doc.lineAt(node.to).number;

      // Collect fence-marker line numbers, language tag, and the inner
      // code body text by walking the children of the matched FencedCode
      // node. Using the parsed tree avoids re-tokenizing the source and
      // keeps S2 honest — every text payload is set via dataset /
      // textContent, never via innerHTML.
      const fenceLines = new Set<number>();
      let language: string | null = null;
      const codeTextParts: string[] = [];

      const stable = node.node;
      let child = stable.firstChild;
      while (child) {
        if (child.name === 'CodeMark') {
          fenceLines.add(state.doc.lineAt(child.from).number);
        } else if (child.name === 'CodeInfo' && language === null) {
          language = state.sliceDoc(child.from, child.to).trim();
        } else if (child.name === 'CodeText') {
          codeTextParts.push(state.sliceDoc(child.from, child.to));
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

      // Copy button anchored at the start of the FencedCode node. The
      // CSS class positions it absolutely top-right inside the styled
      // block.
      const codeText = codeTextParts.join('\n');
      if (codeText.length > 0) {
        out.push(
          Decoration.widget({
            widget: new CopyCodeWidget(codeText),
            side: -1,
          }).range(node.from),
        );
      }

      return out;
    },
  });
}
