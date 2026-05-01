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
    kind: 'inline',
    nodeTypes: HEADING_NODES,
    build({ node, isCursorInRange }): Array<Range<Decoration>> | null {
      const level = LEVEL_BY_NAME[node.name];
      if (!level) return null;
      if (node.to <= node.from) return null;

      const out: Array<Range<Decoration>> = [];

      // Find the leading HeaderMark range so the style mark can start
      // *after* it. CM6 silently drops a Decoration.replace nested
      // inside an overlapping Decoration.mark of the same plugin, so
      // we never let the style mark cover the marker source — instead
      // it covers from marker-end to node-end. When the cursor is
      // outside, the marker is replaced with a zero-width widget.
      let markerFrom = -1;
      let markerTo = -1;
      const cursor = node.node.cursor();
      if (cursor.firstChild()) {
        do {
          if (cursor.name === 'HeaderMark' && cursor.to > cursor.from) {
            markerFrom = cursor.from;
            markerTo = cursor.to;
            break;
          }
        } while (cursor.nextSibling());
      }
      const styleFrom = markerTo > 0 ? markerTo : node.from;
      if (styleFrom < node.to) {
        out.push(
          Decoration.mark({
            class: `cm-md-heading cm-md-heading-${level}`,
          }).range(styleFrom, node.to),
        );
      }
      if (!isCursorInRange && markerFrom >= 0) {
        out.push(hideMarker(markerFrom, markerTo));
      }
      return out;
    },
  });
}
