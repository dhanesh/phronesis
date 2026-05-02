// YAML frontmatter decoration: a `---\n...\n---` block at the start of
// the document is rendered as a compact metadata pill bar when the
// cursor is outside the block. When the cursor enters the range, the
// raw YAML source is revealed for editing.
//
// Lezer markdown's default parser does not recognise frontmatter — it
// sees the opening `---` as a HorizontalRule and the body+closing
// `---` as a SetextHeading2. So this family detects the block by
// regex over the doc start, not by tree traversal.
//
// Parsing strategy is intentionally minimal: split the body on lines,
// match `key: value` per line, ignore anything else. List values like
// `[draft, internal]` are kept as-is in the chip text.
//
// Satisfies: U2 (frontmatter — bonus beyond original V1 list), U1
// (cursor-in reveals source for the entire block), T1 (visual-only),
// S2 (chips set via textContent).

import { WidgetType } from '@codemirror/view';
import type { Range } from '@codemirror/state';
import { Decoration } from '../base';
import type { DecorationFamily } from '../base';

const FRONTMATTER_REGEX = /^---\r?\n([\s\S]*?)\r?\n---(\r?\n|$)/;

interface FrontmatterEntry {
  key: string;
  value: string;
}

function parseFrontmatter(body: string): FrontmatterEntry[] {
  const entries: FrontmatterEntry[] = [];
  for (const rawLine of body.split(/\r?\n/)) {
    const line = rawLine.trim();
    if (line === '' || line.startsWith('#')) continue;
    const colonIdx = line.indexOf(':');
    if (colonIdx <= 0) continue;
    const key = line.slice(0, colonIdx).trim();
    const value = line.slice(colonIdx + 1).trim();
    if (key.length === 0) continue;
    entries.push({ key, value });
  }
  return entries;
}

class FrontmatterPillsWidget extends WidgetType {
  constructor(private readonly entries: ReadonlyArray<FrontmatterEntry>) {
    super();
  }

  eq(other: FrontmatterPillsWidget): boolean {
    if (other.entries.length !== this.entries.length) return false;
    for (let i = 0; i < this.entries.length; i++) {
      if (
        other.entries[i].key !== this.entries[i].key ||
        other.entries[i].value !== this.entries[i].value
      ) {
        return false;
      }
    }
    return true;
  }

  toDOM(): HTMLElement {
    const wrap = document.createElement('div');
    wrap.className = 'cm-md-frontmatter';
    for (const entry of this.entries) {
      const chip = document.createElement('span');
      chip.className = 'cm-md-frontmatter-chip';
      const k = document.createElement('span');
      k.className = 'cm-md-frontmatter-key';
      k.textContent = entry.key;
      const v = document.createElement('span');
      v.className = 'cm-md-frontmatter-value';
      v.textContent = entry.value;
      chip.append(k, v);
      wrap.appendChild(chip);
    }
    return wrap;
  }

  ignoreEvent(): boolean {
    return false;
  }
}

export function frontmatterFamily(): DecorationFamily {
  return {
    name: 'frontmatter',
    kind: 'block',
    scan({ state, isCursorInRange }) {
      // Frontmatter, if it exists, is only at the very start of the
      // document. Slice the first ~1KB to keep this cheap.
      const head = state.sliceDoc(0, Math.min(state.doc.length, 1024));
      const m = FRONTMATTER_REGEX.exec(head);
      if (!m) return [];
      const blockEnd = m[0].length;
      // Cursor inside the frontmatter range — leave bare source.
      if (isCursorInRange(0, blockEnd)) return [];

      const entries = parseFrontmatter(m[1]);
      if (entries.length === 0) return [];

      const out: Array<Range<Decoration>> = [];
      out.push(
        Decoration.replace({
          widget: new FrontmatterPillsWidget(entries),
          block: true,
        }).range(0, blockEnd),
      );
      return out;
    },
  };
}
