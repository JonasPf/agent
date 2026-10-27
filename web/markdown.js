'use strict';

// Markdown for what the agent says. The model writes markdown whether anything
// renders it or not, so a transcript that shows the source shows the seams:
// bullet stars, fence lines, and pipe tables read as noise around the answer.
// This is the smallest renderer that covers what a chat turn actually contains.
//
// It is also the only place in the interface that builds HTML out of model
// output, so it passes nothing through: the source is escaped first and every
// tag here is one this file constructed. Nested lists and reference links are
// not supported — they render as their own text rather than silently wrong.

function escapeHTML(s) {
  return String(s == null ? '' : s)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

// safeHref keeps a link to what a link can be: somewhere else on the web, an
// address, or a place in this app. javascript: and data: are not links, they
// are code, and a model can be talked into writing one.
function safeHref(url) {
  return /^(https?:\/\/|mailto:|\/|#)/i.test(String(url || '').trim());
}

// ---- files ----

// MENTION is a run of the characters a path is written with. Whatever else a
// sentence puts around a path ends it: a quote, a bracket, a comma, a colon
// and a line number, markdown's stars.
const MENTION = /[^\s"'`<>()[\]{}|,;:!?*&\\\u0000]+/g;

// splitMentions splits text into the files it names and the text between
// them. file says where a name opens, or null for a name that is not a file;
// a full stop ending the sentence is not part of the name.
function splitMentions(text, file) {
  const src = String(text == null ? '' : text);
  const parts = [];
  const add = t => { if (t) parts.push({ text: t }); };
  let at = 0;
  for (const m of src.matchAll(MENTION)) {
    const name = m[0].replace(/\.+$/, '');
    const href = name && file(name);
    if (!href) continue;
    add(src.slice(at, m.index));
    parts.push({ text: name, href });
    at = m.index + name.length;
  }
  add(src.slice(at));
  return parts;
}

// fileLink is the anchor for a file, around markup already made safe. The
// address is the page's own, never the model's.
function fileLink(href, html) {
  return '<a class="file" href="' + escapeHTML(href) + '" target="_blank" rel="noopener noreferrer">' + html + '</a>';
}

// ---- inline ----

const MARK = '\u0000';
const HELD = /\u0000(\d+)\u0000/;

// inline renders one line's spans. Code and links are held aside as finished
// HTML before emphasis runs, so a * inside a snippet or a URL is never read as
// markup. The placeholder is a NUL. Model output can hold a NUL of its own, so
// each one in the source is shown as the replacement character first: left
// in, one shaped like a placeholder was read as one, and inside a code span
// named the span itself and never stopped being restored.
//
// Given opts.file, a file the line names — a whole code span, or a path in the
// text — is a link to it.
function inline(src, opts) {
  const held = [];
  const hold = html => MARK + (held.push(html) - 1) + MARK;
  const file = (opts || {}).file;

  let s = String(src == null ? '' : src).replace(/\u0000/g, '\uFFFD')
    .replace(/`([^`]+)`/g, (m, c) => {
      const code = '<code>' + escapeHTML(c) + '</code>';
      const href = file && file(c.trim());
      return hold(href ? fileLink(href, code) : code);
    });
  s = escapeHTML(s);

  const link = (text, href) => {
    if (!safeHref(href)) return text;
    return hold('<a href="' + href + '" target="_blank" rel="noopener noreferrer">' + text + '</a>');
  };
  // An image is shown as a link to it. Rendering it would fetch a remote file
  // the operator never asked for, from a URL the model chose.
  s = s.replace(/!\[([^\]]*)\]\(([^)\s]+)\)/g, (m, alt, href) => link(alt || href, href));
  s = s.replace(/\[([^\]]*)\]\(([^)\s]+)\)/g, (m, text, href) => link(text, href));
  s = s.replace(/(^|[\s(])(https?:\/\/[^\s<]*[^\s<.,:;!?)\]])/g,
    (m, pre, url) => pre + link(url, url));
  // After the links, so a path inside an address stays part of the address.
  if (file) s = splitMentions(s, file).map(p => p.href ? hold(fileLink(p.href, p.text)) : p.text).join('');

  s = s.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
    .replace(/__([^_]+)__/g, '<strong>$1</strong>')
    .replace(/(^|[^*\w])\*([^*\n]+)\*/g, '$1<em>$2</em>')
    .replace(/(^|[^_\w])_([^_\n]+)_/g, '$1<em>$2</em>')
    .replace(/~~([^~]+)~~/g, '<del>$1</del>');

  // A held fragment can hold another — a code span inside a link's text — so
  // restoring runs until nothing is left standing in for something else.
  while (HELD.test(s)) s = s.replace(/\u0000(\d+)\u0000/g, (m, i) => held[Number(i)]);
  return s;
}

// inlineLines renders text whose newlines are line breaks — a wrapped paragraph
// or list item. A break the writer typed is a break they meant.
function inlineLines(text, opts) {
  return text.split('\n').map(line => inline(line, opts)).join('<br>');
}

// ---- blocks ----

const FENCE = /^\s*(```|~~~)(.*)$/;
const HEADING = /^(#{1,6})\s+(.*)$/;
const RULE = /^\s*([-*_])\s*(?:\1\s*){2,}$/;
const QUOTE = /^\s*>\s?(.*)$/;
const ITEM = /^(\s*)([-*+]|\d+[.)])\s+(.*)$/;
const DIVIDER = /^\s*\|?[\s:|-]*-[\s:|-]*$/;

// starts says whether a line opens a block of its own, and so ends the
// paragraph above it without a blank line between them.
function starts(line) {
  return FENCE.test(line) || HEADING.test(line) || RULE.test(line)
    || QUOTE.test(line) || ITEM.test(line);
}

function cells(line) {
  return line.trim().replace(/^\||\|$/g, '').split('|').map(c => c.trim());
}

// renderMarkdown renders a turn. opts.file, when given, says where a file the
// turn names opens, or null for a name that is not one.
function renderMarkdown(src, opts) {
  const lines = String(src == null ? '' : src).replace(/\r\n?/g, '\n').split('\n');
  return blocks(lines, opts).map(b => b.html).join('');
}

// markdownSettled is how much of an answer still arriving can be rendered once
// and left alone: every block but the last, on lines that are complete. Nothing
// written after it can change how those blocks render, and it is always where a
// block starts, so rendering the two sides apart gives exactly what rendering
// the whole gives. The source has \n line ends.
function markdownSettled(src) {
  const end = String(src).lastIndexOf('\n') + 1;
  if (!end) return 0;
  const lines = src.slice(0, end).split('\n');
  const all = blocks(lines);
  if (all.length < 2) return 0;
  let at = 0;
  for (let k = 0; k < all[all.length - 1].line; k++) at += lines[k].length + 1;
  return at;
}

// blocks reads lines into the blocks they make, each with the line it starts on.
function blocks(lines, opts) {
  const out = [];
  let i = 0;
  const push = (line, html) => out.push({ line, html });

  while (i < lines.length) {
    const line = lines[i];
    if (!line.trim()) { i++; continue; }
    const at = i;

    const fence = FENCE.exec(line);
    if (fence) {
      const lang = fence[2].trim().split(/\s+/)[0];
      const body = [];
      const close = new RegExp('^\\s*' + fence[1]);
      i++;
      while (i < lines.length && !close.test(lines[i])) body.push(lines[i++]);
      i++; // the closing fence — or the end of a turn that is still streaming
      push(at, '<pre' + (lang ? ' data-lang="' + escapeHTML(lang) + '"' : '') + '><code>'
        + escapeHTML(body.join('\n')) + '</code></pre>');
      continue;
    }

    if (RULE.test(line)) { push(at, '<hr>'); i++; continue; }

    const head = HEADING.exec(line);
    if (head) {
      const n = head[1].length;
      push(at, '<h' + n + '>' + inline(head[2].trim(), opts) + '</h' + n + '>');
      i++;
      continue;
    }

    if (QUOTE.test(line)) {
      const body = [];
      while (i < lines.length && QUOTE.test(lines[i])) body.push(QUOTE.exec(lines[i++])[1]);
      push(at, '<blockquote>' + renderMarkdown(body.join('\n'), opts) + '</blockquote>');
      continue;
    }

    if (ITEM.test(line)) {
      const ordered = /\d/.test(ITEM.exec(line)[2]);
      const items = [];
      while (i < lines.length) {
        const item = ITEM.exec(lines[i]);
        if (item) { items.push(item[3]); i++; continue; }
        // An indented line that is not an item continues the one above it.
        if (items.length && lines[i].trim() && /^\s+/.test(lines[i])) {
          items[items.length - 1] += '\n' + lines[i].trim();
          i++;
          continue;
        }
        break;
      }
      const tag = ordered ? 'ol' : 'ul';
      push(at, '<' + tag + '>'
        + items.map(t => '<li>' + inlineLines(t, opts) + '</li>').join('')
        + '</' + tag + '>');
      continue;
    }

    if (line.includes('|') && i + 1 < lines.length
      && lines[i + 1].includes('-') && DIVIDER.test(lines[i + 1])) {
      const header = cells(line);
      i += 2;
      const rows = [];
      while (i < lines.length && lines[i].trim() && lines[i].includes('|')) rows.push(cells(lines[i++]));
      push(at, '<table><thead><tr>'
        + header.map(c => '<th>' + inline(c, opts) + '</th>').join('')
        + '</tr></thead><tbody>'
        + rows.map(r => '<tr>' + r.map(c => '<td>' + inline(c, opts) + '</td>').join('') + '</tr>').join('')
        + '</tbody></table>');
      continue;
    }

    const para = [];
    while (i < lines.length && lines[i].trim() && !starts(lines[i])) para.push(lines[i++]);
    push(at, '<p>' + inlineLines(para.join('\n'), opts) + '</p>');
  }

  return out;
}

if (typeof module !== 'undefined' && module.exports) {
  module.exports = { renderMarkdown, markdownSettled, splitMentions };
}
