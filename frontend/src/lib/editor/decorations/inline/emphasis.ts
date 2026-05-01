// Emphasis decoration: `*italic*` / `_italic_` and `**bold**` / `__bold__`.
// The inner text range (between the opening and closing EmphasisMark
// children) gets a mark of `cm-md-emphasis` (italic) or `cm-md-strong`
// (bold). When the cursor is outside the construct, the EmphasisMark
// children are hidden via empty Decoration.replace; when inside, the
// markers are revealed for editing (U1). The styling mark stays in
// both cases so the visible text retains its italic/bold style even
// while editing.
//
// Satisfies: U1 (cursor-in reveals source), U2 (emphasis coverage),
// T1 (visual-only — no doc mutation), T4 (treeFamily skeleton), S2
// (no innerHTML — these are mark/replace decorations, not widgets),
// TN3 (no widget allocation).

import type { Range } from '@codemirror/state';
import type { Decoration } from '../base';
import { treeFamily } from '../base';
import type { DecorationFamily } from '../base';
import { pairedMarkerDecorations } from './_helpers';

const EMPHASIS_NODES = ['Emphasis', 'StrongEmphasis'] as const;

export function emphasisFamily(): DecorationFamily {
  return treeFamily({
    name: 'emphasis',
    nodeTypes: EMPHASIS_NODES,
    build({ node, isCursorInRange }): Array<Range<Decoration>> | null {
      const className = node.name === 'StrongEmphasis' ? 'cm-md-strong' : 'cm-md-emphasis';
      return pairedMarkerDecorations({
        node,
        isCursorInRange,
        className,
        markerName: 'EmphasisMark',
      });
    },
  });
}
