'use strict';

const test = require('node:test');
const assert = require('node:assert');
const { renderMarkdown } = require('./markdown.js');

// The agent writes markdown whether or not anything renders it. What must never
// happen is the other direction: model output becoming markup of its own.
test('html in the source is escaped, never emitted', () => {
  const h = renderMarkdown('<script>alert(1)</script> & <b>bold</b>');
  assert.ok(!/<script>/.test(h), h);
  assert.ok(!/<b>/.test(h), h);
  assert.match(h, /&lt;script&gt;/);
  assert.match(h, /&amp;/);
});

test('a plain line is one paragraph', () => {
  assert.strictEqual(renderMarkdown('just words'), '<p>just words</p>');
});

test('a blank line separates paragraphs, a single newline breaks a line', () => {
  const h = renderMarkdown('one\ntwo\n\nthree');
  assert.match(h, /<p>one<br>two<\/p>/);
  assert.match(h, /<p>three<\/p>/);
});

test('emphasis, strong, strike and code spans render', () => {
  const h = renderMarkdown('**a** *b* ~~c~~ `d`');
  assert.match(h, /<strong>a<\/strong>/);
  assert.match(h, /<em>b<\/em>/);
  assert.match(h, /<del>c<\/del>/);
  assert.match(h, /<code>d<\/code>/);
});

test('nothing inside a code span is markup', () => {
  const h = renderMarkdown('use `a * b * c` and `<div>`');
  assert.match(h, /<code>a \* b \* c<\/code>/);
  assert.match(h, /<code>&lt;div&gt;<\/code>/);
  assert.ok(!/<em>/.test(h), h);
});

test('headings render at their level', () => {
  assert.match(renderMarkdown('# Title'), /<h1>Title<\/h1>/);
  assert.match(renderMarkdown('### Deep'), /<h3>Deep<\/h3>/);
  assert.match(renderMarkdown('####### too deep'), /<p>####### too deep<\/p>/);
});

test('a fenced block keeps its content verbatim and names its language', () => {
  const h = renderMarkdown('```go\nif x < 1 {\n  *p = 2\n}\n```');
  assert.match(h, /<pre data-lang="go"><code>if x &lt; 1 \{\n  \*p = 2\n\}<\/code><\/pre>/);
});

// A turn arrives token by token, so the last fence of a streaming message has
// not been typed yet. It must still render as code, not as loose text.
test('an unterminated fence still renders as a code block', () => {
  assert.match(renderMarkdown('```\nhalf a li'), /<pre><code>half a li<\/code><\/pre>/);
});

test('bullet and numbered lists render as lists', () => {
  const ul = renderMarkdown('- one\n- two');
  assert.match(ul, /<ul><li>one<\/li><li>two<\/li><\/ul>/);
  const ol = renderMarkdown('1. one\n2. two');
  assert.match(ol, /<ol><li>one<\/li><li>two<\/li><\/ol>/);
});

test('a wrapped list item keeps its continuation', () => {
  assert.match(renderMarkdown('- one\n  still one\n- two'), /<li>one<br>still one<\/li>/);
});

test('a blockquote renders as a quote', () => {
  assert.match(renderMarkdown('> quoted\n> still'), /<blockquote><p>quoted<br>still<\/p><\/blockquote>/);
});

test('a rule renders as a rule', () => {
  assert.match(renderMarkdown('---'), /<hr>/);
});

test('a pipe table renders as a table', () => {
  const h = renderMarkdown('| a | b |\n| --- | --- |\n| 1 | 2 |');
  assert.match(h, /<table><thead><tr><th>a<\/th><th>b<\/th><\/tr><\/thead>/);
  assert.match(h, /<tbody><tr><td>1<\/td><td>2<\/td><\/tr><\/tbody><\/table>/);
});

test('a link opens away from the app', () => {
  const h = renderMarkdown('see [docs](https://example.com/a?x=1&y=2)');
  assert.match(h, /<a href="https:\/\/example\.com\/a\?x=1&amp;y=2" target="_blank" rel="noopener noreferrer">docs<\/a>/);
});

// The only place in the interface that builds HTML from model output is also
// the only place a crafted link could execute. It cannot.
test('a link that is not http, mailto, or in-app renders as text', () => {
  const h = renderMarkdown('[click](javascript:alert(1)) and [d](data:text/html,x)');
  assert.ok(!/<a /.test(h), h);
  assert.match(h, /click/);
});

test('a bare url is linked', () => {
  assert.match(renderMarkdown('at https://example.com/x now'),
    /<a href="https:\/\/example\.com\/x"[^>]*>https:\/\/example\.com\/x<\/a>/);
});

test('an image renders as a link to it, not as a remote fetch', () => {
  const h = renderMarkdown('![a cat](https://example.com/c.png)');
  assert.ok(!/<img/.test(h), h);
  assert.match(h, /<a href="https:\/\/example\.com\/c\.png"[^>]*>a cat<\/a>/);
});

test('empty input renders nothing', () => {
  assert.strictEqual(renderMarkdown(''), '');
  assert.strictEqual(renderMarkdown(null), '');
});
