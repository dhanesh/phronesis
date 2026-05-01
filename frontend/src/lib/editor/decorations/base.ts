// Shared scaffold for live-preview decoration families.
// Satisfies: T4 (refined per TN2 — the *skeleton* is shared, individual
// families pick their own decoration shape: replace / mark / line / block).
// Constrains: T1 (visual-only — never dispatch doc changes from here),
//             T2 (viewport-scoped, segmented rebuild policy),
//             O2 / TN8 (per-family code stays small by reusing this base).

import { syntaxTree } from '@codemirror/language';
import { EditorState, RangeSetBuilder, StateEffect } from '@codemirror/state';
import type { Extension, Range } from '@codemirror/state';
import { Decoration, EditorView, ViewPlugin } from '@codemirror/view';
import type { DecorationSet, ViewUpdate } from '@codemirror/view';
import type { SyntaxNodeRef } from '@lezer/common';

// Sent by Editor.svelte when an external dependency of the decoration set
// changes (e.g., the current page name used by wiki-link widgets to mark
// themselves "current"). Plugin's update() also rebuilds on this signal.
export const rebuildLivePreview = StateEffect.define<void>();

export interface ScanContext {
  state: EditorState;
  view: EditorView;
  visibleRange: { from: number; to: number };
  isCursorInRange: (from: number, to: number) => boolean;
  syntaxTree: ReturnType<typeof syntaxTree>;
}

export interface DecorationFamily {
  // Human-readable name for debugging / bundle analysis.
  name: string;
  // scan() emits zero or more decoration ranges for the visible viewport.
  // Tree-driven families (headings, emphasis, code, links, lists, tables,
  // blockquote, fenced-code, image) walk ctx.syntaxTree.iterate. Non-tree
  // families (wiki-links, which aren't standard markdown) regex over
  // ctx.state.doc inside ctx.visibleRange.
  scan(ctx: ScanContext): Array<Range<Decoration>>;
}

export function selectionTouches(state: EditorState, from: number, to: number): boolean {
  for (const range of state.selection.ranges) {
    if (range.from <= to && range.to >= from) return true;
  }
  return false;
}

// Wraps a tree-driven scan: iterates the syntax tree once, dispatches
// matching nodes to the family-supplied builder. Most inline + block
// families are built on this.
export function treeFamily<T extends string>(opts: {
  name: string;
  nodeTypes: readonly T[];
  build(args: {
    node: SyntaxNodeRef;
    state: EditorState;
    isCursorInRange: boolean;
  }): Array<Range<Decoration>> | null;
}): DecorationFamily {
  const types = new Set<string>(opts.nodeTypes);
  return {
    name: opts.name,
    scan(ctx) {
      const out: Array<Range<Decoration>> = [];
      ctx.syntaxTree.iterate({
        from: ctx.visibleRange.from,
        to: ctx.visibleRange.to,
        enter: (node) => {
          if (!types.has(node.name)) return;
          const ranges = opts.build({
            node,
            state: ctx.state,
            isCursorInRange: ctx.isCursorInRange(node.from, node.to),
          });
          if (ranges) out.push(...ranges);
        },
      });
      return out;
    },
  };
}

// Compose a list of families into one ViewPlugin. Iterates the tree
// once per visible range, hands each family the same context, then
// merges the produced decoration ranges into a single sorted RangeSet.
export function liveDecorationPlugin(families: readonly DecorationFamily[]): Extension {
  return ViewPlugin.fromClass(
    class {
      decorations: DecorationSet;

      constructor(view: EditorView) {
        this.decorations = this.build(view);
      }

      // T2 / TN4 segmented rebuild policy:
      //  - selectionSet:    immediate rebuild (cheap; no parse needed).
      //  - docChanged:      rebuild within the same frame (CM coalesces).
      //  - viewportChanged: rebuild for the new visible range.
      // All three trigger here; the cost discrimination happens inside
      // the families' scan() (e.g., widget memoization in TN3).
      update(update: ViewUpdate) {
        const forceRebuild = update.transactions.some((tr) =>
          tr.effects.some((e) => e.is(rebuildLivePreview)),
        );
        if (
          update.docChanged ||
          update.viewportChanged ||
          update.selectionSet ||
          forceRebuild
        ) {
          this.decorations = this.build(update.view);
        }
      }

      build(view: EditorView): DecorationSet {
        const tree = syntaxTree(view.state);
        const isCursorInRange = (from: number, to: number) =>
          selectionTouches(view.state, from, to);
        const collected: Array<Range<Decoration>> = [];

        for (const visibleRange of view.visibleRanges) {
          const ctx: ScanContext = {
            state: view.state,
            view,
            visibleRange,
            isCursorInRange,
            syntaxTree: tree,
          };
          for (const family of families) {
            collected.push(...family.scan(ctx));
          }
        }

        // RangeSetBuilder requires sorted-by-start adds; tie-break on end
        // and on inclusiveStart so block decorations precede inline marks
        // at the same position.
        collected.sort((a, b) => a.from - b.from || a.to - b.to);

        const builder = new RangeSetBuilder<Decoration>();
        for (const range of collected) {
          builder.add(range.from, range.to, range.value);
        }
        return builder.finish();
      }
    },
    {
      decorations: (v) => v.decorations,
    },
  );
}

// Re-exported so per-family modules import only from this file.
export { Decoration, EditorView };
export type { Range, SyntaxNodeRef };
