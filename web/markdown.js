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

// ---- inline ----

const MARK = '\u0000';
const HELD = /\u0000(\d+)\u0000/;

// inline renders one line's spans. Code and links are held aside as finished
// HTML before emphasis runs, so a * inside a snippet or a URL is never read as
// markup. The placeholder is a NUL, which the source text cannot contain.
function inline(src) {
  const held = [];
  const hold = html => MARK + (held.push(html) - 1) + MARK;

  let s = String(src == null ? '' : src)
    .replace(/`([^`]+)`/g, (m, c) => hold('<code>' + escapeHTML(c) + '</code>'));
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
function inlineLines(text) {
  return text.split('\n').map(inline).join('<br>');
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

function renderMarkdown(src) {
  const lines = String(src == null ? '' : src).replace(/\r\n?/g, '\n').split('\n');
  const out = [];
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];
    if (!line.trim()) { i++; continue; }

    const fence = FENCE.exec(line);
    if (fence) {
      const lang = fence[2].trim().split(/\s+/)[0];
      const body = [];
      const close = new RegExp('^\\s*' + fence[1]);
      i++;
      while (i < lines.length && !close.test(lines[i])) body.push(lines[i++]);
      i++; // the closing fence — or the end of a turn that is still streaming
      out.push('<pre' + (lang ? ' data-lang="' + escapeHTML(lang) + '"' : '') + '><code>'
        + escapeHTML(body.join('\n')) + '</code></pre>');
      continue;
    }

    if (RULE.test(line)) { out.push('<hr>'); i++; continue; }

    const head = HEADING.exec(line);
    if (head) {
      const n = head[1].length;
      out.push('<h' + n + '>' + inline(head[2].trim()) + '</h' + n + '>');
      i++;
      continue;
    }

    if (QUOTE.test(line)) {
      const body = [];
      while (i < lines.length && QUOTE.test(lines[i])) body.push(QUOTE.exec(lines[i++])[1]);
      out.push('<blockquote>' + renderMarkdown(body.join('\n')) + '</blockquote>');
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
      out.push('<' + tag + '>'
        + items.map(t => '<li>' + inlineLines(t) + '</li>').join('')
        + '</' + tag + '>');
      continue;
    }

    if (line.includes('|') && i + 1 < lines.length
      && lines[i + 1].includes('-') && DIVIDER.test(lines[i + 1])) {
      const header = cells(line);
      i += 2;
      const rows = [];
      while (i < lines.length && lines[i].trim() && lines[i].includes('|')) rows.push(cells(lines[i++]));
      out.push('<table><thead><tr>'
        + header.map(c => '<th>' + inline(c) + '</th>').join('')
        + '</tr></thead><tbody>'
        + rows.map(r => '<tr>' + r.map(c => '<td>' + inline(c) + '</td>').join('') + '</tr>').join('')
        + '</tbody></table>');
      continue;
    }

    const para = [];
    while (i < lines.length && lines[i].trim() && !starts(lines[i])) para.push(lines[i++]);
    out.push('<p>' + inlineLines(para.join('\n')) + '</p>');
  }

  return out.join('');
}

if (typeof module !== 'undefined' && module.exports) {
  module.exports = { renderMarkdown };
}
