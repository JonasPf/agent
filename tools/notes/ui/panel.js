// A tool panel: a module the interface mounts under this tool's name.
// It reads its data through the API, not by querying the database directly.
export default async function ({ root, api, el }) {
  const out = el('div');
  const inp = el('input', 'text');
  inp.placeholder = 'add a note…';
  inp.onchange = async () => { await call('add', inp.value); inp.value = ''; refresh(); };
  root.append(inp, out);

  async function call(action, text) {
    return api('/tools/notes/call', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ action, text })
    });
  }
  async function refresh() {
    const r = await call('list');
    out.innerHTML = '';
    const pre = el('pre', null, (r && r.content) || '');
    pre.style.whiteSpace = 'pre-wrap';
    out.append(pre);
  }
  refresh();
}
