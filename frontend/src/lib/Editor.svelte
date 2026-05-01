<script>
  import { onDestroy, onMount } from 'svelte';
  import { Compartment, EditorSelection, EditorState } from '@codemirror/state';
  import { keymap, EditorView } from '@codemirror/view';
  import { markdown, markdownLanguage } from '@codemirror/lang-markdown';
  import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands';
  import { syntaxHighlighting, defaultHighlightStyle } from '@codemirror/language';
  import { DURABILITY_STATES } from './durability.js';
  import DurabilityIndicator from './DurabilityIndicator.svelte';
  import { livePreviewExtension, rebuildLivePreview } from './editor/decorations/index.js';

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
  // Tracks the current page name for the live-preview decoration registry
  // so wiki-link widgets can self-style as `current` without a CodeMirror
  // facet round-trip. Initialised in onMount; updated by the
  // page-reconfigure $effect below.
  let currentPageName = '';

  const editableCompartment = new Compartment();

  function createState(doc) {
    return EditorState.create({
      doc,
      extensions: [
        history(),
        keymap.of([...defaultKeymap, ...historyKeymap, indentWithTab]),
        // markdownLanguage is the GFM-enabled variant exported by
        // @codemirror/lang-markdown. Tables, task lists, strikethrough,
        // subscript, superscript, and emoji parse as distinct Lezer node
        // types — required by U2 (Full V1 coverage) and verified by
        // RT-1's parsing probe.
        markdown({ base: markdownLanguage }),
        syntaxHighlighting(defaultHighlightStyle),
        EditorView.lineWrapping,
        editableCompartment.of(EditorView.editable.of(!readOnly)),
        livePreviewExtension({
          currentPage: () => currentPageName,
          onnavigate: (target) => onnavigate?.({ page: target, source: 'wikilink' }),
        }),
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
    currentPageName = page;
    view = new EditorView({
      state: createState(value),
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

  // Editability is reconfigured via the CM compartment on prop change. The
  // current-page identity is consumed by the live-preview decoration plugin
  // through a closure (currentPageName), updated here so wiki-link widgets
  // pick up the new page name on next rebuild without a CM facet.
  $effect(() => {
    if (!view) return;
    currentPageName = page;
    view.dispatch({
      effects: [
        editableCompartment.reconfigure(EditorView.editable.of(!readOnly)),
        rebuildLivePreview.of(),
      ],
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
