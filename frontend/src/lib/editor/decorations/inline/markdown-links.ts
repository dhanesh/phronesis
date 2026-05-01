// Markdown link decoration: `[label](url)`.
// When the cursor is outside the entire Link node, the source range is
// replaced with a `MarkdownLinkWidget` rendering an <a class="cm-md-link">
// chip. The widget routes its href through safeURL() (S1 / RT-5) and
// invokes the optional onnavigate callback for internal targets so the
// SPA shell can handle navigation without a full reload. When the
// cursor is inside the link, no decoration is emitted — raw markdown
// source is visible per U1.
//
// Satisfies: U1 (cursor-in reveals source), U2 (markdown-link coverage),
// T1 (visual-only — no doc mutation), T4 (treeFamily skeleton), S1
// (safeURL allow-list), S2 (textContent only — no innerHTML),
// TN3 (WidgetType.eq() compares the meaningful display fields).

import type { Range } from '@codemirror/state';
import { WidgetType } from '@codemirror/view';
import { Decoration, treeFamily } from '../base';
import type { DecorationFamily } from '../base';
import { safeURL } from '../../../safeURL';

class MarkdownLinkWidget extends WidgetType {
  constructor(
    private readonly label: string,
    private readonly target: string,
    private readonly onnavigate?: (target: string) => void,
  ) {
    super();
  }

  // TN3 widget memoization — equal widgets short-circuit DOM rebuild.
  eq(other: MarkdownLinkWidget): boolean {
    return other.label === this.label && other.target === this.target;
  }

  toDOM(): HTMLElement {
    const anchor = document.createElement('a');
    anchor.className = 'cm-md-link';
    const safe = safeURL(this.target);
    anchor.href = safe;
    anchor.textContent = this.label; // S2: textContent, never innerHTML.
    anchor.title = this.target;
    // Internal targets (relative paths or fragments) are routed through
    // the SPA's navigation hook when one is supplied; everything else
    // falls through to the browser's default link behaviour.
    const isInternal = safe.startsWith('/') || safe.startsWith('#');
    if (isInternal && this.onnavigate) {
      anchor.addEventListener('click', (event) => {
        event.preventDefault();
        this.onnavigate?.(this.target);
      });
    }
    return anchor;
  }

  ignoreEvent(): boolean {
    return false;
  }
}

// Extract label / URL from a Link node by walking its LinkMark + URL
// children. Lezer's structure is: `[` LinkMark, label text, `]` LinkMark,
// `(` LinkMark, URL, `)` LinkMark.
function extractLink(
  node: import('@lezer/common').SyntaxNodeRef,
  state: import('@codemirror/state').EditorState,
): { label: string; url: string } | null {
  const linkMarks: Array<{ from: number; to: number }> = [];
  let urlFrom = -1;
  let urlTo = -1;
  const cursor = node.node.cursor();
  if (!cursor.firstChild()) return null;
  do {
    if (cursor.name === 'LinkMark') {
      linkMarks.push({ from: cursor.from, to: cursor.to });
    } else if (cursor.name === 'URL') {
      urlFrom = cursor.from;
      urlTo = cursor.to;
    }
  } while (cursor.nextSibling());

  // Need at least the opening `[` and closing `]` LinkMarks to delimit
  // the label; URL is optional only for malformed nodes (skip those).
  if (linkMarks.length < 2 || urlFrom < 0) return null;
  const labelFrom = linkMarks[0].to;
  const labelTo = linkMarks[1].from;
  if (labelTo < labelFrom) return null;
  const label = state.sliceDoc(labelFrom, labelTo);
  const url = state.sliceDoc(urlFrom, urlTo);
  return { label, url };
}

export function markdownLinksFamily(opts: {
  onnavigate?: (target: string) => void;
}): DecorationFamily {
  return treeFamily({
    name: 'markdown-links',
    kind: 'inline',
    nodeTypes: ['Link'] as const,
    build({ node, state, isCursorInRange }): Array<Range<Decoration>> | null {
      // Cursor inside: leave the source visible — no decoration at all
      // (per U1: editing reveals raw markdown).
      if (isCursorInRange) return null;
      const parts = extractLink(node, state);
      if (!parts) return null;
      return [
        Decoration.replace({
          widget: new MarkdownLinkWidget(parts.label, parts.url, opts.onnavigate),
          inclusive: false,
        }).range(node.from, node.to),
      ];
    },
  });
}
