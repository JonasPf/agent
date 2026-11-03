// The screen behind ask_operator: it shows what the agent asked, as a form it
// draws or a page in a box, and it is the only thing that can answer. A page
// may be anything the agent wrote, so it runs with no origin and no network and
// reaches this screen through messages. It can save a file beside itself, propose
// an answer with the files Send should save, and hold Send back. Only the operator
// pressing Send or Decline, out here where the page cannot reach, says anything
// to the conversation (ADR-074, ADR-079).

// The policy put in front of a page. It matches filePolicy on the file route,
// and the page cannot loosen it: a policy can only be tightened after this one.
const POLICY = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; " +
  "img-src data: blob:; media-src data: blob:; font-src data:; form-action 'none'; base-uri 'none'";

// What a page calls. postMessage is the only way out of the box.
const BRIDGE = `<script>(function () {
  var next = 0, waiting = {};
  window.addEventListener('message', function (e) {
    if (e.source === parent && e.data && e.data.ask === 'flush') {
      parent.postMessage({ ask: 'flushed', id: e.data.id }, '*');
      return;
    }
    if (e.source !== parent || !e.data || !waiting[e.data.id]) return;
    var w = waiting[e.data.id];
    delete waiting[e.data.id];
    if (e.data.error) w[1](new Error(e.data.error)); else w[0](e.data.saved);
  });
  window.ask = {
    answer: function (text, files) { parent.postMessage({ ask: 'answer', text: String(text), files: files || {} }, '*'); },
    hold: function (why) { parent.postMessage({ ask: 'hold', text: String(why) }, '*'); },
    save: function (name, text) {
      return new Promise(function (ok, no) {
        var id = ++next;
        waiting[id] = [ok, no];
        parent.postMessage({ ask: 'save', id: id, name: String(name), content: String(text) }, '*');
      });
    }
  };
})();<\/script>`;

// The most a page may save in one file: the API takes a megabyte for a call.
const MAX_SAVE = 900 << 10;
const MAX_ANSWER = 64 << 10;
// How long Send waits for a page to catch up before it goes with what it has.
const CATCH_UP = 500;

// parseLink reads a screen's link. The router keeps only two parts of it.
export function parseLink(hash) {
  const [, , session, ask] = String(hash || '').replace(/^#/, '').split('/');
  return session && ask ? { session, ask } : null;
}

// frameDoc is the page as the box receives it, with the policy and ask in force
// before anything of the page's own can run. The doctype comes first because
// without one a page renders in quirks mode, whatever it declares later.
export function frameDoc(html) {
  return '<!doctype html><meta http-equiv="Content-Security-Policy" content="' + POLICY + '">' + BRIDGE + html;
}

// answerText is what the conversation receives: what was asked, what came back,
// and which of the two it was, so the agent never has to guess.
export function answerText(ask, action, r) {
  const lines = [(action === 'accept' ? 'Answered: ' : 'Declined: ') + ask.message];
  const body = [];
  for (const [name, value] of r.values || []) body.push(name + ': ' + value);
  if (r.proposed) body.push(r.proposed);
  if (r.saved && r.saved.length) body.push('Saved: ' + r.saved.join(', '));
  if (r.note) body.push((action === 'accept' ? 'Note: ' : 'Reason: ') + r.note);
  return body.length ? lines.concat('', body).join('\n') : lines[0];
}

// field draws one question and returns how to read its answer.
function field(el, q) {
  const label = q.label || q.name;
  const box = el('label', 'ask-field');
  box.style.display = 'grid';
  box.style.gap = '4px';
  box.append(el('span', null, label + (q.required ? ' (required)' : '')));
  let read;
  if (q.type === 'choices') {
    const on = String(q.default || '').split(',').map(s => s.trim());
    const ticks = q.options.map(o => {
      const row = el('label'), t = el('input');
      t.type = 'checkbox';
      t.checked = on.includes(o);
      row.append(t, el('span', null, ' ' + o));
      box.append(row);
      return [o, t];
    });
    read = () => ticks.filter(([, t]) => t.checked).map(([o]) => o).join(', ');
  } else if (q.type === 'choice' || q.type === 'yes_no') {
    const s = el('select');
    const options = q.type === 'yes_no' ? ['yes', 'no'] : q.options;
    s.append(el('option', null, ''));
    for (const o of options) {
      const opt = el('option', null, o);
      opt.value = o;
      s.append(opt);
    }
    s.value = q.default || '';
    box.append(s);
    read = () => s.value;
  } else {
    const input = el(q.type === 'long_text' ? 'textarea' : 'input');
    if (q.type === 'number') input.type = 'number';
    input.value = q.default || '';
    box.append(input);
    read = () => String(input.value || '').trim();
  }
  return { name: label, required: q.required, read, box };
}

export default async function mount({ root, api, el }) {
  const link = parseLink(location.hash);
  if (!link) {
    root.append(el('div', 'empty', 'Questions from the agent open from a link in its conversation.'));
    return;
  }
  const { session, ask: id } = link;
  const file = p => '/sessions/' + session + '/files/' + p.split('/').map(encodeURIComponent).join('/');
  const ask = await api(file('asks/' + id + '.json'));
  const { session: conversation } = await api('/sessions/' + session);

  root.append(el('div', 'ask-from', (conversation.title || 'A conversation') + ' asks:'));
  const message = el('p', 'ask-message', ask.message);
  message.style.whiteSpace = 'pre-wrap';
  root.append(message);
  root.append(el('p', 'ask-warning', 'What you enter here goes to the agent and its model. Do not enter passwords, keys, or card numbers.'));

  let fields = [], proposed = '', saved = [], files = [], held = '';
  // A page proposes on every change, and its last message can still be on its way when
  // Send is pressed. Messages from the box arrive in the order it sent them, so once it
  // answers this, everything it proposed before has arrived. A form has nothing to wait for.
  let caughtUp = async () => {};
  const proposal = el('p', 'ask-proposal');
  proposal.hidden = true;
  proposal.style.whiteSpace = 'pre-wrap';
  const savedLine = el('p', 'ask-saved');
  savedLine.hidden = true;
  const note = el('textarea', 'ask-note');
  note.setAttribute('aria-label', 'Anything to add');
  note.placeholder = 'Anything to add?';
  note.style.width = '100%';
  const problem = el('p', 'ask-problem');
  problem.hidden = true;
  const send = el('button', 'btn primary', 'Send');
  const decline = el('button', 'btn', 'Decline');
  send.type = decline.type = 'button';
  const buttons = el('div', 'ask-buttons');
  buttons.style.display = 'flex';
  buttons.style.gap = '8px';
  buttons.append(send, decline);
  const answering = [proposal, savedLine, note, problem, buttons];

  const store = async (name, content) => {
    if (content.length > MAX_SAVE) throw new Error('too large to save');
    const res = await api('/tools/ask_operator/call?session_id=' + session, {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ ask: id, save: name, content })
    });
    if (!res.ok) throw new Error(res.error);
    if (!saved.includes(res.content)) saved.push(res.content);
    savedLine.textContent = 'Saved: ' + saved.join(', ');
    savedLine.hidden = false;
    return res.content;
  };

  if (ask.questions) {
    fields = ask.questions.map(q => field(el, q));
    const form = el('div', 'ask-form');
    form.style.display = 'grid';
    form.style.gap = '12px';
    form.append(...fields.map(f => f.box));
    root.append(form, ...answering);
  } else {
    const html = await fetch(file(ask.page)).then(r => r.text());
    const frame = el('iframe', 'ask-frame');
    frame.setAttribute('sandbox', 'allow-scripts');
    frame.setAttribute('title', ask.message);
    frame.style.width = '100%';
    frame.style.height = '70vh';
    frame.style.border = '1px solid var(--line, #888)';
    let flushes = 0, flushing = null;
    caughtUp = () => new Promise(done => {
      flushing = { id: ++flushes, done };
      frame.contentWindow.postMessage({ ask: 'flush', id: flushing.id }, '*');
      setTimeout(done, CATCH_UP);
    });
    const receive = async e => {
      if (e.source !== frame.contentWindow || !e.data) return;
      if (e.data.ask === 'flushed') {
        if (flushing && e.data.id === flushing.id) flushing.done();
      } else if (e.data.ask === 'answer') {
        proposed = String(e.data.text).slice(0, MAX_ANSWER);
        files = Object.entries(e.data.files || {}).map(([name, content]) => [String(name), String(content)]);
        held = '';
        problem.hidden = true;
        proposal.textContent = 'The page will send: ' + proposed +
          (files.length ? '\nAnd save: ' + files.map(([name]) => name).join(', ') : '');
        proposal.hidden = false;
      } else if (e.data.ask === 'hold') {
        held = String(e.data.text).slice(0, MAX_ANSWER);
        proposal.hidden = true;
        problem.textContent = held;
        problem.hidden = false;
      } else if (e.data.ask === 'save') {
        const reply = { id: e.data.id };
        try {
          reply.saved = await store(String(e.data.name), String(e.data.content));
        } catch (err) { reply.error = err.message; }
        frame.contentWindow.postMessage(reply, '*');
      }
    };
    window.addEventListener('message', receive);
    const leave = () => {
      window.removeEventListener('message', receive);
      window.removeEventListener('hashchange', leave);
    };
    window.addEventListener('hashchange', leave);
    frame.srcdoc = frameDoc(html);
    // A page scrolls in its own box, and the box fits the screen, so this keeps Send in sight.
    note.rows = 1;
    root.append(...answering, frame);
  }

  const say = async compose => {
    send.disabled = decline.disabled = true;
    try {
      const text = await compose();
      await api('/sessions/' + session + '/messages', {
        method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ text })
      });
      location.hash = '#session/' + session;
    } catch (err) {
      send.disabled = decline.disabled = false;
      problem.textContent = 'That did not send: ' + err.message;
      problem.hidden = false;
    }
  };
  send.onclick = async () => {
    const missing = fields.filter(f => f.required && !f.read()).map(f => f.name);
    if (missing.length) {
      problem.textContent = 'Still needed: ' + missing.join(', ');
      problem.hidden = false;
      return;
    }
    send.disabled = true;
    await caughtUp();
    send.disabled = false;
    if (held) return;
    // The files go first, so an answer naming one finds it there.
    await say(async () => {
      for (const [name, content] of files) await store(name, content);
      return answerText(ask, 'accept', {
        values: fields.map(f => [f.name, f.read()]).filter(([, v]) => v), proposed, saved,
        note: String(note.value || '').trim()
      });
    });
  };
  decline.onclick = () => say(() => answerText(ask, 'decline', { note: String(note.value || '').trim() }));
}
