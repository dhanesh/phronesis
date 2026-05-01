// Shared scaffold for live-preview decoration families.
// Satisfies: T4 (refined per TN2 — the *skeleton* is shared, individual
// families pick their own decoration shape: replace / mark / line / block).
// Constrains: T1 (visual-only — never dispatch doc changes from here),
//             T2 (viewport-scoped where possible, segmented rebuild),
//             O2 / TN8 (per-family code stays small by reusing this base).
//
// Two extension paths are exported because CM6 imposes a hard rule:
// block-level decorations (Decoration.line, Decoration.replace({block:
// true})) cannot come from a ViewPlugin — they must be provided by a
// StateField via EditorView.decorations.from(field). Inline-only
// decorations CAN come from a ViewPlugin (which is what we want for
// viewport scoping). So families declare a `kind`, and the composer
// routes them to the right path.

import { syntaxTree } from '@codemirror/language';
import { EditorState, RangeSetBuilder, StateEffect, StateField } from '@codemirror/state';
import type { Extension, Range, Transaction } from '@codemirror/state';
import { Decoration, EditorView, ViewPlugin } from '@codemirror/view';
import type { DecorationSet, ViewUpdate } from '@codemirror/view';
import type { SyntaxNodeRef } from '@lezer/common';

// Sent by Editor.svelte when an external dependency of the decoration set
// changes (e.g., the current page name used by wiki-link widgets to mark
// themselves "current"). Both the inline ViewPlugin and the block
// StateField rebuild on this signal.
export const rebuildLivePreview = StateEffect.define<void>();

export interface ScanContext {
  state: EditorState;
  // visibleRange covers the EditorView viewport for inline families and
  // [0, doc.length] for block families (StateField has no viewport).
  visibleRange: { from: number; to: number };
  isCursorInRange: (from: number, to: number) => boolean;
  syntaxTree: ReturnType<typeof syntaxTree>;
}

export interface DecorationFamily {
  // Human-readable name for debugging / bundle analysis.
  name: string;
  // 'inline' families produce only inline decorations (mark / replace
  // without block:true). They run in a ViewPlugin and are viewport-scoped.
  // 'block' families may produce Decoration.line or Decoration.replace
  // ({block: true}) and run in a StateField (full-doc scope).
  kind: 'inline' | 'block';
  // scan() emits zero or more decoration ranges. Tree-driven families
  // walk ctx.syntaxTree.iterate; non-tree families (wiki-links, which
  // aren't standard markdown) regex over ctx.state.doc inside the range.
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
  kind: 'inline' | 'block';
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
    kind: opts.kind,
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

// Walk a list of families against a single ScanContext, return a sorted
// DecorationSet. Shared by both extension paths below.
function buildDecorationSet(
  families: readonly DecorationFamily[],
  ctx: ScanContext,
): DecorationSet {
  const collected: Array<Range<Decoration>> = [];
  for (const family of families) {
    collected.push(...family.scan(ctx));
  }
  // RangeSetBuilder requires sorted-by-start adds; tie-break on end
  // so wider ranges starting at the same position are added in stable
  // order.
  collected.sort((a, b) => a.from - b.from || a.to - b.to);
  const builder = new RangeSetBuilder<Decoration>();
  for (const range of collected) {
    builder.add(range.from, range.to, range.value);
  }
  return builder.finish();
}

function transactionForcesRebuild(tr: Transaction): boolean {
  return tr.effects.some((e) => e.is(rebuildLivePreview));
}

// Inline path: ViewPlugin, viewport-scoped, segmented rebuild policy.
// CM6 allows inline decorations from plugins; this gives us cheap
// per-frame rebuilds and viewport scoping for free.
function inlineDecorationPlugin(families: readonly DecorationFamily[]): Extension {
  return ViewPlugin.fromClass(
    class {
      decorations: DecorationSet;

      constructor(view: EditorView) {
        this.decorations = this.build(view);
      }

      update(update: ViewUpdate) {
        const forceRebuild = update.transactions.some(transactionForcesRebuild);
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
            visibleRange,
            isCursorInRange,
            syntaxTree: tree,
          };
          for (const family of families) {
            collected.push(...family.scan(ctx));
          }
        }
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

// Block path: StateField, full-doc scope. Required by CM6 because block
// decorations cannot come from a ViewPlugin. Block-level constructs
// (tables, fenced code, blockquotes) are sparse, so full-doc scan per
// rebuild is cheap. Rebuild triggers: docChanged, selection change,
// rebuildLivePreview effect.
function blockDecorationField(families: readonly DecorationFamily[]): Extension {
  const compute = (state: EditorState): DecorationSet => {
    const isCursorInRange = (from: number, to: number) => selectionTouches(state, from, to);
    const ctx: ScanContext = {
      state,
      visibleRange: { from: 0, to: state.doc.length },
      isCursorInRange,
      syntaxTree: syntaxTree(state),
    };
    return buildDecorationSet(families, ctx);
  };

  const field = StateField.define<DecorationSet>({
    create: compute,
    update(decorations, tr) {
      const selectionChanged = tr.startState.selection.main.from !== tr.state.selection.main.from
        || tr.startState.selection.main.to !== tr.state.selection.main.to;
      if (tr.docChanged || selectionChanged || transactionForcesRebuild(tr)) {
        return compute(tr.state);
      }
      return decorations.map(tr.changes);
    },
    provide: (f) => EditorView.decorations.from(f),
  });

  return field;
}

// Top-level composer: partitions families by kind and wires up both
// extension paths. Used by livePreviewExtension in index.ts.
export function liveDecorationExtension(families: readonly DecorationFamily[]): Extension[] {
  const inline = families.filter((f) => f.kind === 'inline');
  const block = families.filter((f) => f.kind === 'block');
  const out: Extension[] = [];
  if (inline.length > 0) out.push(inlineDecorationPlugin(inline));
  if (block.length > 0) out.push(blockDecorationField(block));
  return out;
}

// Re-exported so per-family modules import only from this file.
export { Decoration, EditorView };
export type { Range, SyntaxNodeRef };
