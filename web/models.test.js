'use strict';

const test = require('node:test');
const assert = require('node:assert');
const { perMillion, contextLabel, modelName, modelFacts, sortModels, openRouterURL } = require('./models.js');

// A model is chosen on what it costs, what it holds, and how it measured. The
// gateway reports price per token, which nobody reads; a person compares
// dollars per million.

test('a price per token reads as dollars per million', () => {
  assert.strictEqual(perMillion(0.00001), '$10');
  assert.strictEqual(perMillion(0.000003), '$3');
  assert.strictEqual(perMillion(0.0000125), '$12.50');
  assert.strictEqual(perMillion(0.00000025), '$0.25');
  assert.strictEqual(perMillion(0.00000002), '$0.02');
  assert.strictEqual(perMillion(0), 'free');
});

test('a context window reads in thousands or millions', () => {
  assert.strictEqual(contextLabel(1000000), '1M');
  assert.strictEqual(contextLabel(1048576), '1M');
  assert.strictEqual(contextLabel(200000), '200k');
  assert.strictEqual(contextLabel(32768), '33k');
  assert.strictEqual(contextLabel(0), '');
});

test('a model is named without the provider the id already carries', () => {
  assert.strictEqual(modelName({ id: 'anthropic/claude-fable-5.1', name: 'Anthropic: Claude Fable 5.1' }), 'Claude Fable 5.1');
  assert.strictEqual(modelName({ id: 'x/y', name: 'Plain' }), 'Plain');
  assert.strictEqual(modelName({ id: 'x/unnamed' }), 'x/unnamed');
});

const fable = {
  id: 'anthropic/claude-fable-5.1', name: 'Anthropic: Claude Fable 5.1', created: 300,
  context_length: 1000000, prompt_price: 0.00001, completion_price: 0.00005,
  intelligence_index: 53.4, coding_index: 81.6, agentic_index: 58,
};
const sonnet = {
  id: 'anthropic/claude-sonnet-5', name: 'Anthropic: Claude Sonnet 5', created: 200,
  context_length: 200000, prompt_price: 0.000003, completion_price: 0.000015,
  intelligence_index: 38.4, coding_index: 71.5, agentic_index: 44.3,
};
const unmeasured = {
  id: 'small/unmeasured', name: 'Small: Unmeasured', created: 400,
  context_length: 32000, prompt_price: 0.0000001, completion_price: 0.0000002,
};

test('the facts are price in and out, context, and each index the gateway reports', () => {
  assert.deepStrictEqual(modelFacts(fable).map(f => f.join(' ')),
    ['in $10', 'out $50', 'context 1M', 'intelligence 53.4', 'coding 81.6', 'agentic 58']);
});

// An unmeasured model has no score. Showing 0 would rank it below every model
// that was measured, which is a claim nobody made.
test('an index nobody measured is left out rather than shown as zero', () => {
  assert.deepStrictEqual(modelFacts(unmeasured).map(f => f[0]), ['in', 'out', 'context']);
});

test('models sort by intelligence, with the unmeasured last', () => {
  assert.deepStrictEqual(sortModels([unmeasured, sonnet, fable], 'intelligence').map(m => m.id),
    [fable.id, sonnet.id, unmeasured.id]);
});

test('models sort by price, cheapest first', () => {
  assert.deepStrictEqual(sortModels([fable, sonnet, unmeasured], 'price').map(m => m.id),
    [unmeasured.id, sonnet.id, fable.id]);
});

test('models sort by context, largest first, and by release, newest first', () => {
  assert.deepStrictEqual(sortModels([unmeasured, sonnet, fable], 'context').map(m => m.id),
    [fable.id, sonnet.id, unmeasured.id]);
  assert.deepStrictEqual(sortModels([fable, sonnet, unmeasured], 'newest').map(m => m.id),
    [unmeasured.id, fable.id, sonnet.id]);
});

test('sorting leaves the catalogue it was given alone', () => {
  const list = [unmeasured, sonnet, fable];
  sortModels(list, 'intelligence');
  assert.deepStrictEqual(list.map(m => m.id), [unmeasured.id, sonnet.id, fable.id]);
});

// OpenRouter's page for a model is where its providers, uptime, and full
// description are. A variant such as :batch has no page of its own.
test('a model links to its page on OpenRouter', () => {
  assert.strictEqual(openRouterURL('anthropic/claude-fable-5.1'), 'https://openrouter.ai/anthropic/claude-fable-5.1');
  assert.strictEqual(openRouterURL('anthropic/claude-fable-5.1:batch'), 'https://openrouter.ai/anthropic/claude-fable-5.1');
});
