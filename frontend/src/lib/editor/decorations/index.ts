// Barrel for the live-preview decoration system.
// V1 families plug in here; Editor.svelte composes them via composeV1Families().
// Subsequent waves will register inline (headings, emphasis, code, links,
// lists, images) and block (fenced-code, blockquote, tables) families
// alongside wiki-links.

import { liveDecorationPlugin } from './base';
import type { DecorationFamily } from './base';
import { wikiLinksFamily } from './inline/wiki-links';
import type { Extension } from '@codemirror/state';

export type { DecorationFamily } from './base';
export { selectionTouches, treeFamily, rebuildLivePreview } from './base';

export interface V1Options {
  currentPage: () => string;
  onnavigate?: (target: string) => void;
}

// Registry of V1 decoration families. Each wave's PR appends to this array.
export function composeV1Families(opts: V1Options): readonly DecorationFamily[] {
  return [
    wikiLinksFamily({ currentPage: opts.currentPage, onnavigate: opts.onnavigate }),
    // Wave 3 inline (pending): headings, emphasis, inlineCode, markdownLinks, lists, images
    // Wave 4 block (pending):  fencedCode, blockquote, tables
  ];
}

export function livePreviewExtension(opts: V1Options): Extension {
  return liveDecorationPlugin(composeV1Families(opts));
}
