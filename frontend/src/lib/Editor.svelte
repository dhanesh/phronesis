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
          onTaskToggle: (from, to, checked) => {
            if (!view) return;
            view.dispatch({
              changes: { from, to, insert: checked ? '[ ]' : '[x]' }
            });
          },
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
          },
          // Live-preview decoration styles. Class contract documented in
          // docs/silverbullet-like-live-preview/README.md and asserted by
          // frontend/tests/e2e/live-preview/.
          '.cm-md-line-heading': {
            fontWeight: '600',
            color: '#1f241c'
          },
          '.cm-md-line-heading-1': { fontSize: '1.85em', lineHeight: '1.25', padding: '0.4rem 0 0.2rem' },
          '.cm-md-line-heading-2': { fontSize: '1.5em',  lineHeight: '1.3',  padding: '0.35rem 0 0.15rem' },
          '.cm-md-line-heading-3': { fontSize: '1.25em', lineHeight: '1.35', padding: '0.3rem 0 0.1rem' },
          '.cm-md-line-heading-4': { fontSize: '1.1em',  padding: '0.25rem 0' },
          '.cm-md-line-heading-5': { fontSize: '1.0em',  textTransform: 'uppercase', letterSpacing: '0.06em' },
          '.cm-md-line-heading-6': { fontSize: '0.95em', color: '#5d5847' },
          '.cm-md-strong':   { fontWeight: '700' },
          '.cm-md-emphasis': { fontStyle: 'italic' },
          '.cm-md-inline-code': {
            fontFamily: 'inherit',
            background: 'rgba(110, 97, 69, 0.12)',
            padding: '0.05rem 0.35rem',
            borderRadius: '6px',
            fontSize: '0.95em'
          },
          '.cm-md-link': {
            color: '#1f5c46',
            textDecoration: 'underline',
            textUnderlineOffset: '3px'
          },
          '.cm-md-list-marker': {
            color: '#854f1c',
            fontWeight: '600'
          },
          '.cm-md-hashtag': {
            color: '#854f1c',
            textDecoration: 'none',
            background: 'rgba(133, 79, 28, 0.12)',
            borderRadius: '999px',
            padding: '0.05rem 0.4rem',
            fontSize: '0.92em',
            cursor: 'pointer'
          },
          '.cm-md-hashtag:hover': {
            background: 'rgba(133, 79, 28, 0.22)'
          },
          '.cm-md-task': {
            display: 'inline-flex',
            verticalAlign: 'middle',
            marginRight: '0.3rem'
          },
          '.cm-md-task-checkbox': {
            cursor: 'pointer',
            margin: '0',
            accentColor: '#1f5c46'
          },
          '.cm-md-image': {
            display: 'inline-block',
            maxWidth: '100%',
            maxHeight: '24rem',
            borderRadius: '8px',
            verticalAlign: 'middle'
          },
          '.cm-md-fenced-code-line': {
            fontFamily: 'inherit',
            background: 'rgba(110, 97, 69, 0.08)'
          },
          '.cm-md-fenced-code-fence': {
            color: '#928b6f'
          },
          '.cm-md-fenced-code-language': {
            position: 'relative'
          },
          '.cm-md-fenced-code-language::before': {
            content: 'attr(data-lang)',
            float: 'right',
            color: '#928b6f',
            fontSize: '0.78em',
            textTransform: 'uppercase',
            letterSpacing: '0.08em',
            paddingRight: '4.5rem'
          },
          '.cm-md-fenced-code-copy': {
            float: 'right',
            border: '1px solid rgba(110, 97, 69, 0.25)',
            background: 'rgba(255, 252, 244, 0.85)',
            color: '#5d5847',
            fontSize: '0.78em',
            padding: '0.05rem 0.5rem',
            borderRadius: '4px',
            cursor: 'pointer',
            margin: '0 0.25rem 0 0'
          },
          '.cm-md-fenced-code-copy:hover': {
            background: '#fff',
            borderColor: 'rgba(110, 97, 69, 0.45)'
          },
          '.cm-md-blockquote': {
            background: 'rgba(133, 79, 28, 0.05)',
            borderLeft: '3px solid rgba(133, 79, 28, 0.45)',
            paddingLeft: '0.75rem',
            color: '#5d5847'
          },
          '.cm-md-blockquote-marker': {
            color: 'rgba(133, 79, 28, 0.55)'
          },
          '.cm-md-admonition': {
            borderLeftWidth: '4px',
            paddingLeft: '0.85rem',
            background: 'rgba(31, 92, 70, 0.06)'
          },
          '.cm-md-admonition-note':      { borderLeftColor: '#1f5c46', background: 'rgba(31, 92, 70, 0.06)' },
          '.cm-md-admonition-tip':       { borderLeftColor: '#256d3d', background: 'rgba(37, 109, 61, 0.06)' },
          '.cm-md-admonition-warning':   { borderLeftColor: '#a06a13', background: 'rgba(160, 106, 19, 0.08)' },
          '.cm-md-admonition-caution':   { borderLeftColor: '#a06a13', background: 'rgba(160, 106, 19, 0.08)' },
          '.cm-md-admonition-important': { borderLeftColor: '#7d3c8a', background: 'rgba(125, 60, 138, 0.07)' },
          '.cm-md-admonition-danger':    { borderLeftColor: '#a13a3a', background: 'rgba(161, 58, 58, 0.07)' },
          '.cm-md-table': {
            borderCollapse: 'collapse',
            margin: '0.4rem 0',
            fontSize: '0.95em'
          },
          '.cm-md-table-header, .cm-md-table-cell': {
            border: '1px solid rgba(110, 97, 69, 0.25)',
            padding: '0.35rem 0.6rem',
            textAlign: 'left'
          },
          '.cm-md-table-header': {
            background: 'rgba(110, 97, 69, 0.08)',
            fontWeight: '600'
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
    // Delegated click handler for hashtag mark decorations. Decoration
    // .mark cannot attach an event listener directly (unlike a widget),
    // so we delegate from the editor content root. Internal navigation
    // bypasses the browser-default href so we route through onnavigate.
    root.addEventListener('click', (event) => {
      const target = event.target;
      if (!(target instanceof HTMLElement)) return;
      const tag = target.closest('a.cm-md-hashtag');
      if (!tag) return;
      const tagName = tag.getAttribute('data-hashtag');
      if (!tagName) return;
      event.preventDefault();
      onnavigate?.({ page: tagName, source: 'hashtag' });
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
