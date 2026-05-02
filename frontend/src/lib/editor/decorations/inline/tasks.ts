// Task list checkbox decoration: `- [ ] todo` / `- [x] done`.
// Replaces the `[ ]` / `[x]` TaskMarker source with a real
// `<input type="checkbox">`. Clicking the box dispatches a doc-level
// change via the onToggle callback (Editor.svelte owns the
// EditorView, so the dispatch lives there — see V1Options).
//
// T1 invariant note: this family DOES wire up doc mutation, but only
// from a user-initiated click event, never from inside scan(). The
// decoration pipeline itself remains read-only.
//
// Satisfies: U1 (cursor-in reveals raw `[ ]` / `[x]` source), U2
// (task coverage — bonus beyond original V1 list), S2 (textContent
// for any cell-style children, none used here), TN3 (widget eq()
// compares checked state).

import { WidgetType } from '@codemirror/view';
import type { Range } from '@codemirror/state';
import { Decoration, treeFamily } from '../base';
import type { DecorationFamily } from '../base';

class TaskCheckboxWidget extends WidgetType {
  constructor(
    private readonly checked: boolean,
    private readonly markerFrom: number,
    private readonly markerTo: number,
    private readonly onToggle: (
      from: number,
      to: number,
      currentlyChecked: boolean,
    ) => void,
  ) {
    super();
  }

  eq(other: TaskCheckboxWidget): boolean {
    return (
      other.checked === this.checked &&
      other.markerFrom === this.markerFrom &&
      other.markerTo === this.markerTo
    );
  }

  toDOM(): HTMLElement {
    const wrap = document.createElement('span');
    wrap.className = 'cm-md-task';
    const checkbox = document.createElement('input');
    checkbox.type = 'checkbox';
    checkbox.className = 'cm-md-task-checkbox';
    checkbox.checked = this.checked;
    // Stop CodeMirror from intercepting the click as an editor event;
    // we handle it on mouseup so the click toggles cleanly without
    // moving the cursor into the source range.
    checkbox.addEventListener('mousedown', (e) => e.stopPropagation());
    checkbox.addEventListener('click', (e) => {
      e.stopPropagation();
      e.preventDefault();
      this.onToggle(this.markerFrom, this.markerTo, this.checked);
    });
    wrap.appendChild(checkbox);
    return wrap;
  }

  // Required so the editor still receives focus when clicking the
  // wrapping span (but not the input — input's preventDefault wins).
  ignoreEvent(): boolean {
    return false;
  }
}

export function tasksFamily(opts: {
  onToggle: (from: number, to: number, currentlyChecked: boolean) => void;
}): DecorationFamily {
  return treeFamily({
    name: 'tasks',
    kind: 'inline',
    nodeTypes: ['TaskMarker'] as const,
    build({ node, state, isCursorInRange }): Array<Range<Decoration>> | null {
      // When the cursor is inside the marker, leave the source bare so
      // the user can edit it directly. Otherwise replace [ ] / [x] with
      // the checkbox widget.
      if (isCursorInRange) return null;
      const text = state.sliceDoc(node.from, node.to);
      const checked = /\[[xX]\]/.test(text);
      return [
        Decoration.replace({
          widget: new TaskCheckboxWidget(checked, node.from, node.to, opts.onToggle),
          inclusive: false,
        }).range(node.from, node.to),
      ];
    },
  });
}
