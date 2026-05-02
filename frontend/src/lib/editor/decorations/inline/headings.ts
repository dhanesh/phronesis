// ATX heading decoration: `# Title` ... `###### Title`.
//
// Two decorations per heading:
//   1. Decoration.line({class: cm-md-line-heading-N}) on the heading
//      line so CSS can style the entire line (font size, weight,
//      padding, background) the way SilverBullet's .sb-line-hN does.
//   2. Decoration.replace on the leading `#` marker(s) when the
//      cursor is outside, so the heading reads "Title" not "# Title".
//      Revealed when the cursor enters the heading range (U1).
//
// Decoration.line is block-level — CM6 forbids these from ViewPlugins,
// so this family runs in the block StateField path.
//
// Satisfies: U1 (cursor-in reveals source), U2 (heading coverage),
// T1 (visual-only — no doc mutation; only marker is replaced).

import type { Range } from '@codemirror/state';
import { Decoration, treeFamily } from '../base';
import type { DecorationFamily } from '../base';
import { hideMarker } from './_helpers';

const HEADING_NODES = [
  'ATXHeading1',
  'ATXHeading2',
  'ATXHeading3',
  'ATXHeading4',
  'ATXHeading5',
  'ATXHeading6',
] as const;

const LEVEL_BY_NAME: Record<string, number> = {
  ATXHeading1: 1,
  ATXHeading2: 2,
  ATXHeading3: 3,
  ATXHeading4: 4,
  ATXHeading5: 5,
  ATXHeading6: 6,
};

export function headingsFamily(): DecorationFamily {
  return treeFamily({
    name: 'headings',
    kind: 'block',
    nodeTypes: HEADING_NODES,
    build({ node, state, isCursorInRange }): Array<Range<Decoration>> | null {
      const level = LEVEL_BY_NAME[node.name];
      if (!level) return null;
      if (node.to <= node.from) return null;

      const out: Array<Range<Decoration>> = [];

      // Decoration.line on the heading line — CSS targets
      // `.cm-md-line-heading-N` to style the whole line.
      const line = state.doc.lineAt(node.from);
      out.push(
        Decoration.line({
          class: `cm-md-line-heading cm-md-line-heading-${level}`,
        }).range(line.from),
      );

      // Hide the leading `#` marker(s) only when the cursor is
      // elsewhere. The HeaderMark child carries the exact range.
      if (!isCursorInRange) {
        const cursor = node.node.cursor();
        if (cursor.firstChild()) {
          do {
            if (cursor.name === 'HeaderMark' && cursor.to > cursor.from) {
              out.push(hideMarker(cursor.from, cursor.to));
              break;
            }
          } while (cursor.nextSibling());
        }
      }
      return out;
    },
  });
}
