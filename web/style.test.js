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

// A label reads as it is written, in sentence case (webui.html, "Look"):
// capitals set by the stylesheet shout, and are slower to read.
test('nothing is set in capitals by the stylesheet', () => {
  assert.deepStrictEqual(found(/text-transform:\s*uppercase/), []);
});

test('every corner, shadow and transition comes from its tokens', () => {
  assert.deepStrictEqual(found(/border-radius:\s*[^;]*\b\d+px/), [], 'a corner in pixels');
  assert.deepStrictEqual(found(/box-shadow:\s*[^;]*rgba?\(/), [], 'a shadow of its own');
  assert.deepStrictEqual(found(/transition:\s*[^;]*\b[0-9.]+m?s\b/), [], 'a transition with a time of its own');
  assert.deepStrictEqual(found(/cubic-bezier\(/), [], 'a curve of its own');
});

// Each accent the operator can choose is legible in both schemes: as text on
// the page and on a card, and under the text a filled button puts on it,
// hovered or not, at 4.5:1 or better (WCAG AA for body text).
function luminance(hex) {
  const c = [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16) / 255)
    .map(v => v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4);
  return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
}
function contrast(a, b) {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}
function colours(text) {
  return Object.fromEntries([...text.matchAll(/--([a-z0-9-]+):\s*(#[0-9a-f]{6})\b/gi)].map(m => [m[1], m[2].toLowerCase()]));
}
function darkScheme() {
  const at = css.indexOf('@media (prefers-color-scheme: dark)');
  let depth = 0;
  for (let i = css.indexOf('{', at); i < css.length; i++) {
    if (css[i] === '{') depth++;
    else if (css[i] === '}' && --depth === 0) return [at, css.slice(at, i + 1)];
  }
  throw new Error('the dark scheme never closes');
}

test('every accent reads at 4.5:1 or better in light and dark', () => {
  const [at, dark] = darkScheme();
  const light = css.slice(0, at);
  const names = [...new Set([...css.matchAll(/\[data-accent="([a-z]+)"\]/g)].map(m => m[1]))];
  assert.deepStrictEqual(names, ['copper', 'ink', 'plum', 'graphite']);
  for (const [scheme, text] of [['light', light], ['dark', dark]]) {
    const page = colours(text.match(/:root\s*\{([^}]*)\}/)[1]);
    for (const name of names) {
      const m = text.match(new RegExp('\\[data-accent="' + name + '"\\]\\s*\\{([^}]*)\\}'));
      assert.ok(m, name + ' has no colours in the ' + scheme + ' scheme');
      const t = colours(m[1]);
      for (const [fg, bg, where] of [
        [t.accent, page.bg, 'as text on the page'],
        [t.accent, page.card, 'as text on a card'],
        [t['on-accent'], t.accent, 'under a filled button\'s text'],
        [t['on-accent'], t['accent-2'], 'under a hovered button\'s text'],
      ]) {
        assert.ok(fg && bg, `${name} in ${scheme}: a colour is missing for ${where}`);
        const r = contrast(fg, bg);
        assert.ok(r >= 4.5, `${name} in ${scheme} reads at ${r.toFixed(2)}:1 ${where}`);
      }
    }
  }
});
