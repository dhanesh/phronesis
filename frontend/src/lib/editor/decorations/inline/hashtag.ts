// Hashtag decoration: `#urgent`, `#design-review`, `#draft-2`.
// Renders the hashtag as a clickable chip using the
// Decoration.mark({tagName: 'a', ...}) trick — no widget needed; the
// mark itself wraps the source text in an <a> element.
//
// Lezer markdown does not have a Hashtag node, so this family
// regex-scans the visible doc (like wiki-links). The regex requires
// `#` at a non-word boundary so it does not match `id#anchor` style
// fragments. ATX heading markers (`# Title`) are not matched because
// the regex requires a word character immediately after `#`.
//
// Known limitations (V1, acceptable):
//   - `#tag` inside fenced code or inline code IS decorated. Filtering
//     requires a per-match tree lookup; deferred until it becomes a
//     real problem.
//
// Satisfies: U2 (hashtag rendering — bonus beyond original V1 list),
//            S1 (href passes through safeURL via /w/<tag> internal
//            path), T1 (visual-only — mark, not replace).

import type { Range } from '@codemirror/state';
import { Decoration } from '../base';
import type { DecorationFamily } from '../base';

// Match `#tag` where `#` is at a word boundary and the tag is
// `[a-zA-Z0-9_-]+`. The negative-lookbehind keeps the regex from
// matching mid-word (e.g. `id#anchor`). The first capture group is
// the tag name without the leading `#`.
const HASHTAG_REGEX = /(?<![\w-])#([a-zA-Z][\w-]{0,63})/g;

function normalizeTag(tag: string): string {
  return tag.toLowerCase();
}

export function hashtagFamily(opts: {
  onnavigate?: (target: string) => void;
}): DecorationFamily {
  return {
    name: 'hashtag',
    kind: 'inline',
    scan({ state, visibleRange, isCursorInRange }) {
      const out: Array<Range<Decoration>> = [];
      const slice = state.sliceDoc(visibleRange.from, visibleRange.to);
      HASHTAG_REGEX.lastIndex = 0;
      for (
        let match = HASHTAG_REGEX.exec(slice);
        match !== null;
        match = HASHTAG_REGEX.exec(slice)
      ) {
        const from = visibleRange.from + match.index;
        const to = from + match[0].length;
        // When cursor sits inside the tag, leave the source bare so it
        // is editable; the mark would prevent typing in the middle.
        if (isCursorInRange(from, to)) continue;
        const tagName = normalizeTag(match[1]);
        out.push(
          Decoration.mark({
            tagName: 'a',
            class: 'cm-md-hashtag',
            attributes: {
              href: `/w/${tagName}`,
              'data-hashtag': tagName,
              title: `Open ${tagName}`,
            },
          }).range(from, to),
        );
      }
      // Click handling: a Decoration.mark cannot attach a JS event
      // handler. Editor.svelte registers a single delegated click
      // listener on `.cm-content` that intercepts `a.cm-md-hashtag`
      // clicks and routes them through onnavigate. The opts.onnavigate
      // callback flows through V1Options for that delegation.
      void opts;
      return out;
    },
  };
}
