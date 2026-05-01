// Inline-code decoration: `` `code` ``.
// The inner text range (between the opening and closing CodeMark
// children) is wrapped in a mark of `cm-md-inline-code` so CSS can
// switch to a monospace face / background. When the cursor is outside
// the InlineCode node, the surrounding backticks are hidden via empty
// Decoration.replace; when inside, the backticks are revealed (U1)
// while the styling mark stays applied.
//
// Satisfies: U1 (cursor-in reveals source), U2 (inline-code coverage),
// T1 (visual-only — no doc mutation), T4 (treeFamily skeleton), S2
// (no innerHTML — only mark/replace decorations, no widgets), TN3
// (no widget allocation).

import type { Range } from '@codemirror/state';
import type { Decoration } from '../base';
import { treeFamily } from '../base';
import type { DecorationFamily } from '../base';
import { pairedMarkerDecorations } from './_helpers';

export function inlineCodeFamily(): DecorationFamily {
  return treeFamily({
    name: 'inline-code',
    nodeTypes: ['InlineCode'] as const,
    build({ node, isCursorInRange }): Array<Range<Decoration>> | null {
      return pairedMarkerDecorations({
        node,
        isCursorInRange,
        className: 'cm-md-inline-code',
        markerName: 'CodeMark',
      });
    },
  });
}
