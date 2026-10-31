'use strict';

// The panel behind ask_operator. Its pure parts are driven directly; the Send
// and Decline buttons are driven through a small DOM, because the promise worth
// testing is that only those buttons reach the conversation.

const test = require('node:test');
const assert = require('node:assert');
const fs = require('fs');
const path = require('path');

const SOURCE = path.join(__dirname, '..', 'tools', 'ask_operator', 'ui', 'panel.js');
const load = () => import('data:text/javascript,' + encodeURIComponent(fs.readFileSync(SOURCE, 'utf8')));

test('a link names the conversation and the ask', async () => {
  const { parseLink } = await load();
  assert.deepStrictEqual(parseLink('#toolpanel/ask_operator/1A18/20261031-120000-ab12'),
    { session: '1A18', ask: '20261031-120000-ab12' });
  assert.strictEqual(parseLink('#toolpanel/ask_operator'), null);
});

test('a page goes in the box with nothing allowed but itself', async () => {
  const { frameDoc } = await load();
  const doc = frameDoc('<!doctype html><html><body>hi</body></html>');
  assert.ok(doc.startsWith('<!doctype html>'), 'without a doctype first, the page renders in quirks mode');
  const policy = doc.indexOf('Content-Security-Policy'), page = doc.indexOf('<body>');
  assert.ok(policy > 0 && policy < page, 'the policy comes before anything the page can run');
  assert.match(doc, /default-src 'none'/);
  assert.doesNotMatch(doc, /connect-src [^'n]/, 'a page reaches nothing over the network');
  assert.ok(doc.indexOf('window.ask') < page, 'ask is there before the page runs');
});

test('an answer says what was asked, and everything that came back', async () => {
  const { answerText } = await load();
  const text = answerText({ message: 'Which voice?' }, 'accept', {
    values: [['voice', 'gareth'], ['speed', 'normal']], proposed: 'looks right',
    saved: ['review/chunks.json'], note: 'keep it short'
  });
  assert.match(text, /^Answered: Which voice\?/);
  assert.match(text, /voice: gareth/);
  assert.match(text, /looks right/);
  assert.match(text, /Saved: review\/chunks\.json/);
  assert.match(text, /keep it short/);
});

test('a decline is said as one, with the reason if there is one', async () => {
  const { answerText } = await load();
  assert.strictEqual(answerText({ message: 'Which voice?' }, 'decline', { note: '' }), 'Declined: Which voice?');
  assert.match(answerText({ message: 'Which voice?' }, 'decline', { note: 'not now' }), /not now/);
});

function fakeEl(tag, cls, text) {
  const n = {
    tag, className: cls || '', textContent: text === undefined ? '' : String(text), children: [],
    style: {}, attrs: {}, value: '', checked: false, hidden: false, disabled: false,
    append(...k) { for (const x of k) if (x) this.children.push(x); },
    setAttribute(k, v) { this.attrs[k] = v; },
    set onclick(f) { this.click = f; }, get onclick() { return this.click; }
  };
  return n;
}
function all(n, out = []) { out.push(n); for (const c of n.children || []) all(c, out); return out; }
const button = (root, label) => all(root).find(n => n.tag === 'button' && n.textContent === label);

async function mounted(ask, id) {
  const { default: mount } = await load();
  const posts = [], listeners = {};
  const api = async (url, opts) => {
    if (opts && opts.method === 'POST') {
      const body = JSON.parse(opts.body);
      posts.push({ url, body });
      return url.startsWith('/tools/') ? { ok: true, content: 'review/' + body.save } : null;
    }
    if (url.endsWith('.json')) return ask;
    return { session: { title: 'Voice Over' } };
  };
  globalThis.location = { hash: '#toolpanel/ask_operator/S1/' + id };
  globalThis.window = {
    addEventListener(k, f) { listeners[k] = f; },
    removeEventListener(k) { delete listeners[k]; }
  };
  globalThis.fetch = async () => ({ text: async () => '<p>the page</p>' });
  const root = fakeEl('div');
  await mount({ root, api, el: fakeEl });
  return { root, posts, listeners };
}

// The box as the panel sees it: a window that posts messages and receives them.
function boxOf(root) {
  const frame = all(root).find(n => n.tag === 'iframe');
  frame.contentWindow = { sent: [], postMessage(m) { this.sent.push(m); } };
  return frame;
}

test('nothing reaches the conversation until Send is pressed', async () => {
  const ask = { message: 'Which voice?', questions: [{ name: 'voice', type: 'choice', options: ['gareth', 'brian'], required: true }] };
  const { root, posts } = await mounted(ask, 'a1');
  assert.strictEqual(posts.length, 0);
  const shown = all(root).map(n => n.textContent).join(' ');
  assert.match(shown, /Voice Over/, 'it says which conversation is asking');
  assert.match(shown, /Which voice\?/);
  assert.match(shown, /passwords/, 'it warns where an answer goes');

  all(root).find(n => n.tag === 'select').value = 'brian';
  await button(root, 'Send').onclick();
  assert.deepStrictEqual(posts.map(p => p.url), ['/sessions/S1/messages']);
  assert.match(posts[0].body.text, /voice: brian/);
});

test('a required answer holds Send back', async () => {
  const ask = { message: 'Name?', questions: [{ name: 'name', type: 'text', required: true }] };
  const { root, posts } = await mounted(ask, 'a2');
  await button(root, 'Send').onclick();
  assert.strictEqual(posts.length, 0);
  assert.match(all(root).map(n => n.textContent).join(' '), /name/);
});

test('Decline tells the conversation, and needs no answer', async () => {
  const ask = { message: 'Name?', questions: [{ name: 'name', type: 'text', required: true }] };
  const { root, posts } = await mounted(ask, 'a3');
  await button(root, 'Decline').onclick();
  assert.deepStrictEqual(posts.map(p => p.body.text), ['Declined: Name?']);
});

test('a page runs in a box with scripts and no origin', async () => {
  const { root } = await mounted({ message: 'Check it.', page: 'review/index.html' }, 'a4');
  const frame = all(root).find(n => n.tag === 'iframe');
  assert.strictEqual(frame.attrs.sandbox, 'allow-scripts');
  assert.match(frame.srcdoc, /the page/);
});

test('what a page proposes waits for Send', async () => {
  const { root, posts, listeners } = await mounted({ message: 'Check it.', page: 'review/index.html' }, 'a5');
  const frame = boxOf(root);
  await listeners.message({ source: frame.contentWindow, data: { ask: 'answer', text: 'approved' } });
  assert.strictEqual(posts.length, 0, 'a page reached the conversation by itself');
  assert.match(all(root).map(n => n.textContent).join(' '), /The page will send: approved/);
  await button(root, 'Send').onclick();
  assert.match(posts[0].body.text, /approved/);
});

test('a page saves through the tool, hears back, and the answer names the file', async () => {
  const { root, posts, listeners } = await mounted({ message: 'Check it.', page: 'review/index.html' }, 'a6');
  const frame = boxOf(root);
  await listeners.message({ source: frame.contentWindow, data: { ask: 'save', id: 7, name: 'chunks.json', content: '[]' } });
  assert.strictEqual(posts[0].url, '/tools/ask_operator/call?session_id=S1');
  assert.deepStrictEqual(posts[0].body, { ask: 'a6', save: 'chunks.json', content: '[]' });
  assert.deepStrictEqual(frame.contentWindow.sent, [{ id: 7, saved: 'review/chunks.json' }]);
  await button(root, 'Send').onclick();
  assert.match(posts[1].body.text, /Saved: review\/chunks\.json/);
});

test('a message from anywhere but the box is ignored', async () => {
  const { root, posts, listeners } = await mounted({ message: 'Check it.', page: 'review/index.html' }, 'a7');
  boxOf(root);
  await listeners.message({ source: {}, data: { ask: 'save', id: 1, name: 'x', content: 'y' } });
  await listeners.message({ source: {}, data: { ask: 'answer', text: 'forged' } });
  assert.strictEqual(posts.length, 0);
  assert.doesNotMatch(all(root).map(n => n.textContent).join(' '), /forged/);
});

test('a save too large for the API is refused to the page', async () => {
  const { root, posts, listeners } = await mounted({ message: 'Check it.', page: 'review/index.html' }, 'a8');
  const frame = boxOf(root);
  await listeners.message({ source: frame.contentWindow, data: { ask: 'save', id: 2, name: 'x', content: 'y'.repeat(1 << 20) } });
  assert.strictEqual(posts.length, 0);
  assert.match(frame.contentWindow.sent[0].error, /too large/);
});
