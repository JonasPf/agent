'use strict';

const test = require('node:test');
const assert = require('node:assert');
const { appHost, appFrameDoc, APP_POLICY } = require('./apphost.js');

const APP = {
  tool: { name: 'show', description: 'Show a page.', inputSchema: { type: 'object' } },
  arguments: { note: 'hello' },
  result: { content: [{ type: 'text', text: 'The page is shown.' }], structuredContent: { value: 42 } },
};

function host(over) {
  const sent = [], called = [], said = [];
  const h = appHost(Object.assign({
    app: APP,
    send: m => sent.push(m),
    callTool: async (name, args) => { called.push([name, args]); return { content: [{ type: 'text', text: 'fresh' }] }; },
    sendMessage: async text => { said.push(text); },
  }, over));
  return { h, sent, called, said };
}

test('a page is told nothing until it says it is ready, then the call that opened it, once', async () => {
  const { h, sent } = host();
  await h.receive({ jsonrpc: '2.0', id: 1, method: 'ui/initialize', params: { appCapabilities: {} } });
  assert.strictEqual(sent.length, 1, 'the host spoke before the page was ready');
  assert.strictEqual(sent[0].id, 1);
  assert.strictEqual(sent[0].result.protocolVersion, '2026-01-26');
  assert.strictEqual(sent[0].result.hostContext.toolInfo.tool.name, 'show');
  await h.receive({ jsonrpc: '2.0', method: 'ui/notifications/initialized' });
  await h.receive({ jsonrpc: '2.0', method: 'ui/notifications/initialized' });
  assert.deepStrictEqual(sent.slice(1).map(m => m.method), ['ui/notifications/tool-input', 'ui/notifications/tool-result']);
  assert.deepStrictEqual(sent[1].params, { arguments: { note: 'hello' } });
  assert.strictEqual(sent[2].params.structuredContent.value, 42, 'the page is not handed the whole result');
});

test('a page calls a tool through the agent, and a refusal comes back as an error', async () => {
  const { h, sent, called } = host();
  await h.receive({ jsonrpc: '2.0', id: 7, method: 'tools/call', params: { name: 'refresh', arguments: { a: 1 } } });
  assert.deepStrictEqual(called, [['refresh', { a: 1 }]]);
  assert.deepStrictEqual(sent[0], { jsonrpc: '2.0', id: 7, result: { content: [{ type: 'text', text: 'fresh' }] } });
  const no = host({ callTool: async () => { throw new Error('quiet is not a tool this page may call'); } });
  await no.h.receive({ jsonrpc: '2.0', id: 8, method: 'tools/call', params: { name: 'quiet' } });
  assert.strictEqual(no.sent[0].id, 8);
  assert.match(no.sent[0].error.message, /may call/);
  assert.strictEqual(no.sent[0].result, undefined);
});

test('a page sends the conversation a message, and only a message in words', async () => {
  const { h, sent, said } = host();
  await h.receive({ jsonrpc: '2.0', id: 2, method: 'ui/message', params: { role: 'user', content: { type: 'text', text: 'Use take two.' } } });
  assert.deepStrictEqual(said, ['Use take two.']);
  assert.deepStrictEqual(sent[0], { jsonrpc: '2.0', id: 2, result: {} });
  for (const params of [{ role: 'assistant', content: { type: 'text', text: 'x' } }, { role: 'user', content: { type: 'image', data: 'x' } }, { role: 'user', content: { type: 'text', text: '  ' } }, undefined]) {
    await h.receive({ jsonrpc: '2.0', id: 3, method: 'ui/message', params });
  }
  assert.strictEqual(said.length, 1, 'a message that is not the operator’s words was sent');
  assert.strictEqual(sent.filter(m => m.error).length, 4);
});

test('what this host does not do for a page is refused by name, and noise is ignored', async () => {
  const { h, sent } = host();
  for (const method of ['ui/open-link', 'ui/update-model-context', 'resources/read', 'sampling/createMessage']) {
    await h.receive({ jsonrpc: '2.0', id: method, method, params: {} });
  }
  assert.deepStrictEqual(sent.map(m => m.error.code), [-32601, -32601, -32601, -32601]);
  assert.match(sent[0].error.message, /ui\/open-link/);
  const before = sent.length;
  for (const m of [null, 'hello', { ask: 'answer' }, { jsonrpc: '2.0' }, { jsonrpc: '2.0', method: 'notifications/message', params: {} },
    { jsonrpc: '2.0', method: 'ui/notifications/size-changed', params: { width: 1, height: 1 } }, { jsonrpc: '2.0', id: 5, result: {} }]) {
    await h.receive(m);
  }
  assert.strictEqual(sent.length, before, 'the host answered something that asked nothing');
  await h.receive({ jsonrpc: '2.0', id: 9, method: 'ping' });
  assert.deepStrictEqual(sent[sent.length - 1], { jsonrpc: '2.0', id: 9, result: {} });
});

test('the box is given a policy that allows nothing from anywhere else', () => {
  const doc = appFrameDoc('<p>hi</p>');
  assert.ok(doc.indexOf('Content-Security-Policy') < doc.indexOf('<p>hi</p>'), 'the policy does not come before the page');
  assert.match(APP_POLICY, /default-src 'none'/);
  assert.match(APP_POLICY, /connect-src 'none'/);
  assert.doesNotMatch(APP_POLICY, /https?:|\*|'self'/);
});
