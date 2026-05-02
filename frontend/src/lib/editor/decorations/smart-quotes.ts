// Smart-quotes input transform: typing `"` becomes `“` or `”` and `'`
// becomes `‘` or `’`, based on the preceding character. Inside code
// contexts (InlineCode, FencedCode, CodeText) the quote is left
// straight so source code keeps compiling.
//
// This is NOT a decoration — it modifies the document on user input via
// a CodeMirror keymap binding. T1's "decorations are visual-only" rule
// is unaffected because the change is event-driven, not pipeline-driven.
//
// Adapted from SilverBullet's smart_quotes.ts pattern (Apache-2.0).

import { EditorSelection } from '@codemirror/state';
import { syntaxTree } from '@codemirror/language';
import { keymap } from '@codemirror/view';
import type { Extension } from '@codemirror/state';
import type { KeyBinding } from '@codemirror/view';

const STRAIGHT_QUOTE_CONTEXTS = new Set<string>([
  'InlineCode',
  'FencedCode',
  'CodeText',
  'CodeBlock',
  'CommentBlock',
  'HTMLTag',
]);

function keyBindingForQuote(originalQuote: string, openQuote: string, closeQuote: string): KeyBinding {
  return {
    any: (target, event): boolean => {
      if (event.key !== originalQuote) return false;
      const cursorPos = target.state.selection.main.from;

      // Skip inside any code/HTML context — leave the straight quote.
      let node = syntaxTree(target.state).resolveInner(cursorPos);
      while (node) {
        if (STRAIGHT_QUOTE_CONTEXTS.has(node.type.name)) return false;
        if (!node.parent) break;
        node = node.parent;
      }

      const chBefore = target.state.sliceDoc(cursorPos - 1, cursorPos);
      // Open quote at start of input or after whitespace / opening punctuation;
      // close quote after a word character or closing punctuation.
      const useOpen = cursorPos === 0 || /[\s(\[{]/.test(chBefore);
      const replacement = useOpen ? openQuote : closeQuote;

      const changes = target.state.changeByRange((range) => {
        if (!range.empty) {
          // For non-empty selections, wrap with open + close.
          return {
            changes: [
              { from: range.from, insert: openQuote },
              { from: range.to, insert: closeQuote },
            ],
            range: EditorSelection.range(
              range.anchor + openQuote.length,
              range.head + openQuote.length,
            ),
          };
        }
        return {
          changes: { from: range.from, insert: replacement },
          range: EditorSelection.cursor(range.from + replacement.length),
        };
      });
      target.dispatch(target.state.update(changes, { userEvent: 'input.type' }));
      event.preventDefault();
      return true;
    },
  };
}

export function smartQuotesExtension(): Extension {
  return keymap.of([
    keyBindingForQuote('"', '“', '”'),
    keyBindingForQuote("'", '‘', '’'),
  ]);
}
