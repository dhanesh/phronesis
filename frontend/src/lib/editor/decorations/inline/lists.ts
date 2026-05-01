// List-marker decoration: the `-`, `*`, `+` glyph in unordered lists
// and the `1.` / `2.` glyph in ordered lists.
// Marks the ListMark child range (which is the marker glyph itself,
// not the surrounding ListItem) with `cm-md-list-marker` so CSS can
// restyle it (e.g. tighten spacing, swap to a custom bullet, or hide
// task-list markers later). Unlike emphasis / inline-code, the marker
// is *not* hidden — bullets must remain visible to indicate list
// structure even when the cursor is outside.
//
// Satisfies: U2 (list-marker coverage), T1 (visual-only), T4
// (treeFamily skeleton), TN3 (no widget allocation).

import type { Range } from '@codemirror/state';
import { Decoration, treeFamily } from '../base';
import type { DecorationFamily } from '../base';

export function listsFamily(): DecorationFamily {
  return treeFamily({
    name: 'lists',
    kind: 'inline',
    nodeTypes: ['ListMark'] as const,
    build({ node }): Array<Range<Decoration>> | null {
      if (node.to <= node.from) return null;
      return [
        Decoration.mark({ class: 'cm-md-list-marker' }).range(node.from, node.to),
      ];
    },
  });
}
