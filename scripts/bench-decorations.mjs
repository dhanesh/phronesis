#!/usr/bin/env node
// T2 perf benchmark for the silverbullet-like-live-preview manifold.
//
// Closes m5-verify gap G3: the manifold declares
//   T2 (boundary, statistical): p95 < 16ms decoration rebuild for
//                                a viewport-scoped pass over a 5KB doc.
// but had no benchmark to validate the distribution.
//
// What this measures: the syntax-tree walk that drives every
// DecorationFamily's scan(). Tree iteration over ~5KB of mixed-
// markdown is the dominant cost in inline rebuilds; widget
// construction adds a smaller, DOM-bound overhead that this Node-only
// bench can't run. The published p95 is therefore the *lower bound*
// on the real decoration build time — if the lower bound exceeds the
// budget, real-browser timing certainly will.
//
// Run: node scripts/bench-decorations.mjs
// Threshold: p95 < 16ms (one frame) by default; override with
//            P95_BUDGET_MS=N in the environment.

import { markdownLanguage } from '../frontend/node_modules/@codemirror/lang-markdown/dist/index.js';

// Fixture: ~5KB of mixed markdown that mirrors what real V1 pages
// contain. Headings + emphasis + code + lists + tables +
// blockquote + tasks + wiki-links + hashtags.
function buildFixture() {
  const parts = [];
  parts.push('---\ntitle: Bench Fixture\ntags: [bench, draft]\nstatus: review\n---\n');
  parts.push('# H1 heading\n## H2 heading\n### H3 heading\n');
  for (let i = 0; i < 12; i++) {
    parts.push(`Paragraph ${i} with **bold word**, *italic*, and \`code span\`.`);
    parts.push(`See [[wiki-page-${i}]] and visit [external](https://example.com/${i}).`);
    parts.push(`Tagged with #urgent #review-${i % 4} and a footnote ref [^n${i}].`);
    parts.push('');
  }
  parts.push('- [ ] todo task one');
  parts.push('- [x] done task two');
  parts.push('- list item');
  parts.push('  - nested item');
  parts.push('1. ordered first');
  parts.push('2. ordered second');
  parts.push('');
  parts.push('```javascript');
  parts.push('const f = (x) => x * 2;');
  parts.push('console.log(f(21));');
  parts.push('```');
  parts.push('');
  parts.push('> [!warning]');
  parts.push('> A blockquote admonition body.');
  parts.push('');
  parts.push('| col a | col b | col c |');
  parts.push('| ----- | ----- | ----- |');
  parts.push('| r1c1  | r1c2  | r1c3  |');
  parts.push('| r2c1  | r2c2  | r2c3  |');
  parts.push('');
  // Pad to ~5KB so the total walk approximates a real page.
  while (parts.join('\n').length < 5000) {
    parts.push(`Filler paragraph with **bold-${parts.length}**, [[wiki-${parts.length}]], #tag-${parts.length}.`);
  }
  return parts.join('\n');
}

// V1 node types every family iterates over. Mirrors the union of
// nodeTypes declared in frontend/src/lib/editor/decorations/{inline,block}/.
const V1_NODE_TYPES = new Set([
  'ATXHeading1', 'ATXHeading2', 'ATXHeading3',
  'ATXHeading4', 'ATXHeading5', 'ATXHeading6',
  'Emphasis', 'StrongEmphasis',
  'InlineCode',
  'Link',
  'Image',
  'ListMark',
  'TaskMarker',
  'FencedCode',
  'Blockquote',
  'Table',
]);

// Approximate the inline ViewPlugin's per-rebuild work: walk the tree
// over the visible byte range, count matches by node type, and
// (cheaply) slice the doc text for each match — same operations the
// families do before constructing widget DOM.
function simulateRebuild(tree, source, viewportFrom, viewportTo) {
  let matches = 0;
  let slicedBytes = 0;
  const cursor = tree.cursor();
  // Walk full tree but only count matches within viewport range —
  // matches what the inline ViewPlugin's scan does in practice.
  while (cursor.next()) {
    if (cursor.from > viewportTo) break;
    if (cursor.to < viewportFrom) continue;
    if (V1_NODE_TYPES.has(cursor.name)) {
      matches++;
      slicedBytes += source.slice(cursor.from, cursor.to).length;
    }
  }
  return { matches, slicedBytes };
}

function percentile(samplesSortedAsc, p) {
  const n = samplesSortedAsc.length;
  if (n === 0) return 0;
  const idx = Math.min(n - 1, Math.floor((p / 100) * n));
  return samplesSortedAsc[idx];
}

const BUDGET_P95_MS = Number(process.env.P95_BUDGET_MS ?? 16);
const ITERATIONS = Number(process.env.ITER ?? 200);
const WARMUP = 20;

const source = buildFixture();
const tree = markdownLanguage.parser.parse(source);
// Viewport = whole doc for V1 fixture (5KB fits one screen).
const viewport = { from: 0, to: source.length };

console.log(`Fixture: ${source.length} bytes`);
console.log(`Iterations: ${ITERATIONS} (${WARMUP} warmup)`);

// Warmup so JIT-tier compilation settles.
for (let i = 0; i < WARMUP; i++) {
  simulateRebuild(tree, source, viewport.from, viewport.to);
}

const samples = [];
let lastResult;
for (let i = 0; i < ITERATIONS; i++) {
  const t0 = performance.now();
  lastResult = simulateRebuild(tree, source, viewport.from, viewport.to);
  const t1 = performance.now();
  samples.push(t1 - t0);
}

samples.sort((a, b) => a - b);
const p50 = percentile(samples, 50);
const p95 = percentile(samples, 95);
const p99 = percentile(samples, 99);
const max = samples[samples.length - 1];

console.log('');
console.log(`Matches: ${lastResult.matches}, sliced bytes: ${lastResult.slicedBytes}`);
console.log(`Latency  p50: ${p50.toFixed(3)}ms`);
console.log(`Latency  p95: ${p95.toFixed(3)}ms`);
console.log(`Latency  p99: ${p99.toFixed(3)}ms`);
console.log(`Latency  max: ${max.toFixed(3)}ms`);
console.log(`Budget   p95: ${BUDGET_P95_MS}ms`);

if (p95 > BUDGET_P95_MS) {
  console.log('');
  console.log(`FAIL: p95 ${p95.toFixed(3)}ms exceeds budget ${BUDGET_P95_MS}ms`);
  process.exit(1);
}
console.log('PASS: p95 under budget.');
