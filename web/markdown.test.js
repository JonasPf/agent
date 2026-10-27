'use strict';

const test = require('node:test');
const assert = require('node:assert');
const { renderMarkdown, markdownSettled, splitMentions } = require('./markdown.js');

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

// ---------- streaming ----------

// An answer arrives a few characters at a time. Rendering all of it again for
// every few characters is quadratic in its length, so what can no longer change
// is rendered once and only the rest is redrawn. That is only sound if splitting
// there renders exactly as the whole does, and if nothing more arriving could
// change what was already settled.
const streamed = [
  '# Findings\n\nThe sensor reads **high** on Tuesdays.\nIt reads low otherwise.\n\n' +
    '- one\n- two\n  continued\n- three\n\n1. first\n\n2. second\n\n' +
    '```go\nfunc main() {\n\n\tfmt.Println("x")\n}\n```\n\nAfter the code.\n\n' +
    '| a | b |\n|---|---|\n| 1 | 2 |\n\n> quoted\n> still\n\n---\n\n## Next\nDone.',
  'para\n# heading straight after\nmore text\n- item after text\n```\nunclosed fence\n\nstill code',
  '- item\n  ```\n  not a fence\n\n  later\n  ```\n\nafter\n\n~~~\ntilde\n~~~\n',
  'a | b\n--- | ---\nx | y\nplain\n\n#hashtag is not a heading\n## but this is\n',
];

test('a settled split renders exactly as the whole does', () => {
  for (const doc of streamed) {
    for (let n = 0; n <= doc.length; n++) {
      const prefix = doc.slice(0, n);
      const cut = markdownSettled(prefix);
      assert.ok(cut >= 0 && cut <= prefix.length, `cut ${cut} outside ${JSON.stringify(prefix)}`);
      assert.strictEqual(renderMarkdown(prefix.slice(0, cut)) + renderMarkdown(prefix.slice(cut)),
        renderMarkdown(prefix), `split at ${cut} of ${JSON.stringify(prefix)}`);
    }
  }
});

test('what is settled stays as it was rendered, however the answer goes on', () => {
  for (const doc of streamed) {
    const whole = renderMarkdown(doc);
    let last = 0;
    for (let n = 0; n <= doc.length; n++) {
      const cut = markdownSettled(doc.slice(0, n));
      assert.ok(cut >= last, `settled went back from ${last} to ${cut}`);
      last = cut;
      const settled = renderMarkdown(doc.slice(0, cut));
      assert.ok(whole.startsWith(settled),
        `settled ${JSON.stringify(doc.slice(0, cut))} rendered differently once more arrived`);
    }
  }
});

test('most of a long answer settles while it arrives', () => {
  const doc = Array.from({ length: 30 }, (_, i) => 'Paragraph ' + i + ' says something.').join('\n\n');
  assert.ok(markdownSettled(doc) > doc.length * 0.9, 'a long answer settled almost none of itself');
});

// The renderer holds finished fragments aside behind a NUL-numbered marker.
// Model output can carry a NUL of its own, and one shaped like a marker used
// to be read as one: inside a code span it named the span itself, and the
// renderer looped for ever, freezing the page each time the conversation was
// opened; elsewhere it printed "undefined". Run apart, so a loop is a failure
// rather than a hung suite.
test('a NUL in model output is shown, and never read as a marker', () => {
  const { spawnSync } = require('child_process');
  const script = `
    const { renderMarkdown } = require(${JSON.stringify(require.resolve('./markdown.js'))});
    process.stdout.write(JSON.stringify([
      renderMarkdown('\`\\u00000\\u0000\`'),
      renderMarkdown('ok \\u00005\\u0000 here'),
      renderMarkdown('**a \\u00000\\u0000** [x](https://example.com)'),
    ]));`;
  const run = spawnSync(process.execPath, ['-e', script], { timeout: 3000, encoding: 'utf8' });
  assert.strictEqual(run.signal, null, 'the renderer never returned');
  assert.strictEqual(run.status, 0, run.stderr);
  const [code, text, mixed] = JSON.parse(run.stdout);
  for (const html of [code, text, mixed]) {
    assert.doesNotMatch(html, /undefined/, 'a stray marker rendered as undefined: ' + html);
    assert.doesNotMatch(html, /\u0000/, 'a NUL reached the page: ' + html);
  }
  assert.match(code, /<code>�0�<\/code>/);
  assert.match(text, /ok �5� here/);
  assert.match(mixed, /<a href="https:\/\/example.com"/);
});

// ---- files a conversation names ----

// file stands in for the page, which knows two files and where each opens.
const files = { 'out.mp4': '/f/out.mp4', 'audio/clean.wav': '/f/audio/clean.wav' };
const file = name => files[name] || null;

test('a file named in a code span is a link to it', () => {
  const h = renderMarkdown('Saved to `out.mp4`.', { file });
  assert.match(h, /<a class="file" href="\/f\/out\.mp4" target="_blank" rel="noopener noreferrer"><code>out\.mp4<\/code><\/a>\.<\/p>/);
});

test('a file named in running text is a link to it, and the full stop after it is not', () => {
  const h = renderMarkdown('The audio is in audio/clean.wav.', { file });
  assert.match(h, /in <a class="file" href="\/f\/audio\/clean\.wav"[^>]*>audio\/clean\.wav<\/a>\.<\/p>/);
});

test('a file named in emphasis is linked and stays emphasised', () => {
  const h = renderMarkdown('**out.mp4** is ready', { file });
  assert.match(h, /<strong><a class="file" href="\/f\/out\.mp4"[^>]*>out\.mp4<\/a><\/strong>/);
});

test('a name that is not a file there stays text', () => {
  const h = renderMarkdown('See `draft.mp4`, notes.txt and https://example.com/out.mp4', { file });
  assert.doesNotMatch(h, /class="file"/);
});

test('without a way to find files, nothing is linked as one', () => {
  assert.doesNotMatch(renderMarkdown('`out.mp4` and out.mp4'), /<a/);
});

test('plain text splits into the files it names and the text between them', () => {
  assert.deepStrictEqual(splitMentions('wrote out.mp4, then "audio/clean.wav".', file), [
    { text: 'wrote ' }, { text: 'out.mp4', href: '/f/out.mp4' }, { text: ', then "' },
    { text: 'audio/clean.wav', href: '/f/audio/clean.wav' }, { text: '".' }]);
  assert.deepStrictEqual(splitMentions('nothing here', file), [{ text: 'nothing here' }]);
});
