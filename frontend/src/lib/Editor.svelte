<script>
  import { onDestroy, onMount } from 'svelte';
  import { Compartment, EditorSelection, EditorState, Facet, RangeSetBuilder } from '@codemirror/state';
  import { keymap, Decoration, EditorView, ViewPlugin, WidgetType } from '@codemirror/view';
  import { markdown } from '@codemirror/lang-markdown';
  import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands';
  import { syntaxHighlighting, defaultHighlightStyle } from '@codemirror/language';
  import { DURABILITY_STATES } from './durability.js';
  import DurabilityIndicator from './DurabilityIndicator.svelte';

  // INT-9: durability state is externally-driven by the parent (App.svelte)
  // based on autosave lifecycle. When server-side op_acked/op_saved events
  // are wired (future editor-feature wave), this prop can be replaced by an
  // internal tracker from durability.js reading the SSE stream.
  let {
    value = '',
    page = 'home',
    readOnly = false,
    durability = DURABILITY_STATES.IDLE,
    onchange,
    onnavigate,
  } = $props();

  let root;
  let view;
  let suppressChange = false;

  const editableCompartment = new Compartment();
  const pageCompartment = new Compartment();

  function normalizeWikiName(name) {
    return (name || '').trim().replaceAll(' ', '-').replaceAll(/^\/+/, '').toLowerCase();
  }

  function selectionTouches(selection, from, to) {
    for (const range of selection.ranges) {
      if (range.from <= to && range.to >= from) {
        return true;
      }
    }
    return false;
  }

  function wikiLinkDecorations() {
    return ViewPlugin.fromClass(
      class {
        constructor(view) {
          this.decorations = this.buildDecorations(view);
        }

        update(update) {
          if (
            update.docChanged ||
            update.selectionSet ||
            update.viewportChanged ||
            update.startState.facet(pageFacet) !== update.state.facet(pageFacet)
          ) {
            this.decorations = this.buildDecorations(update.view);
          }
        }

        buildDecorations(view) {
          const builder = new RangeSetBuilder();
          const text = view.state.doc.toString();
          const pageName = view.state.facet(pageFacet);
          const regex = /\[\[([^\]|]+)(?:\|([^\]]+))?\]\]/g;
          let match;
          while ((match = regex.exec(text))) {
            const from = match.index;
            const to = from + match[0].length;
            if (selectionTouches(view.state.selection, from, to)) {
              continue;
            }
            const target = normalizeWikiName(match[1]);
            const label = match[2] || match[1];
            builder.add(
              from,
              to,
              Decoration.replace({
                widget: new WikiLinkWidget(label, target, pageName),
                inclusive: false
              })
            );
          }
          return builder.finish();
        }
      },
      {
        decorations: (value) => value.decorations
      }
    );
  }

  class WikiLinkWidget extends WidgetType {
    constructor(label, target, currentPage) {
      super();
      this.label = label;
      this.target = target;
      this.currentPage = currentPage;
    }

    eq(other) {
      return other.label === this.label && other.target === this.target && other.currentPage === this.currentPage;
    }

    toDOM() {
      const anchor = document.createElement('a');
      anchor.className = `cm-wikilink${this.target === this.currentPage ? ' current' : ''}`;
      anchor.href = `/w/${this.target}`;
      anchor.textContent = this.label;
      anchor.dataset.wikiLink = this.target;
      anchor.title = `Open ${this.target}`;
      anchor.addEventListener('click', (event) => {
        event.preventDefault();
        onnavigate?.({ page: this.target, source: 'wikilink' });
      });
      return anchor;
    }

    ignoreEvent() {
      return false;
    }
  }

  const pageFacet = Facet.define({
    combine: (values) => values[0] ?? ''
  });

  function createState(doc, currentPage) {
    return EditorState.create({
      doc,
      extensions: [
        history(),
        keymap.of([...defaultKeymap, ...historyKeymap, indentWithTab]),
        markdown(),
        syntaxHighlighting(defaultHighlightStyle),
        EditorView.lineWrapping,
        editableCompartment.of(EditorView.editable.of(!readOnly)),
        pageCompartment.of(pageFacet.of(currentPage)),
        wikiLinkDecorations(),
        EditorView.updateListener.of((update) => {
          if (!update.docChanged || suppressChange) {
            return;
          }
          onchange?.({
            value: update.state.doc.toString(),
            selection: update.state.selection.main
          });
        }),
        EditorView.theme({
          '&': {
            height: '100%',
            fontSize: '16px',
            background: 'transparent'
          },
          '.cm-scroller': {
            fontFamily: '"IBM Plex Mono", monospace',
            lineHeight: '1.7',
            padding: '1.1rem 0 3rem'
          },
          '.cm-content': {
            padding: '0 0.35rem 0 0'
          },
          '.cm-focused': {
            outline: 'none'
          },
          '.cm-cursor, .cm-dropCursor': {
            borderLeftColor: '#1f241c'
          },
          '.cm-selectionBackground, ::selection': {
            backgroundColor: 'rgba(222, 184, 90, 0.28)'
          },
          '.cm-wikilink': {
            color: '#1f5c46',
            textDecoration: 'none',
            background: 'rgba(82, 134, 111, 0.12)',
            borderRadius: '999px',
            padding: '0.08rem 0.42rem'
          },
          '.cm-wikilink.current': {
            background: 'rgba(31, 92, 70, 0.22)'
          }
        })
      ]
    });
  }

  onMount(() => {
    view = new EditorView({
      state: createState(value, page),
      parent: root
    });
  });

  // Sync the prop value back into the editor when the parent updates it
  // (e.g., loading a different page or merging a server snapshot).
  $effect(() => {
    if (!view) return;
    const currentDoc = view.state.doc.toString();
    if (value !== currentDoc) {
      suppressChange = true;
      view.dispatch({
        changes: { from: 0, to: currentDoc.length, insert: value }
      });
      suppressChange = false;
    }
  });

  // Reconfigure compartments when editability or page identity changes.
  $effect(() => {
    if (!view) return;
    view.dispatch({
      effects: [
        editableCompartment.reconfigure(EditorView.editable.of(!readOnly)),
        pageCompartment.reconfigure(pageFacet.of(page))
      ]
    });
  });

  export function focus() {
    view?.focus();
  }

  export function moveToLink(target) {
    if (!view) return;
    const text = view.state.doc.toString();
    const link = `[[${target}`;
    const index = text.toLowerCase().indexOf(link.toLowerCase());
    if (index === -1) {
      view.focus();
      return;
    }
    view.dispatch({
      selection: EditorSelection.single(index, index + link.length),
      scrollIntoView: true
    });
    view.focus();
  }

  onDestroy(() => {
    view?.destroy();
  });
</script>

<div class="editor-shell">
  <div class="editor-chrome">
    <DurabilityIndicator state={durability} />
  </div>
  <div class="editor-root" bind:this={root}></div>
</div>

<style>
  .editor-shell {
    display: flex;
    flex-direction: column;
    height: 100%;
  }
  .editor-chrome {
    display: flex;
    justify-content: flex-end;
    padding: 0.25rem 0.5rem;
    min-height: 1.75rem;
  }
  .editor-root {
    flex: 1;
    min-height: 60vh;
  }
</style>
