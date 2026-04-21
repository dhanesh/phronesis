#!/usr/bin/env node
// @constraint O6 — weekly flake-rate computation extracted for testability.
// Reads Playwright JSON reporter results.json files from FLAKE_DATA_DIR (default: flake-data/).
// Writes flake-rate.txt (float string) and GITHUB_STEP_SUMMARY (markdown summary).
// Exit 0 always — alerting decision is the caller's job (see flake-monitor.yml).
'use strict';

const fs = require('fs');
const path = require('path');

const dataDir = process.env.FLAKE_DATA_DIR ?? 'flake-data';
const outputFile = process.env.FLAKE_RATE_OUTPUT ?? 'flake-rate.txt';
const summaryFile = process.env.GITHUB_STEP_SUMMARY ?? '/dev/null';

if (!fs.existsSync(dataDir)) {
  console.log('No flake data found — nothing to compute.');
  process.exit(0);
}

const runs = fs.readdirSync(dataDir).filter(e => {
  try { return fs.statSync(path.join(dataDir, e)).isDirectory(); } catch { return false; }
});

let totalSpecs = 0;
let totalFlakes = 0;
const specFlakes = {};

for (const run of runs) {
  const resultsPath = path.join(dataDir, run, 'results.json');
  if (!fs.existsSync(resultsPath)) continue;
  try {
    const report = JSON.parse(fs.readFileSync(resultsPath, 'utf8'));
    for (const suite of (report.suites ?? [])) {
      for (const spec of (suite.specs ?? [])) {
        totalSpecs++;
        const retried = spec.tests?.some(t =>
          t.results?.some(r => r.retry > 0 && r.status === 'passed')
        );
        if (retried) {
          totalFlakes++;
          specFlakes[spec.title] = (specFlakes[spec.title] ?? 0) + 1;
        }
      }
    }
  } catch (err) {
    console.warn(`Skipping ${resultsPath}: ${err.message}`);
  }
}

const flakeRate = totalSpecs > 0 ? (totalFlakes / totalSpecs * 100).toFixed(2) : '0.00';
console.log(`Flake rate: ${flakeRate}% (${totalFlakes}/${totalSpecs} specs retried-then-passed)`);
if (Object.keys(specFlakes).length > 0) {
  console.log('Per-spec flake counts:', JSON.stringify(specFlakes, null, 2));
}

const summary = [
  '## E2E Flake Rate Report',
  '',
  `**Trailing ${runs.length} runs** | **Rate: ${flakeRate}%** | Threshold: 5%`,
  '',
  Object.entries(specFlakes).map(([k, v]) => `- \`${k}\`: ${v} flakes`).join('\n') || '_No flakes detected_',
].join('\n');

try { fs.writeFileSync(summaryFile, summary); } catch {}
fs.writeFileSync(outputFile, flakeRate);
