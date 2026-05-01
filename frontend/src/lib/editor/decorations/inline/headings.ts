// ATX heading decoration: `# Title` ... `###### Title`.
// When the cursor is outside the heading, the leading `#` marker is
// hidden via Decoration.replace and the whole heading node is wrapped
// in a mark with `cm-md-heading cm-md-heading-N` so CSS can scale it.
// When the cursor enters the heading range, the marker is revealed
// (U1) but the styling mark stays applied.
//
// Satisfies: U1 (cursor-in reveals source), U2 (heading coverage),
// T1 (visual-only — no doc mutation), T4 (treeFamily skeleton),
// TN3 (no widget allocation; mark/replace decorations are diffed
// trivially by CodeMirror).

import type { Range } from '@codemirror/state';
import { Decoration, treeFamily } from '../base';
import type { DecorationFamily } from '../base';

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
    nodeTypes: HEADING_NODES,
    build({ node, isCursorInRange }): Array<Range<Decoration>> | null {
      const level = LEVEL_BY_NAME[node.name];
      if (!level) return null;
      if (node.to <= node.from) return null;

      const out: Array<Range<Decoration>> = [];
      // Whole-heading style mark — covers the entire node so the level
      // class applies even when the source is revealed for editing.
      out.push(
        Decoration.mark({
          class: `cm-md-heading cm-md-heading-${level}`,
        }).range(node.from, node.to),
      );

      // Hide the `#` marker(s) only when the cursor is elsewhere.
      if (!isCursorInRange) {
        const cursor = node.node.cursor();
        if (cursor.firstChild()) {
          do {
            if (cursor.name === 'HeaderMark' && cursor.to > cursor.from) {
              out.push(Decoration.replace({}).range(cursor.from, cursor.to));
            }
          } while (cursor.nextSibling());
        }
      }
      return out;
    },
  });
}
