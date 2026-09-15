'use strict';

// Pure helpers for choosing a model. Kept out of app.js so they can be tested
// without a DOM; app.js loads this file first and uses the globals.

// perMillion turns the gateway's price per token into what a person compares:
// dollars per million tokens, to the cent, with whole dollars left whole.
function perMillion(price) {
  const dollars = Math.round((price || 0) * 1e6 * 100) / 100;
  if (dollars <= 0) return price > 0 ? '<$0.01' : 'free';
  return '$' + (Number.isInteger(dollars) ? String(dollars) : dollars.toFixed(2));
}

function contextLabel(n) {
  if (!n) return '';
  if (n >= 1e6) return String(Math.round(n / 1e5) / 10).replace(/\.0$/, '') + 'M';
  return Math.round(n / 1000) + 'k';
}

// modelName drops the "Provider: " the gateway prefixes, which the id beneath
// it already says.
function modelName(m) {
  if (!m.name) return m.id;
  return m.name.replace(/^[^:]+:\s*/, '');
}

const INDICES = [['intelligence_index', 'intelligence'], ['coding_index', 'coding'], ['agentic_index', 'agentic']];

// modelFacts is what a row says about a model, in the order it is read: what it
// costs, what it holds, how it measured. A figure the gateway did not report is
// left out — an unmeasured model has no score, and zero is a score.
function modelFacts(m) {
  const out = [];
  if (typeof m.prompt_price === 'number') {
    out.push(['in', perMillion(m.prompt_price)], ['out', perMillion(m.completion_price)]);
  }
  if (m.context_length) out.push(['context', contextLabel(m.context_length)]);
  for (const [key, label] of INDICES) {
    if (typeof m[key] === 'number') out.push([label, String(m[key])]);
  }
  return out;
}

// Each sort ranks by one figure, best first. A model without that figure goes
// last rather than being counted as the worst; ties fall back to the id.
const SORT_KEYS = {
  intelligence: m => m.intelligence_index,
  price: m => typeof m.prompt_price === 'number' ? -(m.prompt_price + (m.completion_price || 0)) : undefined,
  context: m => m.context_length,
  newest: m => m.created,
};

function sortModels(list, by) {
  const key = SORT_KEYS[by] || SORT_KEYS.intelligence;
  const has = v => typeof v === 'number' && !isNaN(v);
  return (list || []).slice().sort((a, b) => {
    const x = key(a), y = key(b);
    if (has(x) !== has(y)) return has(x) ? -1 : 1;
    if (has(x) && x !== y) return y - x;
    return String(a.id).localeCompare(String(b.id));
  });
}

// openRouterURL is the model's page on OpenRouter. A variant such as :batch has
// no page of its own, so it links to the model it is a variant of.
function openRouterURL(id) {
  return 'https://openrouter.ai/' + String(id || '').split(':')[0];
}

if (typeof module !== 'undefined' && module.exports) {
  module.exports = { perMillion, contextLabel, modelName, modelFacts, sortModels, openRouterURL };
}
