// Wiki-link decoration: replaces [[Page]] / [[Page|Label]] in source with
// a clickable chip when the cursor is outside the range.
// Satisfies: U1 (cursor-in reveals source), S2 (textContent only).
// Migrated from the inline wikiLinkDecorations ViewPlugin in Editor.svelte
// to the shared DecorationFamily contract — see base.ts.

import { WidgetType } from '@codemirror/view';
import type { Range } from '@codemirror/state';
import { Decoration } from '../base';
import type { DecorationFamily } from '../base';

const WIKI_LINK_REGEX = /\[\[([^\]|]+)(?:\|([^\]]+))?\]\]/g;

function normalizeWikiName(name: string): string {
  return (name || '').trim().replaceAll(' ', '-').replaceAll(/^\/+/, '').toLowerCase();
}

class WikiLinkWidget extends WidgetType {
  constructor(
    private readonly label: string,
    private readonly target: string,
    private readonly currentPage: string,
    private readonly onnavigate?: (target: string) => void,
  ) {
    super();
  }

  // TN3 widget memoization: stable identity across rebuilds when the
  // rendered output is unchanged.
  eq(other: WikiLinkWidget): boolean {
    return (
      other.label === this.label &&
      other.target === this.target &&
      other.currentPage === this.currentPage
    );
  }

  toDOM(): HTMLElement {
    const anchor = document.createElement('a');
    anchor.className = `cm-wikilink${this.target === this.currentPage ? ' current' : ''}`;
    anchor.href = `/w/${this.target}`;
    anchor.textContent = this.label; // S2: textContent, never innerHTML.
    anchor.dataset.wikiLink = this.target;
    anchor.title = `Open ${this.target}`;
    anchor.addEventListener('click', (event) => {
      event.preventDefault();
      this.onnavigate?.(this.target);
    });
    return anchor;
  }

  ignoreEvent(): boolean {
    return false;
  }
}

export function wikiLinksFamily(opts: {
  currentPage: () => string;
  onnavigate?: (target: string) => void;
}): DecorationFamily {
  return {
    name: 'wiki-links',
    scan({ state, visibleRange, isCursorInRange }) {
      const out: Array<Range<Decoration>> = [];
      // Wiki-links are not standard markdown grammar — Lezer markdown sees
      // them as plain text. So we regex over the visible slice of the doc.
      const slice = state.sliceDoc(visibleRange.from, visibleRange.to);
      const currentPage = opts.currentPage();

      WIKI_LINK_REGEX.lastIndex = 0;
      for (let match = WIKI_LINK_REGEX.exec(slice); match !== null; match = WIKI_LINK_REGEX.exec(slice)) {
        const from = visibleRange.from + match.index;
        const to = from + match[0].length;
        if (isCursorInRange(from, to)) continue;
        const target = normalizeWikiName(match[1]);
        const label = match[2] || match[1];
        out.push(
          Decoration.replace({
            widget: new WikiLinkWidget(label, target, currentPage, opts.onnavigate),
            inclusive: false,
          }).range(from, to),
        );
      }
      return out;
    },
  };
}
