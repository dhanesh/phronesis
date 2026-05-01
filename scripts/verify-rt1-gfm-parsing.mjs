#!/usr/bin/env node
// RT-1 verification probe for the silverbullet-like-live-preview manifold.
//
// Confirms that @codemirror/lang-markdown's exported markdownLanguage
// (the "GFM plus subscript/superscript/emoji" variant) parses GFM
// constructs into distinguishable Lezer node names — without us adding
// any dependency beyond what's already in frontend/package.json.
//
// Outputs a flat list of node names found in a sample document. The
// list MUST contain Table, TableRow, TableCell, FencedCode, Blockquote,
// Image, Heading types, etc. for U2 (Full V1) to be achievable under
// Option A.
//
// Run: node scripts/verify-rt1-gfm-parsing.mjs

import { markdownLanguage } from '../frontend/node_modules/@codemirror/lang-markdown/dist/index.js';

const sample = `# Heading

Paragraph with **bold**, *italic*, \`inline code\`, and a [link](https://example.com).

- list item one
- list item two
1. ordered one

\`\`\`javascript
const fenced = "code block";
\`\`\`

> blockquote line one
> blockquote line two

| col a | col b |
| ----- | ----- |
| cell  | cell  |

![image alt](/media/abc123)

[[wiki-page-link]]
`;

const tree = markdownLanguage.parser.parse(sample);
const seen = new Set();
const cursor = tree.cursor();
do {
  seen.add(cursor.name);
} while (cursor.next());

const required = [
  'ATXHeading1',
  'StrongEmphasis',
  'Emphasis',
  'InlineCode',
  'Link',
  'BulletList',
  'OrderedList',
  'FencedCode',
  'Blockquote',
  'Table',
  'TableRow',
  'TableCell',
  'Image',
];

const present = required.filter((t) => seen.has(t));
const missing = required.filter((t) => !seen.has(t));

console.log('All node names found:', [...seen].sort().join(', '));
console.log('');
console.log(`Required nodes present: ${present.length}/${required.length}`);
if (missing.length > 0) {
  console.log('MISSING:', missing.join(', '));
  console.log('RT-1: NOT_SATISFIED');
  process.exit(1);
}
console.log('RT-1: SATISFIED');
