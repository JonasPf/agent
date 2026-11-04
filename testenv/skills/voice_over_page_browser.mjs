// The review page in real Chrome, in the real panel.js box, driven with real mouse and touch input.
import { spawn, execFileSync } from 'node:child_process';
import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';

const ROOT = path.resolve(import.meta.dirname, '..', '..');
const SK = ROOT + '/skills/voice-over/scripts';
const W = process.env.REVIEW_SHOTS || fs.mkdtempSync(path.join(os.tmpdir(), 'voice-over-browser-'));
const PANEL = ROOT + '/tools/ask_operator/ui/panel.js';
const CHROME = ['/usr/bin/chromium', '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'].find(p => fs.existsSync(p));
const sleep = ms => new Promise(r => setTimeout(r, ms));

const problems = [];
function report(label, good, detail) {
  console.log((good ? 'ok  ' : 'BAD ') + label + (good ? '' : '  ' + JSON.stringify(detail ?? '').slice(0, 400)));
  if (!good) problems.push(label);
}

// ---- what the agent would have built
fs.rmSync(W, { recursive: true, force: true });
fs.mkdirSync(W + '/spoken', { recursive: true });
fs.mkdirSync(W + '/silent', { recursive: true });
const ff = (...a) => execFileSync('ffmpeg', ['-y', '-hide_banner', '-loglevel', 'error', ...a], { stdio: 'pipe' });
ff('-f', 'lavfi', '-i', 'testsrc2=s=1832x1522:r=5:d=205', '-pix_fmt', 'yuv420p',
  '-c:v', 'libx264', '-preset', 'ultrafast', '-g', '100', W + '/demo.mov');
const SAMPLE = [
  [3, 'This short demo shows how an expense report moves from submitted to paid.'],
  [12.5, 'Here is the list of open reports. Each one shows who sent it and how much it is for.'],
  [34, "I'll open the report from the Berlin trip."],
  [44, 'The totals are added up for me, and each receipt is attached to its line.'],
  [63, 'When I submit it, the policy check runs straight away.'],
  [78.5, 'It flags one item: the hotel is over the nightly limit.'],
  [122, 'As the manager, I can see the flag and the reason for it.'],
  [135, "I'll approve it with a short note, and that note stays on the report so anyone can read it later."],
  [178, 'And here it is, marked as paid.'],
  [190, 'A tag </textarea><img src=x onerror="document.title=\'pwned\'"> stays words.'],
];
fs.writeFileSync(W + '/spoken/chunks.json', JSON.stringify(SAMPLE.map(([at, text]) => ({ at, text }))));
execFileSync('python3', [SK + '/make_review.py', '../demo.mov'], { cwd: W + '/spoken', stdio: 'pipe' });
execFileSync('python3', [SK + '/make_review.py', '../demo.mov'], { cwd: W + '/silent', stdio: 'pipe' });
// the same lines once spoken: a tone as long as each, with the silence a voice leaves at its ends
const LENGTHS = [4.0, 7.0, 4.5, 6.0, 5.0, 6.5, 5.5, 9.0, 4.0, 5.0];
fs.mkdirSync(W + '/heard/chunks', { recursive: true });
fs.writeFileSync(W + '/heard/chunks.json', JSON.stringify(SAMPLE.map(([at, text]) => ({ at, text }))));
fs.writeFileSync(W + '/heard/chunks/voice.json', JSON.stringify({ model: 'heygen/heygen-voice-1-turbo', voice: 'gareth' }));
LENGTHS.forEach((s, i) => ff('-f', 'lavfi', '-i', 'anullsrc=r=24000:cl=mono:d=0.4', '-f', 'lavfi', '-i', `sine=f=220:r=24000:d=${s}`,
  '-f', 'lavfi', '-i', 'anullsrc=r=24000:cl=mono:d=0.6', '-filter_complex', 'concat=n=3:v=0:a=1', `${W}/heard/chunks/${String(i + 1).padStart(2, '0')}.wav`));
execFileSync('python3', [SK + '/make_review.py', '../demo.mov'], { cwd: W + '/heard', stdio: 'pipe' });

// a browser that cannot size a box to its words, such as Safari before version 26
if (process.env.NO_FIELD_SIZING) for (const f of ['spoken', 'silent', 'heard']) {
  const page = W + '/' + f + '/review/review.html';
  const html = fs.readFileSync(page, 'utf8');
  fs.writeFileSync(page, html.replace(' field-sizing: content;', '').replace("CSS.supports('field-sizing', 'content')", 'false'));
}

// ---- a server shaped like the agent's API, with the real panel
let folder = W + '/spoken';
const saves = [], messages = [];
const HOST = `<!doctype html><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1"><title>host</title>
<link rel=stylesheet href=/style.css><main id=root></main>
<script type=module>
const el = (tag, cls, text) => { const n = document.createElement(tag); if (cls) n.className = cls; if (text != null) n.textContent = text; return n; };
const api = async (path, opts) => {
  const r = await fetch(path, opts);
  if (r.status === 204) return null;
  const body = await r.text();
  let json = null; try { json = body ? JSON.parse(body) : null; } catch (e) {}
  if (!r.ok) throw new Error((json && json.error) || body || r.status);
  return json;
};
const mod = await import('/panel.js');
await mod.default({ root: document.getElementById('root'), api, el });
window.mounted = true;
</script>`;
const server = http.createServer((req, res) => {
  let body = '';
  req.on('data', d => body += d);
  req.on('end', () => {
    const url = req.url.split('?')[0];
    const send = (type, text) => { res.writeHead(200, { 'content-type': type }); res.end(text); };
    if (url === '/') return send('text/html', HOST);
    if (url === '/panel.js') return send('text/javascript', fs.readFileSync(PANEL));
    if (url === '/style.css') return send('text/css', fs.readFileSync(ROOT + '/web/style.css'));
    if (url === '/sessions/s1') return send('application/json', JSON.stringify({ session: { title: 'Expense demo' } }));
    if (url === '/sessions/s1/files/asks/a1.json') return send('application/json', JSON.stringify({ message: 'Check the script before I buy the speech.', page: 'review/review.html' }));
    if (url === '/sessions/s1/files/review/review.html') return send('text/html', fs.readFileSync(folder + '/review/review.html'));
    if (url === '/tools/ask_operator/call') {
      const a = JSON.parse(body);
      saves.push(a);
      fs.writeFileSync(folder + '/review/' + a.save, a.content);
      return send('application/json', JSON.stringify({ ok: true, content: 'review/' + a.save }));
    }
    if (url === '/sessions/s1/messages') { messages.push(JSON.parse(body).text); return send('application/json', '{}'); }
    res.writeHead(404); res.end('no ' + url);
  });
});
await new Promise(r => server.listen(0, '127.0.0.1', r));
const ORIGIN = 'http://127.0.0.1:' + server.address().port;

// ---- Chrome, spoken to directly
const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'review-chrome-'));
// As root, in the test container, chromium refuses to start with its own sandbox on.
const chrome = spawn(CHROME, ['--headless=new', '--remote-debugging-port=0', '--user-data-dir=' + profile, '--no-first-run',
  '--no-default-browser-check', '--disable-gpu', ...(process.getuid() === 0 ? ['--no-sandbox'] : []), 'about:blank'], { stdio: 'ignore' });
let port = null;
for (let i = 0; i < 100 && !port; i++) {
  await sleep(100);
  try { port = fs.readFileSync(profile + '/DevToolsActivePort', 'utf8').split('\n'); } catch {}
}
const ws = new WebSocket(`ws://127.0.0.1:${port[0]}${port[1]}`);
await new Promise(r => ws.addEventListener('open', r));
let nextId = 0;
const waiting = new Map(), children = [], contexts = [], noise = [];
ws.addEventListener('message', m => {
  const d = JSON.parse(m.data);
  if (d.id && waiting.has(d.id)) {
    const [ok, no] = waiting.get(d.id);
    waiting.delete(d.id);
    return d.error ? no(new Error(d.error.message)) : ok(d.result);
  }
  if (d.method === 'Target.attachedToTarget' && d.params.targetInfo.type === 'iframe') {
    children.push(d.params.sessionId);
    send('Runtime.enable', {}, d.params.sessionId);
    send('Log.enable', {}, d.params.sessionId);
  }
  if (d.method === 'Runtime.executionContextCreated') contexts.push(d.params.context);
  if (d.method === 'Runtime.exceptionThrown') noise.push(d.params.exceptionDetails.exception?.description || d.params.exceptionDetails.text);
  if (d.method === 'Log.entryAdded' && ['error', 'warning'].includes(d.params.entry.level)) noise.push(d.params.entry.text);
});
function send(method, params = {}, sessionId) {
  const id = ++nextId;
  ws.send(JSON.stringify({ id, method, params, sessionId }));
  return new Promise((ok, no) => waiting.set(id, [ok, no]));
}
const { targetId } = await send('Target.createTarget', { url: 'about:blank' });
const { sessionId: page } = await send('Target.attachToTarget', { targetId, flatten: true });
await send('Page.enable', {}, page);
await send('Runtime.enable', {}, page);
await send('Log.enable', {}, page);
await send('Target.setAutoAttach', { autoAttach: true, waitForDebuggerOnStart: false, flatten: true }, page);

const top = async expr => {
  const r = await send('Runtime.evaluate', { expression: expr, returnByValue: true, awaitPromise: true }, page);
  if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text);
  return r.result.value;
};
async function frameTarget() {
  const tree = await send('Page.getFrameTree', {}, page);
  const child = tree.frameTree.childFrames?.[0]?.frame.id;
  const ctx = [...contexts].reverse().find(c => c.auxData?.frameId === child && c.auxData?.isDefault);
  return ctx ? { contextId: ctx.id, session: page } : { session: children[children.length - 1] };
}
const inFrame = async expr => {
  const t = await frameTarget();
  const params = { expression: expr, returnByValue: true, awaitPromise: true };
  if (t.contextId) params.contextId = t.contextId;
  const r = await send('Runtime.evaluate', params, t.session);
  if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text);
  return r.result.value;
};
const until = async (fn, what) => {
  for (let i = 0; i < 100; i++) {
    try { if (await fn()) return true; } catch {}
    await sleep(50);
  }
  throw new Error('timed out: ' + what);
};

// a point in the top page for what a selector finds in the box, after bringing it into view
async function point(selector, at = 0.5) {
  await inFrame(`document.querySelectorAll(${JSON.stringify(selector)})[0].scrollIntoView({block:'center'})`);
  await sleep(40);
  const box = await top(`(() => { const b = document.querySelector('iframe').getBoundingClientRect(); return {x:b.x,y:b.y}; })()`);
  const r = await inFrame(`(() => { const b = document.querySelectorAll(${JSON.stringify(selector)})[0].getBoundingClientRect(); return {x:b.x,y:b.y,w:b.width,h:b.height}; })()`);
  return { x: box.x + r.x + r.w * at, y: box.y + r.y + r.h / 2 };
}
const mouse = (type, x, y, extra = {}) => send('Input.dispatchMouseEvent', { type, x, y, button: 'left', buttons: type === 'mouseReleased' ? 0 : 1, clickCount: 1, ...extra }, page);
async function click(selector) {
  const p = await point(selector);
  await send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: p.x, y: p.y }, page);
  await mouse('mousePressed', p.x, p.y);
  await mouse('mouseReleased', p.x, p.y);
  await sleep(30);
}
async function replaceText(selector, text) {
  await click(selector);
  await inFrame(`(() => { const e = document.activeElement; e.select(); })()`);
  await send('Input.insertText', { text }, page);
}
const key = async name => {
  const code = { Tab: 9, Enter: 13, ArrowUp: 38, ArrowDown: 40 }[name];
  await send('Input.dispatchKeyEvent', { type: 'keyDown', key: name, code: name, windowsVirtualKeyCode: code }, page);
  await send('Input.dispatchKeyEvent', { type: 'keyUp', key: name, code: name, windowsVirtualKeyCode: code }, page);
  await sleep(30);
};
const shot = async name => {
  const { data } = await send('Page.captureScreenshot', { format: 'png' }, page);
  fs.writeFileSync(W + '/' + name + '.png', Buffer.from(data, 'base64'));
};
const lines = () => inFrame(`[...document.querySelectorAll('.line')].map(c => ({ time: c.querySelector('.time').textContent, words: c.querySelector('.words').value, warn: c.querySelector('.warn').textContent }))`);
const moving = () => inFrame(`document.querySelector('.line.moving .time')?.textContent`);
// pairs of lines where one hides part of the other
const covered = () => inFrame(`(() => { const r = [...document.querySelectorAll('.line')].map(c => c.getBoundingClientRect()); let n = 0; r.forEach((a, i) => r.slice(i + 1).forEach(b => { if (Math.min(a.right, b.right) - Math.max(a.left, b.left) > 1 && Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top) > 1) n++; })); return n; })()`);
const pick = value => inFrame(`(() => { const s = document.getElementById('voice'); s.value = ${JSON.stringify(value)}; s.dispatchEvent(new Event('change', {bubbles:true})); })()`);
async function drag(selector, by, hold = 0) {
  const from = await point(selector);
  const bottom = await top(`document.querySelector('iframe').getBoundingClientRect().bottom - 5`);
  const y = Math.min(from.y + by, bottom);
  await send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: from.x, y: from.y }, page);
  await mouse('mousePressed', from.x, from.y);
  await mouse('mouseMoved', from.x, y);
  await sleep(40 + hold);
  await mouse('mouseReleased', from.x, y);
  await sleep(40);
}
const status = () => inFrame(`[...document.querySelectorAll('.stat')].map(s => s.textContent)`);
const clipped = () => inFrame(`[...document.querySelectorAll('.words')].filter(t => t.scrollHeight > t.clientHeight + 2).length`);
const seconds = s => { const [m, r] = s.split(':'); return Number(m) * 60 + Number(r); };
const pressing = label => top(`[...document.querySelectorAll('button')].find(b => b.textContent === ${JSON.stringify(label)}).click()`);
const proposal = () => top(`document.querySelector('.ask-proposal').textContent`);
const guesses = () => inFrame(`document.querySelectorAll('.line.guess').length`);
const off = id => inFrame(`document.getElementById(${JSON.stringify(id)}).disabled`);
const sounding = () => inFrame(`(() => { const a = document.getElementById('sample'); return !a.error && !a.paused && a.currentTime > 0.05; })()`);
const toSpeak = out => [...out.matchAll(/^(chunks\/\d+\.mp3)  (.*)$/gm)].map(m => [m[1], m[2]]);
const refused = cwd => { try { execFileSync('python3', [SK + '/make_review.py', '--apply'], { cwd, stdio: 'pipe' }); return ''; } catch (e) { return String(e.stderr); } };
// where Send stands against the top of the box and the top of what scrolls
const sendPlace = () => top(`(() => { const s = [...document.querySelectorAll('button')].find(b => b.textContent === 'Send').getBoundingClientRect(), f = document.querySelector('iframe').getBoundingClientRect(), m = document.querySelector('main'); return { bottom: s.bottom, top: s.top, box: f.top, main: m.getBoundingClientRect().top, scrolled: m.scrollTop, room: m.scrollHeight - m.clientHeight }; })()`);
async function open(which) {
  folder = W + '/' + which;
  contexts.length = 0;
  await send('Page.navigate', { url: 'about:blank' }, page);
  await sleep(50);
  await send('Page.navigate', { url: ORIGIN + '/#toolpanel/ask_operator/s1/a1' }, page);
  await until(() => top('window.mounted === true'), 'panel mounted');
  await until(() => inFrame(`document.querySelectorAll('.slot').length > 0`), 'page drawn');
}
async function viewport(width, height, mobile = false) {
  await send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: mobile ? 2 : 1, mobile }, page);
  await send('Emulation.setTouchEmulationEnabled', { enabled: mobile }, page);
}

try {
  // ---- desktop, a video with narration
  await viewport(1280, 1200);
  await open('spoken');
  report('the panel shows who asks and what', (await top('document.querySelector(".ask-from").textContent')).includes('Expense demo') &&
    (await top('document.querySelector(".ask-message").textContent')).includes('buy the speech'));
  report('the page runs in the box', (await top('document.querySelector("iframe").getAttribute("sandbox")')) === 'allow-scripts');
  report('every screenshot loads under the policy', await inFrame(`[...document.images].length === 7 && [...document.images].every(i => i.complete && i.naturalWidth >= 700)`),
    await inFrame(`[...document.images].map(i => i.naturalWidth)`));
  let all = await lines();
  const scale = await inFrame(`document.querySelector('.slot[data-t="30"]').getBoundingClientRect().top - document.querySelector('.slot[data-t="0"]').getBoundingClientRect().top`) / 30;
  const spots = await inFrame(`[...document.querySelectorAll('.line')].map(c => c.getBoundingClientRect().top - document.querySelector('.slot').getBoundingClientRect().top)`);
  report('each line sits on the timeline at its moment', all.length === 10 && all.every((l, i) => Math.abs(spots[i] - seconds(l.time) * scale) < 1.5), { spots, scale });
  const tall = await inFrame(`[...document.querySelectorAll('.line')].map(c => c.getBoundingClientRect().height)`);
  const saying = i => all[i].words.split(/\s+/).length / 2.3 * scale;
  report('a line is as tall as it takes to say', Math.abs(tall[7] - saying(7)) < 2 && tall[7] > tall[2], { tall, want: saying(7) });
  report('and stands beside the screenshot of its moment', await inFrame(`(() => { const b = document.querySelector('.slot[data-t="120"]').getBoundingClientRect(), c = document.querySelectorAll('.line')[6].getBoundingClientRect(); return c.top >= b.top && c.top < b.bottom && b.right <= c.left; })()`));
  report('no line hides another', (await covered()) === 0, await covered());
  const first = await status();
  report('the status line counts lines and cost', first[0].startsWith('10 lines') && first[1].includes('$0.00'), first);
  report('a tag in a line stays words', all[9].words.includes('<img src=x') && (await inFrame(`document.querySelectorAll('.line img').length`)) === 0 &&
    (await inFrame(`document.images.length`)) === 7 && (await top('document.title')) === 'host', all[9]);
  await shot('desktop');
  report('no line is cut off', (await clipped()) === 0, await clipped());
  await viewport(560, 1200);
  await sleep(150);
  report('no line is cut off after the window narrows', (await clipped()) === 0, await clipped());
  // squeezed cards wrap taller and crowd each other: that must not outlast the squeeze
  await viewport(200, 1200);
  await sleep(150);
  await viewport(1280, 1200);
  await sleep(150);
  const shares = await inFrame(`[...document.querySelectorAll('.line')].map(c => c.getBoundingClientRect().width / document.getElementById('track').getBoundingClientRect().width)`);
  report('lines spread out again when the window widens', shares.every(w => w > 0.99), shares);

  // drag the second line down the timeline, with a real mouse, letting the page scroll
  const from = await point('.line:nth-of-type(2) .grip');
  await send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: from.x, y: from.y }, page);
  await mouse('mousePressed', from.x, from.y);
  await sleep(20);
  await mouse('mouseMoved', from.x, from.y + 40);
  await sleep(40);
  report('a line moves with the pointer and says where it is', (await moving()) === '0:15.0', await moving());
  const edge = await top(`(() => { const b = document.querySelector('iframe').getBoundingClientRect(); return b.bottom - 30; })()`);
  await mouse('mouseMoved', from.x, edge);
  await sleep(100);
  const early = await moving();
  await sleep(300);
  report('held at the window edge, the page keeps going', seconds(await moving()) > seconds(early), [early, await moving()]);
  await mouse('mouseMoved', from.x, edge - 300);
  await sleep(120);
  const hovering = await moving();
  await mouse('mouseReleased', from.x, edge - 300);
  await sleep(50);
  all = await lines();
  const moved = all.find(l => l.words.startsWith('Here is the list'));
  report('the dragged line lands where the drag said', moved.time === hovering && seconds(moved.time) > 15, { moved, hovering });
  report('nothing stays marked as moving', !(await inFrame(`!!document.querySelector('.line.moving')`)));
  report('lines stay in time order', all.every((l, i) => !i || seconds(l.time) >= seconds(all[i - 1].time)), all);
  report('every screenshot is still loaded after a move', await inFrame(`[...document.images].every(i => i.naturalWidth > 0)`));
  await click('#undo');
  all = await lines();
  report('Undo puts it back', all.find(l => l.words.startsWith('Here is the list')).time === '0:12.5', all[1]);

  // the ends of the video
  await drag('.line:nth-of-type(1) .grip', -200);
  report('a line stops at the start of the video', (await lines())[0].time === '0:00.0', (await lines())[0]);
  await click('#undo');
  await drag('.line:nth-of-type(10) .grip', 400, 900);
  report('a line stops at the end of the video', (await lines())[9].time === '3:24.5', (await lines())[9]);
  await click('#undo');

  // a line dragged onto another
  await drag('.line:nth-of-type(2) .grip', -7.5 * scale);
  all = await lines();
  report('the line it lands on says it will be cut off', all[0].warn.includes('cuts off'), all[0]);
  report('the status line counts it', (await status())[2].includes('cut off'), await status());
  report('both stay in full view, side by side', (await covered()) === 0, await covered());
  await shot('overlap');
  await click('#undo');
  report('and the warning goes when they part', !(await lines())[0].warn, (await lines())[0]);

  // the keyboard
  const before = (await lines())[0].time;
  await click('.line:nth-of-type(1) .grip');
  await key('ArrowDown');
  report('the down arrow moves a line later', seconds((await lines())[0].time) === seconds(before) + 0.5, [(await lines())[0].time, before]);
  report('focus stays on the handle', await inFrame(`document.activeElement.classList.contains('grip')`));
  await key('ArrowUp');
  report('the up arrow moves it back', (await lines())[0].time === before, (await lines())[0]);
  await click('#undo'); await click('#undo');

  // words
  const costBefore = (await status())[1];
  await replaceText('.line:nth-of-type(1) .words', 'A much shorter opening.');
  all = await lines();
  report('words can be changed', all[0].words === 'A much shorter opening.', all[0]);
  report('the count follows the words', (await status())[0] !== first[0], await status());
  await click('.line:nth-of-type(2) .words');
  await send('Input.insertText', { text: ' Added.' }, page);
  await click('#undo');
  all = await lines();
  report('Undo takes back one edit, not every letter', !all[1].words.includes('Added') && all[0].words === 'A much shorter opening.', all[1]);
  await click('#undo');
  report('and the edit before it', (await lines())[0].words.startsWith('This short demo'));

  // remove, add
  const count = (await lines()).length;
  await click('.line:nth-of-type(1) .del');
  report('Remove takes the line out', (await lines()).length === count - 1);
  await click('#undo');
  await click('.slot[data-t="60"] .add');
  report('Add puts the cursor in a new line', await inFrame(`document.activeElement.classList.contains('words')`));
  await send('Input.insertText', { text: 'A new line.' }, page);
  all = await lines();
  const added = all.find(l => l.words === 'A new line.');
  report('a new line lands clear of what is there', added && seconds(added.time) >= 60, all);
  await click('#undo'); await click('#undo');
  report('Undo takes a new line out again', !(await lines()).some(l => l.words === 'A new line.' || l.words === ''), await lines());
  await click('.slot[data-t="60"] .add');
  const withEmpty = await status();
  report('an empty line is left out of the count', withEmpty[0].startsWith('10 lines') && (await lines()).length === 11, [withEmpty, (await lines()).length]);
  await click('#undo');
  report('and is gone again', (await lines()).length === 10, await lines());

  // warnings
  await replaceText('.line:nth-of-type(1) .words', Array(30).fill('word').join(' '));
  report('a line that runs into the next says it will be cut off', (await lines())[0].warn.includes('cuts off'), (await lines())[0]);
  report('the status line says so too', (await status())[2].includes('cut off'), await status());
  await shot('warning');
  await click('#undo');
  report('the warning goes with the words', !(await lines())[0].warn, (await lines())[0]);
  await replaceText('.line:nth-of-type(10) .words', Array(60).fill('word').join(' '));
  report('a last line that runs past the end says so', (await lines())[9].warn.includes('past the end'), (await lines())[9]);
  report('and is called too long', (await status())[2].includes('too long'), await status());
  await click('#undo');

  // voice and language
  const cheap = (await status())[1];
  report('no sample plays until a voice is picked', await inFrame(`(() => { const a = document.getElementById('sample'); return a.paused && !a.currentSrc; })()`));
  await pick('1|brian');
  const playing = () => inFrame(`(() => { const a = document.getElementById('sample'); return !a.error && !a.paused && a.currentTime > 0.05; })()`);
  await until(playing, 'the sample of the voice picked playing');
  const heard = v => inFrame(`document.getElementById('sample').currentSrc.endsWith(JSON.parse(document.getElementById('data').textContent).samples['elevenlabs/eleven-flash-v2.5'][${JSON.stringify(v)}])`);
  report('picking a voice plays its sample', await heard('brian'));
  report('a sample is a few seconds long', await inFrame(`(() => { const d = document.getElementById('sample').duration; return d > 0.8 && d < 6; })()`), await inFrame(`document.getElementById('sample').duration`));
  await pick('1|alice');
  await until(playing, 'the next sample playing');
  report('picking another plays that one instead', await heard('alice'));
  const dear = (await status())[1];
  report('the cost follows the voice', Number(dear.match(/\$([\d.]+)/)[1]) > 5 * Number(cheap.match(/\$([\d.]+)/)[1]), [cheap, dear]);
  report('all voices of both models are offered', (await inFrame(`document.querySelectorAll('#voice option').length`)) === 31 &&
    (await inFrame(`document.querySelectorAll('#voice optgroup').length`)) === 2);
  await inFrame(`(() => { const s = document.getElementById('lang'); s.value = 'de'; s.dispatchEvent(new Event('change', {bubbles:true})); })()`);
  report('a new language says what happens next', (await inFrame(`document.getElementById('langNote').textContent`)).includes('German'));
  await inFrame(`(() => { const s = document.getElementById('lang'); s.value = ''; s.dispatchEvent(new Event('change', {bubbles:true})); })()`);
  report('and stops saying it', await inFrame(`document.getElementById('langNote').hidden`));

  // the zoom
  await click('.slot[data-t="30"] .zoom');
  report('tapping a screenshot shows it larger', await inFrame(`document.querySelector('.slot[data-t="30"]').classList.contains('big')`));
  const wide = await inFrame(`document.querySelector('.slot[data-t="30"] img').getBoundingClientRect().width`);
  report('larger means most of the width', wide > 900, wide);
  await shot('zoomed');
  await click('.slot[data-t="30"] .zoom');

  // Send, the one approval
  await until(async () => (await proposal()).includes('10 lines'), 'proposal');
  report('the panel says what Send will do', (await proposal()).includes('Approved: 10 lines') && (await proposal()).includes('alice') && (await proposal()).includes('And save: approved.json'), await proposal());
  report('no Approve on the page', !(await inFrame(`[...document.querySelectorAll('button')].some(b => /approve/i.test(b.textContent))`)));
  report('nothing is saved before Send', saves.length === 0 && !fs.existsSync(W + '/spoken/review/approved.json'), saves.map(s => s.save));
  report('so nothing can be applied before Send', refused(W + '/spoken').includes('not approved'), refused(W + '/spoken'));
  const placed = await sendPlace();
  report('Send stands directly above the page', placed.bottom <= placed.box && placed.box - placed.bottom < 30, placed);
  await shot('send');

  await pick('0|gareth');
  await until(async () => (await proposal()).includes('gareth'), 'proposal follows the voice');
  await pick('1|alice');
  await inFrame(`(() => { const s = document.getElementById('lang'); s.value = 'de'; s.dispatchEvent(new Event('change', {bubbles:true})); })()`);
  await until(async () => (await proposal()).includes('German'), 'proposal follows the language');
  report('the proposal says a translation comes first', (await proposal()).includes('translated into German'), await proposal());
  await inFrame(`(() => { const s = document.getElementById('lang'); s.value = ''; s.dispatchEvent(new Event('change', {bubbles:true})); })()`);
  await drag('.line:nth-of-type(1) .grip', 3 * scale);
  await until(async () => seconds((await lines())[0].time) > 3, 'line moved');
  await click('#undo');

  // Send pressed in the same breath as the last change
  await replaceText('.line:nth-of-type(1) .words', 'Changed my mind.');
  await pressing('Send');
  await until(() => messages.length === 1, 'send');
  report('Send saves one file, then speaks', saves.length === 1 && saves[0].save === 'approved.json' && saves[0].ask === 'a1', saves.map(s => s.save));
  const firstSave = JSON.parse(saves[0].content);
  report('the file holds model, voice, language and every line', firstSave.model === 'elevenlabs/eleven-flash-v2.5' && firstSave.voice === 'alice' && firstSave.language === '' && firstSave.chunks.length === 10, firstSave);
  report('Send carries the last change, however quick', firstSave.chunks[0].text === 'Changed my mind.', firstSave.chunks[0]);
  report('Send reaches the conversation naming the file', messages[0].includes('Answered: Check the script') && messages[0].includes('Saved: review/approved.json') && messages[0].includes('Approved: 10 lines'), messages[0]);
  const applied = execFileSync('python3', [SK + '/make_review.py', '--apply'], { cwd: W + '/spoken', encoding: 'utf8' });
  const final = JSON.parse(fs.readFileSync(W + '/spoken/chunks.json', 'utf8'));
  report('the script takes what Send saved', applied.includes('alice') && final[0].text === 'Changed my mind.' && final.length === 10, applied);
  report('at every line a time', final.every(c => typeof c.at === 'number'), final[0]);

  // Decline
  fs.rmSync(W + '/spoken/review/approved.json');
  saves.length = 0; messages.length = 0;
  await open('spoken');
  await until(async () => (await proposal()).includes('And save'), 'proposal');
  await pressing('Decline');
  await until(() => messages.length === 1, 'decline');
  report('Decline saves nothing', saves.length === 0 && !fs.existsSync(W + '/spoken/review/approved.json') && messages[0].startsWith('Declined:'), messages[0]);

  // a line too long to speak
  saves.length = 0; messages.length = 0;
  await open('spoken');
  await replaceText('.line:nth-of-type(1) .words', 'x'.repeat(5001));
  await until(() => top(`document.querySelector('.ask-problem').textContent.includes('too long')`), 'the hold');
  await pressing('Send');
  await sleep(100);
  report('Send will not take a line too long to speak', saves.length === 0 && messages.length === 0, messages);
  await click('#undo');
  await until(() => top(`document.querySelector('.ask-problem').hidden`), 'the hold lifted');

  // ---- the same lines, spoken: each as long as its speech, and heard
  saves.length = 0; messages.length = 0;
  await open('heard');
  const pace = await inFrame(`(() => { let c = 0, s = 0; JSON.parse(document.getElementById('data').textContent).lines.forEach(l => { c += l.text.trim().split(/\\s+/).join(' ').length; s += l.length; }); return c / s; })()`);
  const heights = await inFrame(`[...document.querySelectorAll('.line')].map(c => c.getBoundingClientRect().height)`);
  report('a spoken line is as tall as its speech, not as its words', heights.length === 10 && heights.every((h, i) => Math.abs(h - LENGTHS[i] * scale) < 2),
    { heights, want: LENGTHS.map(s => s * scale) });
  report('no spoken line is a guess, and nothing is left to speak', (await guesses()) === 0 && (await status())[1] === 'all spoken', await status());
  await shot('heard');
  report('a spoken line shows ▶ on its handle', (await inFrame(`[...document.querySelectorAll('.cue')].filter(c => getComputedStyle(c).display !== 'none').length`)) === 10);
  await click('.line:nth-of-type(2) .grip');
  await until(sounding, "a line's speech playing");
  report("a tap on the handle plays that line's own speech", await inFrame(`document.getElementById('sample').currentSrc.endsWith(JSON.parse(document.getElementById('data').textContent).lines[1].audio)`));
  report('and offers to stop it', (await inFrame(`document.querySelector('.line:nth-of-type(2) .grip').getAttribute('aria-label')`)).includes('stop it') &&
    (await inFrame(`document.querySelector('.line:nth-of-type(2) .cue').textContent`)) === '■');
  await click('.line:nth-of-type(2) .grip');
  report('tapped again, it stops', await inFrame(`document.getElementById('sample').paused`));
  await key('Enter');
  await sleep(200);
  report('Enter on the handle plays it too', await sounding());
  await key('Enter');
  const before2 = (await lines())[1].time;
  await drag('.line:nth-of-type(2) .grip', 3 * scale);
  report('a drag moves the line and plays nothing', (await lines())[1].time !== before2 && (await inFrame(`document.getElementById('sample').paused`)), (await lines())[1]);
  await click('#undo');

  const shorter = 'Here is the list of open reports, each with who sent it.';
  await replaceText('.line:nth-of-type(2) .words', shorter);
  const changed = await inFrame(`(() => { const c = document.querySelectorAll('.line')[1]; return { guess: c.classList.contains('guess'), cue: getComputedStyle(c.querySelector('.cue')).display, h: c.getBoundingClientRect().height }; })()`);
  report('a line whose words changed is a guess until it is spoken, with nothing to play', changed.guess && changed.cue === 'none', changed);
  report('as long as its words take at the pace of the lines spoken', Math.abs(changed.h - shorter.length / pace * scale) < 2, { changed, want: shorter.length / pace * scale });
  report('the status line counts it to speak', (await status())[1].startsWith('1 to speak'), await status());
  await until(async () => (await proposal()).includes('1 to speak'), 'the proposal names what is to speak');
  await click('#undo');
  report('Undo makes it spoken again', (await guesses()) === 0 && (await status())[1] === 'all spoken', await status());
  await pick('0|meredith');
  report('another voice makes every line a guess', (await guesses()) === 10 && (await status())[1].startsWith('10 to speak'), await status());
  const tallNow = await inFrame(`document.querySelector('.line').getBoundingClientRect().height`);
  report('as long as its words take at the usual pace', Math.abs(tallNow - SAMPLE[0][1].split(/\s+/).length / 2.3 * scale) < 2, tallNow);
  await pick('0|gareth');
  report('the voice spoken makes them spoken again', (await guesses()) === 0);
  await inFrame(`(() => { const s = document.getElementById('lang'); s.value = 'de'; s.dispatchEvent(new Event('change', {bubbles:true})); })()`);
  report('so does a language, the other way', (await guesses()) === 10);
  await inFrame(`(() => { const s = document.getElementById('lang'); s.value = ''; s.dispatchEvent(new Event('change', {bubbles:true})); })()`);

  // join a line and the next
  report('Split and Join wait for a line to write in', (await off('split')) && (await off('join')));
  const opening = (await lines())[2].words;
  await click('.line:nth-of-type(3) .words');
  await until(async () => !(await off('join')), 'Join ready');
  await click('#join');
  all = await lines();
  report('Join makes one line of a line and the next', all.length === 9 && all[2].words === opening + ' ' + SAMPLE[3][1] && all[2].time === '0:34.0', all[2]);
  report('which is to be spoken', (await guesses()) === 1 && (await inFrame(`document.querySelectorAll('.line')[2].classList.contains('guess')`)));
  report('the cursor stands where they met', await inFrame(`document.activeElement === document.querySelectorAll('.line')[2].querySelector('.words') && document.activeElement.selectionStart === ${opening.length}`));
  await click('#undo');
  report('Undo parts them again, both spoken', (await lines()).length === 10 && (await guesses()) === 0);
  await click('.line:nth-of-type(10) .words');
  await inFrame(`(() => { const t = document.activeElement; t.setSelectionRange(t.value.length, t.value.length); })()`);
  await sleep(60);
  report('Split is off with the cursor at an end of the words, and Join on the last line', (await off('split')) && (await off('join')));
  await click('.line:nth-of-type(1) .grip');
  report('both are off once no line is being written in', (await off('split')) && (await off('join')));

  // split a line in two where the cursor is
  const whole = (await lines())[7].words, cut = whole.indexOf(' and that');
  await click('.line:nth-of-type(8) .words');
  await inFrame(`document.activeElement.setSelectionRange(${cut}, ${cut})`);
  await until(async () => !(await off('split')), 'Split ready');
  await click('#split');
  all = await lines();
  report('Split makes two lines of one, where the cursor was', all.length === 11 && all[7].words === whole.slice(0, cut) && all[8].words === whole.slice(cut + 1), all.slice(7, 9));
  const ends = 135 + whole.slice(0, cut).length / pace;
  report('the first keeps its start, and the second starts as the first ends, not cutting it off',
    all[7].time === '2:15.0' && seconds(all[8].time) >= ends && seconds(all[8].time) - ends < 0.1 && !all[7].warn, [all[7], all[8].time, ends]);
  report('both are to be spoken', (await guesses()) === 2 && (await status())[1].startsWith('2 to speak'), await status());
  report('the cursor carries on at the start of the second', await inFrame(`document.activeElement === document.querySelectorAll('.line')[8].querySelector('.words') && document.activeElement.selectionStart === 0`));
  await shot('split');
  await until(async () => (await proposal()).includes('11 lines'), 'proposal');
  report('the panel says how many lines are to be spoken', (await proposal()).includes('2 to speak'), await proposal());
  await pressing('Send');
  await until(() => messages.length === 1, 'send');
  const respoken = execFileSync('python3', [SK + '/make_review.py', '--apply'], { cwd: W + '/heard', encoding: 'utf8' });
  report('only the two halves are spoken again; every other line keeps its speech',
    JSON.stringify(toSpeak(respoken)) === JSON.stringify([['chunks/08.mp3', whole.slice(0, cut)], ['chunks/09.mp3', whole.slice(cut + 1)]]) && respoken.includes('9 keep'), respoken);

  // ---- a video with no words at all
  saves.length = 0; messages.length = 0;
  await open('silent');
  report('an empty page asks for the script', await inFrame(`!document.getElementById('paste').hidden`));
  await until(() => top(`document.querySelector('.ask-problem').textContent.includes('at least one line')`), 'the hold');
  await pressing('Send');
  await sleep(100);
  report('Send with nothing says so and does nothing', saves.length === 0 && messages.length === 0, messages);
  await shot('silent');
  await replaceText('#pasteText', 'Version 2.3 of the report opens here. Then the receipts follow!\nAnd the last line ends it.');
  await click('#pasteGo');
  all = await lines();
  report('each sentence becomes a line, and a version number stays whole', all.length === 3 && all[0].words === 'Version 2.3 of the report opens here.' && all[2].words === 'And the last line ends it.', all);
  report('they spread across the video', seconds(all[0].time) < 10 && seconds(all[2].time) > 120 && seconds(all[2].time) < 204, all);
  report('the paste box goes away', await inFrame(`document.getElementById('paste').hidden`));
  await shot('silent-filled');
  await until(async () => (await proposal()).includes('3 lines'), 'proposal');
  await pressing('Send');
  await until(() => messages.length === 1, 'send');
  const silentRun = execFileSync('python3', [SK + '/make_review.py', '--apply'], { cwd: W + '/silent', encoding: 'utf8' });
  report('a silent video reaches speech the same way', silentRun.includes('gareth'), silentRun);

  // ---- a phone, with a finger
  await viewport(390, 844, true);
  await open('spoken');
  await shot('phone');
  await top(`document.querySelector('main').scrollTop = 99999`);
  await sleep(40);
  const held = await sendPlace();
  report('Send stays in sight on a phone with the screen scrolled to the end', held.scrolled > 0 && held.top >= held.main - 1, held);
  await top(`document.querySelector('main').scrollTop = 0`);
  const overflow = await inFrame(`document.documentElement.scrollWidth - document.documentElement.clientWidth`);
  const sticking = () => inFrame(`[...new Set([...document.querySelectorAll('body *')].filter(e => e.getBoundingClientRect().right > document.documentElement.clientWidth + 1).map(e => e.className || e.tagName))]`);
  report('nothing runs off the side on a phone', overflow <= 0, { overflow, sticking: await sticking() });
  report('the screenshots stay beside the lines on a phone', await inFrame(`document.querySelector('.slot').getBoundingClientRect().right <= document.querySelector('.line').getBoundingClientRect().left`));
  report('no line hides another on a phone', (await covered()) === 0, await covered());
  report('no line is cut off on a phone', (await clipped()) === 0, await clipped());
  const bar = await inFrame(`document.querySelector('.status').offsetHeight`);
  report('the top bar stays small on a phone', bar <= 64, bar);
  const firstShot = await inFrame(`document.querySelector('.shot').getBoundingClientRect().top`);
  report('a screenshot shows without scrolling on a phone', firstShot < 300, firstShot);
  const grip = await point('.line:nth-of-type(3) .grip');
  const beforeTouch = (await lines())[2];
  const touch = (type, x, y) => send('Input.dispatchTouchEvent', { type, touchPoints: type === 'touchEnd' ? [] : [{ x, y }] }, page);
  await touch('touchStart', grip.x, grip.y);
  await sleep(20);
  for (let i = 1; i < 12; i++) await touch('touchMove', grip.x, grip.y + i * 12);
  await sleep(120);
  const hoverPhone = await moving();
  await touch('touchEnd');
  await sleep(50);
  const afterTouch = (await lines()).find(l => l.words === beforeTouch.words);
  report('a finger can drag a line', afterTouch.time === hoverPhone && afterTouch.time !== beforeTouch.time, { beforeTouch, afterTouch, hoverPhone });
  await shot('phone-moved');
  // five lines on one moment, on a phone: narrow columns, but none squeezed away
  const onOne = async () => {
    for (let i = 1; i < 5; i++) {
      const gap = (await lines()).map(l => seconds(l.time));
      const box = await point('.line:nth-of-type(' + (i + 1) + ') .grip');
      const up = (gap[i] - gap[0]) * scale;
      await touch('touchStart', box.x, box.y);
      await sleep(20);
      await touch('touchMove', box.x, box.y - up);
      await sleep(60);
      await touch('touchEnd');
      await sleep(40);
    }
  };
  await onOne();
  await point('.line:nth-of-type(1) .grip');
  await shot('phone-crowded');
  const narrowest = await inFrame(`Math.min(...[...document.querySelectorAll('.line')].slice(0, 5).map(c => c.querySelector('.words').getBoundingClientRect().width))`);
  report('lines crowded on a phone keep room for their words', narrowest >= 40, narrowest);
  report('and nothing runs off the side', (await inFrame(`document.documentElement.scrollWidth - document.documentElement.clientWidth`)) <= 0, await sticking());

  // a spoken page on a phone, and a line split with a finger
  await open('heard');
  report('a spoken page fits a phone: nothing runs off the side, hides or is cut off',
    (await inFrame(`document.documentElement.scrollWidth - document.documentElement.clientWidth`)) <= 0 && (await covered()) === 0 && (await clipped()) === 0,
    { sticking: await sticking(), covered: await covered(), clipped: await clipped() });
  const tap = async selector => {
    const p = await point(selector);
    await touch('touchStart', p.x, p.y);
    await sleep(20);
    await touch('touchEnd');
    await sleep(60);
  };
  await tap('.line:nth-of-type(2) .words');
  await inFrame(`document.querySelectorAll('.line')[1].querySelector('.words').setSelectionRange(33, 33)`);
  await until(async () => !(await off('split')), 'Split ready on a phone');
  await tap('#split');
  report('a finger can split a line', (await lines()).length === 11 && (await lines())[1].words === SAMPLE[1][1].slice(0, 33), (await lines()).slice(1, 3));
  report('the top bar stays small with Split and Join in it', (await inFrame(`document.querySelector('.status').offsetHeight`)) <= 64);
  await shot('phone-heard');

  // ---- dark
  await viewport(1280, 900);
  await send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: 'dark' }] }, page);
  await open('spoken');
  await shot('dark');
  report('no line is cut off in the dark', (await clipped()) === 0, await clipped());

  const real = noise.filter(n => !/favicon|404/.test(n));
  report('no error and no policy refusal in the whole run', real.length === 0, real.slice(0, 5));
} catch (e) {
  report('the run finished', false, e.stack);
} finally {
  chrome.kill();
  server.close();
  await sleep(100);
  fs.rmSync(profile, { recursive: true, force: true });
  if (!process.env.REVIEW_SHOTS) fs.rmSync(W, { recursive: true, force: true });
}
console.log(problems.length, 'problems', problems);
process.exit(problems.length ? 1 : 0);
