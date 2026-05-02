// GitHub-style admonition blocks: a Blockquote whose first line is
// `[!note]`, `[!warning]`, `[!tip]`, `[!caution]`, `[!important]`, or
// `[!danger]` is rendered with a distinctive callout style.
//
// Implementation: the `[!type]` syntax is parsed by Lezer as a Link
// node inside the blockquote's first Paragraph. We detect that shape
// and apply Decoration.line with `cm-md-admonition cm-md-admonition-
// <type>` on every line of the blockquote. The blockquote family also
// runs in parallel, layering its base blockquote styling underneath
// (CSS resolves the cascade).
//
// Satisfies: U2 (admonitions — bonus beyond original V1 list), T1
// (line decorations only), S2 (no widgets, no innerHTML).

import type { Range } from '@codemirror/state';
import { Decoration, treeFamily } from '../base';
import type { DecorationFamily } from '../base';

const ADMONITION_TYPES = new Set([
  'note',
  'warning',
  'tip',
  'caution',
  'important',
  'danger',
]);

function detectAdmonitionType(node: { firstChild?: any }, sliceDoc: (from: number, to: number) => string): string | null {
  // Walk past QuoteMark to find the first Paragraph.
  const stable = (node as { firstChild: any }).firstChild ? node : (node as any);
  let child = stable.firstChild;
  while (child) {
    if (child.name === 'Paragraph') {
      // First child of paragraph should be a Link with text `[!type]`.
      const link = child.firstChild;
      if (link && link.name === 'Link' && link.from < link.to) {
        const linkText = sliceDoc(link.from, link.to);
        const m = /^\[!([a-z]+)\]$/i.exec(linkText);
        if (m && ADMONITION_TYPES.has(m[1].toLowerCase())) {
          return m[1].toLowerCase();
        }
      }
      return null;
    }
    child = child.nextSibling;
  }
  return null;
}

export function admonitionFamily(): DecorationFamily {
  return treeFamily({
    name: 'admonition',
    kind: 'block',
    nodeTypes: ['Blockquote'] as const,
    build({ node, state }): Array<Range<Decoration>> | null {
      const stable = node.node;
      const type = detectAdmonitionType(stable, (f, t) => state.sliceDoc(f, t));
      if (!type) return null;

      const out: Array<Range<Decoration>> = [];
      const fromLine = state.doc.lineAt(node.from).number;
      const toLine = state.doc.lineAt(node.to).number;
      for (let lineNo = fromLine; lineNo <= toLine; lineNo++) {
        const line = state.doc.line(lineNo);
        out.push(
          Decoration.line({
            class: `cm-md-admonition cm-md-admonition-${type}`,
            attributes: { 'data-admonition': type },
          }).range(line.from),
        );
      }
      return out;
    },
  });
}
