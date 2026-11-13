'use strict';

// Hosting an MCP App (ADR-090): a page a bundle's tool brought, shown in a box
// with no network and no origin. Everything the page can do, it asks for here
// in a message, and what is not listed here it cannot do.

// APP_POLICY is what the page may load: what is in its own text and nothing
// from anywhere else. A bundle may say which domains its page would like; this
// host grants none, which the specification allows a host to do.
const APP_POLICY = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; " +
  "img-src data: blob:; media-src data: blob:; font-src data:; connect-src 'none'; " +
  "object-src 'none'; form-action 'none'; base-uri 'none'";

// appFrameDoc is the document the box is given: the policy, then the page.
function appFrameDoc(html) {
  return '<!doctype html><meta http-equiv="Content-Security-Policy" content="' + APP_POLICY + '">' + html;
}

// appHost answers one page. o.app is what the agent said of the call that
// opened it; o.send posts a message to the page; o.callTool and o.sendMessage
// do the two things a page can cause, and reject when the agent refuses.
function appHost(o) {
  let started = false;
  const reply = (id, result) => o.send({ jsonrpc: '2.0', id, result });
  const refuse = (id, code, message) => o.send({ jsonrpc: '2.0', id, error: { code, message } });
  const requests = {
    'ui/initialize': () => ({
      protocolVersion: '2026-01-26',
      hostInfo: { name: 'agent', version: o.version || 'dev' },
      hostCapabilities: { serverTools: {}, logging: {} },
      hostContext: {
        toolInfo: { tool: o.app.tool },
        theme: o.theme || 'light',
        displayMode: 'fullscreen',
        availableDisplayModes: ['fullscreen'],
        platform: 'web',
      },
    }),
    'ping': () => ({}),
    'ui/request-display-mode': () => ({ mode: 'fullscreen' }),
    'tools/call': p => o.callTool(String((p || {}).name || ''), (p || {}).arguments || {}),
    'ui/message': async p => {
      const c = (p || {}).content || {};
      if ((p || {}).role !== 'user' || c.type !== 'text' || !String(c.text || '').trim()) {
        throw Object.assign(new Error('a page sends a message as {role: "user", content: {type: "text", text}}'), { code: -32602 });
      }
      await o.sendMessage(String(c.text));
      return {};
    },
  };
  return {
    // receive takes one message from the page. It answers a request once, and
    // ignores what is not a message at all.
    async receive(m) {
      if (!m || m.jsonrpc !== '2.0' || typeof m.method !== 'string') return;
      if (m.id === undefined || m.id === null) {
        // The page says it is ready: only now is it told what opened it.
        if (m.method === 'ui/notifications/initialized' && !started) {
          started = true;
          o.send({ jsonrpc: '2.0', method: 'ui/notifications/tool-input', params: { arguments: o.app.arguments || {} } });
          o.send({ jsonrpc: '2.0', method: 'ui/notifications/tool-result', params: o.app.result });
        }
        return;
      }
      const handle = requests[m.method];
      if (!handle) return refuse(m.id, -32601, m.method + ' is not something this host does for a page');
      try {
        reply(m.id, await handle(m.params));
      } catch (e) {
        refuse(m.id, e.code || -32000, e.message);
      }
    },
  };
}

if (typeof module !== 'undefined' && module.exports) {
  module.exports = { appHost, appFrameDoc, APP_POLICY };
}
