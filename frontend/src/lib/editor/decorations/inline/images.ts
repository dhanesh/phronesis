// Image decoration: `![alt](src)`.
// When the cursor is outside the Image node AND the URL passes the
// RT-12 classifier (absolute http/https OR `/media/<sha>` blob path
// AND non-`#` from safeURL), the source is replaced with an
// `ImageWidget` that renders an <img> plus a fallback caption. When
// the cursor is inside, or the URL fails the gate, no decoration is
// emitted — raw markdown source is visible per U1 / U4.
//
// URL gate (RT-12 + S1):
//   1. safeURL(url) must not collapse to "#" (rules out javascript:,
//      data:, vbscript:, etc.).
//   2. The URL must be either:
//        - an absolute http:// or https:// URL, OR
//        - a `/media/<sha>` path matching internal/media's blob handler
//          (lower-case hex SHA, any length the handler accepts).
//   Anything else (relative paths, attachment shorthands, fragments)
//   produces no decoration; the raw markdown stays visible.
//
// Satisfies: U1 (cursor-in reveals source), U2 (image coverage), U4
// (out-of-scope sources render as raw source), T1 (visual-only — no
// doc mutation), T4 (treeFamily skeleton), S1 (safeURL gate), S2
// (textContent only — no innerHTML), TN3 (WidgetType.eq() compares
// the meaningful display fields), RT-12 (URL classifier scope).

import type { Range } from '@codemirror/state';
import { WidgetType } from '@codemirror/view';
import { Decoration, treeFamily } from '../base';
import type { DecorationFamily } from '../base';
import { safeURL } from '../../../safeURL';

const MEDIA_SHA_PATH = /^\/media\/[a-f0-9]+$/;

function isRenderableImageURL(url: string): boolean {
  if (!url) return false;
  if (safeURL(url) === '#') return false;
  if (url.startsWith('http://') || url.startsWith('https://')) return true;
  if (MEDIA_SHA_PATH.test(url)) return true;
  return false;
}

class ImageWidget extends WidgetType {
  constructor(
    private readonly alt: string,
    private readonly src: string,
  ) {
    super();
  }

  // TN3 widget memoization — equal widgets short-circuit DOM rebuild.
  eq(other: ImageWidget): boolean {
    return other.alt === this.alt && other.src === this.src;
  }

  toDOM(): HTMLElement {
    const wrapper = document.createElement('span');
    wrapper.className = 'cm-md-image-wrapper';

    const img = document.createElement('img');
    img.className = 'cm-md-image';
    img.src = this.src;
    img.alt = this.alt;
    wrapper.appendChild(img);

    // Fallback caption — visible if the image fails to load (alt would
    // otherwise be the only signal). textContent (S2), never innerHTML.
    const caption = document.createElement('span');
    caption.className = 'cm-md-image-caption';
    caption.textContent = this.alt;
    wrapper.appendChild(caption);

    return wrapper;
  }

  ignoreEvent(): boolean {
    return false;
  }
}

// Walk the Image node's children to find the alt text (between the
// opening `![` LinkMark and the closing `]` LinkMark) and the URL
// child. Mirrors extractLink in markdown-links.ts but for the image
// shape, where the leading marker is two characters (`![`).
function extractImage(
  node: import('@lezer/common').SyntaxNodeRef,
  state: import('@codemirror/state').EditorState,
): { alt: string; src: string } | null {
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
  if (linkMarks.length < 2 || urlFrom < 0) return null;
  const altFrom = linkMarks[0].to;
  const altTo = linkMarks[1].from;
  if (altTo < altFrom) return null;
  const alt = state.sliceDoc(altFrom, altTo);
  const src = state.sliceDoc(urlFrom, urlTo);
  return { alt, src };
}

export function imagesFamily(): DecorationFamily {
  return treeFamily({
    name: 'images',
    nodeTypes: ['Image'] as const,
    build({ node, state, isCursorInRange }): Array<Range<Decoration>> | null {
      if (isCursorInRange) return null;
      const parts = extractImage(node, state);
      if (!parts) return null;
      if (!isRenderableImageURL(parts.src)) return null;
      return [
        Decoration.replace({
          widget: new ImageWidget(parts.alt, parts.src),
          inclusive: false,
        }).range(node.from, node.to),
      ];
    },
  });
}
