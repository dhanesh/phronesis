// Barrel for the live-preview decoration system.
// V1 families plug in here; Editor.svelte composes them via composeV1Families().
// Subsequent waves will register inline (headings, emphasis, code, links,
// lists, images) and block (fenced-code, blockquote, tables) families
// alongside wiki-links.

import { liveDecorationExtension } from './base';
import type { DecorationFamily } from './base';
import { wikiLinksFamily } from './inline/wiki-links';
import { headingsFamily } from './inline/headings';
import { emphasisFamily } from './inline/emphasis';
import { inlineCodeFamily } from './inline/inline-code';
import { markdownLinksFamily } from './inline/markdown-links';
import { listsFamily } from './inline/lists';
import { imagesFamily } from './inline/images';
import { hashtagFamily } from './inline/hashtag';
import { tasksFamily } from './inline/tasks';
import { fencedCodeFamily } from './block/fenced-code';
import { blockquoteFamily } from './block/blockquote';
import { tablesFamily } from './block/tables';
import type { Extension } from '@codemirror/state';

export type { DecorationFamily } from './base';
export { selectionTouches, treeFamily, rebuildLivePreview } from './base';

export interface V1Options {
  currentPage: () => string;
  onnavigate?: (target: string) => void;
  onTaskToggle?: (from: number, to: number, currentlyChecked: boolean) => void;
}

// Registry of V1 decoration families. Order matters only when ranges
// overlap at the same byte position; the composer in base.ts sorts by
// (from, to) before adding to the RangeSetBuilder, so block families
// are listed first as a defensive default.
export function composeV1Families(opts: V1Options): readonly DecorationFamily[] {
  return [
    // Block (Wave 4)
    fencedCodeFamily(),
    blockquoteFamily(),
    tablesFamily(),
    // Inline (Wave 3)
    headingsFamily(),
    emphasisFamily(),
    inlineCodeFamily(),
    markdownLinksFamily({ onnavigate: opts.onnavigate }),
    listsFamily(),
    imagesFamily(),
    hashtagFamily({ onnavigate: opts.onnavigate }),
    tasksFamily({
      onToggle: opts.onTaskToggle ?? (() => {}),
    }),
    // Wiki-links (foundation)
    wikiLinksFamily({ currentPage: opts.currentPage, onnavigate: opts.onnavigate }),
  ];
}

export function livePreviewExtension(opts: V1Options): Extension {
  return liveDecorationExtension(composeV1Families(opts));
}
