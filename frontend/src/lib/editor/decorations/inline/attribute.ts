// Attribute syntax decoration: `[priority:: high]`, `[owner:: dhanesh]`.
// Inline metadata pairs that render as compact "key: value" pills.
//
// Lezer markdown parses `[name:: value]` as a Link node WITHOUT a URL
// child (no `(...)` portion follows). This family detects that shape:
//   1. Tree match Link nodes
//   2. Filter to those with no URL child
//   3. Filter to those whose text matches `<key>::\s*<value>`
//   4. Skip `[!type]` patterns (those are admonition prefixes — handled
//      by admonition.ts)
//
// Satisfies: U2 (attribute pairs — bonus beyond original V1 list), U1
// (cursor-in reveals raw `[k:: v]` source), S2 (textContent only).

import { WidgetType } from '@codemirror/view';
import type { Range } from '@codemirror/state';
import { Decoration, treeFamily } from '../base';
import type { DecorationFamily, SyntaxNodeRef } from '../base';

const ATTR_REGEX = /^\[([^[\]:]+?)::\s*(.*?)\]$/;

class AttributePillWidget extends WidgetType {
  constructor(
    private readonly key: string,
    private readonly value: string,
  ) {
    super();
  }
  eq(other: AttributePillWidget): boolean {
    return other.key === this.key && other.value === this.value;
  }
  toDOM(): HTMLElement {
    const wrap = document.createElement('span');
    wrap.className = 'cm-md-attribute';
    const k = document.createElement('span');
    k.className = 'cm-md-attribute-key';
    k.textContent = this.key;
    const v = document.createElement('span');
    v.className = 'cm-md-attribute-value';
    v.textContent = this.value;
    wrap.append(k, v);
    return wrap;
  }
  ignoreEvent(): boolean {
    return false;
  }
}

function hasUrlChild(node: SyntaxNodeRef): boolean {
  const stable = node.node;
  let child = stable.firstChild;
  while (child) {
    if (child.name === 'URL') return true;
    child = child.nextSibling;
  }
  return false;
}

export function attributeFamily(): DecorationFamily {
  return treeFamily({
    name: 'attribute',
    kind: 'inline',
    nodeTypes: ['Link'] as const,
    build({ node, state, isCursorInRange }): Array<Range<Decoration>> | null {
      // Bare `[...]` with no `(url)` is the attribute / admonition shape.
      if (hasUrlChild(node)) return null;
      const text = state.sliceDoc(node.from, node.to);
      // Skip admonition markers — handled by admonition.ts.
      if (/^\[!/.test(text)) return null;
      const m = ATTR_REGEX.exec(text);
      if (!m) return null;
      if (isCursorInRange) return null;
      const key = m[1].trim();
      const value = m[2].trim();
      if (key.length === 0) return null;
      return [
        Decoration.replace({
          widget: new AttributePillWidget(key, value),
          inclusive: false,
        }).range(node.from, node.to),
      ];
    },
  });
}
