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
            background: 'transparent',
            color: 'var(--text-primary)'
          },
          '.cm-scroller': {
            fontFamily: 'var(--font-system)',
            lineHeight: '1.7',
            padding: '1.1rem 0 3rem'
          },
          '.cm-content': {
            padding: '0 0.35rem 0 0',
            caretColor: 'var(--text-primary)'
          },
          '.cm-focused': {
            outline: 'none'
          },
          '.cm-cursor, .cm-dropCursor': {
            borderLeftColor: 'var(--text-primary)'
          },
          '.cm-selectionBackground, ::selection': {
            backgroundColor: 'var(--bg-selected)'
          },
          '.cm-wikilink': {
            color: 'var(--accent)',
            textDecoration: 'none',
            background: 'var(--accent-bg)',
            borderRadius: 'var(--radius-pill)',
            padding: '0.08rem 0.42rem'
          },
          '.cm-wikilink.current': {
            background: 'color-mix(in oklab, var(--accent) 22%, transparent)'
          },
          // Live-preview decoration styles. Class contract documented in
          // docs/silverbullet-like-live-preview/README.md and asserted by
          // frontend/tests/e2e/live-preview/.
          '.cm-md-line-heading': {
            fontWeight: '600',
            color: 'var(--text-primary)'
          },
          '.cm-md-line-heading-1': { fontSize: '1.85em', lineHeight: '1.25', padding: '0.4rem 0 0.2rem' },
          '.cm-md-line-heading-2': { fontSize: '1.5em',  lineHeight: '1.3',  padding: '0.35rem 0 0.15rem' },
          '.cm-md-line-heading-3': { fontSize: '1.25em', lineHeight: '1.35', padding: '0.3rem 0 0.1rem' },
          '.cm-md-line-heading-4': { fontSize: '1.1em',  padding: '0.25rem 0' },
          '.cm-md-line-heading-5': { fontSize: '1.0em',  textTransform: 'uppercase', letterSpacing: '0.06em' },
          '.cm-md-line-heading-6': { fontSize: '0.95em', color: 'var(--text-secondary)' },
          '.cm-md-strong':   { fontWeight: '700' },
          '.cm-md-emphasis': { fontStyle: 'italic' },
          '.cm-md-inline-code': {
            fontFamily: 'var(--font-mono)',
            background: 'var(--bg-control)',
            padding: '0.05rem 0.35rem',
            borderRadius: 'var(--radius-sm)',
            fontSize: '0.92em'
          },
          '.cm-md-link': {
            color: 'var(--accent)',
            textDecoration: 'underline',
            textUnderlineOffset: '3px'
          },
          '.cm-md-list-marker': {
            color: 'var(--accent)',
            fontWeight: '600'
          },
          '.cm-md-hashtag': {
            color: 'var(--accent)',
            textDecoration: 'none',
            background: 'var(--accent-bg)',
            borderRadius: 'var(--radius-pill)',
            padding: '0.05rem 0.4rem',
            fontSize: '0.92em',
            cursor: 'pointer'
          },
          '.cm-md-hashtag:hover': {
            background: 'color-mix(in oklab, var(--accent) 22%, transparent)'
          },
          '.cm-md-task': {
            display: 'inline-flex',
            verticalAlign: 'middle',
            marginRight: '0.3rem'
          },
          '.cm-md-task-checkbox': {
            cursor: 'pointer',
            margin: '0',
            accentColor: 'var(--accent)'
          },
          '.cm-md-image': {
            display: 'inline-block',
            maxWidth: '100%',
            maxHeight: '24rem',
            borderRadius: 'var(--radius-md)',
            verticalAlign: 'middle'
          },
          '.cm-md-fenced-code-line': {
            fontFamily: 'var(--font-mono)',
            background: 'var(--bg-control)'
          },
          '.cm-md-fenced-code-fence': {
            color: 'var(--text-tertiary)'
          },
          '.cm-md-fenced-code-language': {
            position: 'relative'
          },
          '.cm-md-fenced-code-language::before': {
            content: 'attr(data-lang)',
            float: 'right',
            color: 'var(--text-tertiary)',
            fontSize: '0.78em',
            textTransform: 'uppercase',
            letterSpacing: '0.08em',
            paddingRight: '4.5rem'
          },
          '.cm-md-fenced-code-copy': {
            float: 'right',
            border: '1px solid var(--border-subtle)',
            background: 'var(--bg-elevated)',
            color: 'var(--text-secondary)',
            fontSize: '0.78em',
            padding: '0.05rem 0.5rem',
            borderRadius: 'var(--radius-sm)',
            cursor: 'pointer',
            margin: '0 0.25rem 0 0'
          },
          '.cm-md-fenced-code-copy:hover': {
            background: 'var(--bg-hover)',
            borderColor: 'var(--border-strong)'
          },
          '.cm-md-blockquote': {
            background: 'var(--bg-hover)',
            borderLeft: '3px solid var(--border-strong)',
            paddingLeft: '0.75rem',
            color: 'var(--text-secondary)'
          },
          '.cm-md-blockquote-marker': {
            color: 'var(--text-tertiary)'
          },
          '.cm-md-admonition': {
            borderLeftWidth: '4px',
            borderLeftStyle: 'solid',
            paddingLeft: '0.85rem'
          },
          '.cm-md-admonition-note':      { borderLeftColor: 'var(--accent)',  background: 'var(--accent-bg)' },
          '.cm-md-admonition-tip':       { borderLeftColor: 'var(--success)', background: 'color-mix(in oklab, var(--success) 14%, transparent)' },
          '.cm-md-admonition-warning':   { borderLeftColor: 'var(--warning)', background: 'color-mix(in oklab, var(--warning) 14%, transparent)' },
          '.cm-md-admonition-caution':   { borderLeftColor: 'var(--warning)', background: 'color-mix(in oklab, var(--warning) 14%, transparent)' },
          '.cm-md-admonition-important': { borderLeftColor: 'var(--purple)',  background: 'color-mix(in oklab, var(--purple) 14%, transparent)' },
          '.cm-md-admonition-danger':    { borderLeftColor: 'var(--danger)',  background: 'color-mix(in oklab, var(--danger) 14%, transparent)' },
          '.cm-md-frontmatter': {
            display: 'flex',
            flexWrap: 'wrap',
            gap: '0.4rem',
            margin: '0.4rem 0 0.8rem',
            padding: '0.5rem 0.6rem',
            background: 'var(--bg-control)',
            border: '1px solid var(--border-subtle)',
            borderRadius: 'var(--radius-md)',
            fontSize: '0.88em'
          },
          '.cm-md-frontmatter-chip': {
            display: 'inline-flex',
            alignItems: 'center',
            gap: '0.3rem',
            padding: '0.1rem 0.55rem',
            background: 'var(--bg-elevated)',
            border: '1px solid var(--border-subtle)',
            borderRadius: 'var(--radius-pill)'
          },
          '.cm-md-frontmatter-key': {
            color: 'var(--text-secondary)',
            fontWeight: '600'
          },
          '.cm-md-frontmatter-value': {
            color: 'var(--text-primary)'
          },
          '.cm-md-attribute': {
            display: 'inline-flex',
            alignItems: 'center',
            gap: '0.25rem',
            padding: '0.05rem 0.45rem',
            background: 'var(--accent-bg)',
            color: 'var(--accent)',
            borderRadius: 'var(--radius-sm)',
            fontSize: '0.92em',
            verticalAlign: 'baseline'
          },
          '.cm-md-attribute-key': {
            fontWeight: '600'
          },
          '.cm-md-attribute-key::after': {
            content: '":"',
            margin: '0 0.15rem 0 0'
          },
          '.cm-md-table': {
            borderCollapse: 'collapse',
            margin: '0.4rem 0',
            fontSize: '0.95em'
          },
          '.cm-md-table-header, .cm-md-table-cell': {
            border: '1px solid var(--border-subtle)',
            padding: '0.35rem 0.6rem',
            textAlign: 'left'
          },
          '.cm-md-table-header': {
            background: 'var(--bg-control)',
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
