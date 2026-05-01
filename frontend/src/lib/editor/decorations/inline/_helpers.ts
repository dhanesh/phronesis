// Shared helpers for inline marker-hiding families (emphasis, inline
// code). Both follow the same structure: mark the inner text range
// (between the opening and closing marker children) with a class, and
// hide each marker child via Decoration.replace when the cursor is
// outside the construct.
//
// Headings use a slightly different shape (whole-node style mark + a
// single leading marker) so they do not use these helpers — see
// headings.ts.
//
// Satisfies: T1 (visual-only — never mutates state.doc), U1 (cursor-in
// reveals source via the caller's isCursorInRange gate), TN3 (no
// widgets allocated; CodeMirror diffs mark/replace decorations
// without widget memoization concerns).

import type { Range } from '@codemirror/state';
import type { SyntaxNodeRef } from '@lezer/common';
import { WidgetType } from '@codemirror/view';
import { Decoration } from '../base';

// Empty widget used to hide source via Decoration.replace.
// Decoration.replace({}) without a widget does not reliably hide source
// when nested inside an overlapping Decoration.mark range (observed for
// headings — the marker mark wraps the whole heading, and the replace
// of the inner HeaderMark fails to suppress the `#`). Providing an
// explicit empty widget via toDOM() returning a zero-width span
// guarantees the source range is replaced.
class EmptyWidget extends WidgetType {
  eq(): boolean { return true; }
  toDOM(): HTMLElement {
    const span = document.createElement('span');
    span.className = 'cm-md-marker-hidden';
    return span;
  }
  ignoreEvent(): boolean { return false; }
}
const EMPTY_WIDGET = new EmptyWidget();
export const hideMarker = (from: number, to: number): Range<Decoration> =>
  Decoration.replace({ widget: EMPTY_WIDGET }).range(from, to);

// Build mark + replace decorations for a "paired-marker" inline node
// such as Emphasis (one EmphasisMark on each side) or InlineCode (one
// CodeMark on each side). The inner text — everything between the
// first and last marker child — is wrapped in a mark of `className`,
// regardless of cursor position. The marker children themselves are
// replaced with empty widgets only when the cursor is outside.
export function pairedMarkerDecorations(opts: {
  node: SyntaxNodeRef;
  isCursorInRange: boolean;
  className: string;
  markerName: string;
}): Array<Range<Decoration>> | null {
  const { node, isCursorInRange, className, markerName } = opts;

  const markers: Array<{ from: number; to: number }> = [];
  const cursor = node.node.cursor();
  if (cursor.firstChild()) {
    do {
      if (cursor.name === markerName) {
        markers.push({ from: cursor.from, to: cursor.to });
      }
    } while (cursor.nextSibling());
  }

  // Need at least an opening + closing marker to delimit inner text.
  if (markers.length < 2) return null;
  const innerFrom = markers[0].to;
  const innerTo = markers[markers.length - 1].from;
  if (innerTo <= innerFrom) return null;

  const out: Array<Range<Decoration>> = [];
  out.push(Decoration.mark({ class: className }).range(innerFrom, innerTo));

  if (!isCursorInRange) {
    for (const m of markers) {
      if (m.to > m.from) {
        out.push(hideMarker(m.from, m.to));
      }
    }
  }
  return out;
}
