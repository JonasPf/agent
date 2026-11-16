'use strict';

// The interface is drawn with one small system, the tokens at the top of
// style.css (specs/webui.html, "Look"). A literal text size, corner, shadow or
// transition in a rule is a value outside it, chosen in one place and missed
// in the rest; this keeps them in the tokens.

const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

const css = fs.readFileSync(path.join(__dirname, 'style.css'), 'utf8');
// The rules, without the blocks that define the tokens.
const rules = css.replace(/:root\s*\{[^}]*\}/g, '');

function found(re) {
  return rules.split('\n').map((l, i) => [i + 1, l]).filter(([, l]) => re.test(l)).map(([n, l]) => n + ': ' + l.trim());
}

test('every text size comes from the five of the scale', () => {
  assert.deepStrictEqual(found(/font(-size)?:\s*(\d{3}\s+)?[0-9.]+(px|r?em|%)/), []);
  const sizes = [...css.matchAll(/--t-(\w+):\s*([0-9.]+)px/g)].map(m => Number(m[2]));
  assert.deepStrictEqual(sizes, [12, 14, 16, 18, 24]);
});

test('every corner, shadow and transition comes from its tokens', () => {
  assert.deepStrictEqual(found(/border-radius:\s*[^;]*\b\d+px/), [], 'a corner in pixels');
  assert.deepStrictEqual(found(/box-shadow:\s*[^;]*rgba?\(/), [], 'a shadow of its own');
  assert.deepStrictEqual(found(/transition:\s*[^;]*\b[0-9.]+m?s\b/), [], 'a transition with a time of its own');
  assert.deepStrictEqual(found(/cubic-bezier\(/), [], 'a curve of its own');
});
