# Plan 03: redesign the operator interface

## Status

| Step | What | State |
|---|---|---|
| U1 | Audit: screenshots of every screen, list of what feels rough | Not started |
| U2 | Two directions as static mocks; **Jonas picks one** | Not started |
| U3 | Tokens, type, spacing, motion as a small system | Not started |
| U4 | Shell and navigation | Not started |
| U5 | Conversation view and composer | Not started |
| U6 | Lists, forms, and the remaining screens | Not started |
| U7 | States: empty, loading, error, offline | Not started |
| U8 | Pass over phone, dark mode, accessibility | Not started |

Independent of plans 01 and 02, except that plan 01 step G (the Plugins screen) should be built
in the new design if U3 has landed by then.

## What was asked

"Redesign the operator interface to make it look more slick." Nothing more specific. That makes
the first two steps the important ones: find out what is rough, and let Jonas choose a direction
from something he can see. **Do not start restyling before he has picked** — taste is his call.

One hint: his personal instruction file contains the single line "sliding effect". Treat it as a
preference for views that slide rather than swap, and put it in front of him in U2 to confirm.

## What exists

- No framework and no build step: `web/index.html` (53 lines), `web/app.js` (3,600 lines, every
  screen), `web/style.css` (1,070 lines), plus `markdown.js`, `transcript.js`, `notify.js`,
  `models.js`, `sw.js`. Served as files, revalidated on every load. **Keep it that way** unless a
  step shows a concrete need; a build step is a new thing that can break a deploy.
- A warm paper palette with a copper accent, light and dark by `prefers-color-scheme`, system
  fonts, a 288 px side rail and a 780 px reading column. Tokens are CSS variables at the top of
  `style.css`.
- It is a phone-first PWA: Jonas uses it on a phone as much as a laptop.
- The specification is `specs/webui.html` (about 600 lines, many acceptance criteria) with static
  mocks `specs/mock.html`, `mock-tools.html`, `mock-skills.html`, `mock-metrics.html`. Behaviour
  is tested by `web/*.test.js` (pure node; `shell.test.js` is 2,900 lines) and by browser tests in
  the Go suite.

## Constraints

- **Every acceptance criterion in `specs/webui.html` still holds.** This is a redesign of how it
  looks and moves, not of what it does. Where a criterion fixes a visual detail that the new
  design changes, change the criterion in the same pull request and say so.
- The mocks in `specs/` are part of the spec: a screen's mock changes with the screen.
- No third-party requests from the page: no CDN, no hosted fonts. A font, if one is wanted, is
  a file in `web/`.
- Model output is rendered only through `web/markdown.js`, which escapes everything. Do not add
  another path from model text to HTML.
- Motion respects `prefers-reduced-motion`.
- Contrast: text at least 4.5:1 in both themes. The chart colours were chosen so a red is never
  beside the accent (comment at the top of `style.css`); keep that rule.
- Performance on a phone matters more than effects: no layout that reflows the transcript while
  a reply streams in (there are tests about scroll position staying put).

## U1 — audit

Run `task dev`, open http://localhost:7770. Take screenshots of every screen at 390 px and
1440 px, light and dark: conversation (empty, mid-reply, with tool calls, with an error), the
list, new and fork screens, details, files, jobs, tools, skills, personas, metrics, search,
version, an `ask_operator` panel. Write down, per screen, what looks rough and why: spacing that
is uneven, type sizes that do not form a scale, borders doing the work spacing should, controls
of several heights, abrupt changes of view. Put the list in this file under "Findings".

## U2 — two directions

Build two static mocks of the same three screens (conversation, list on a phone, one settings
screen) as `specs/mock-direction-a.html` and `-b.html`, each self-contained. Make them really
different, for example: (a) the present warm palette refined — tighter type scale, fewer lines,
more air, sliding views; (b) a cooler, denser, more "tool-like" look with a stronger hierarchy.
If a design skill is available to the model (`frontend-design`), use it here.

Ask Jonas to pick, and ask at the same time: sliding transitions yes or no; a custom font or
system fonts; whether the copper accent stays. Record his answers in this file under "Chosen
direction". Delete the mock that lost.

## U3 — the system

Before any screen: a short set of tokens in `style.css` and nothing outside it.

- A type scale of five or six sizes with fixed line heights; a spacing scale (4-based); three
  radii; two shadows; durations and one easing curve for motion.
- One height for every control in a row (buttons, inputs, chips); one focus ring.
- Replace literal values across `style.css` with the tokens. This pull request changes no layout;
  it should look almost the same, and make the next ones small.

## U4 — shell and navigation

The side rail, the header, moving between list and conversation on a phone. If sliding was
chosen: the conversation slides in over the list and back, driven by the existing hash routes
(`route()` in `app.js`), with the browser's back gesture still working. Tests in `shell.test.js`
cover the routes; add ones for what is new.

## U5 — conversation and composer

The screen that is used most, so the most care: message rhythm, how tool calls group and
collapse, the streaming reply, the status line (cost, cache), the composer (growing, attachments,
send and stop). Read the "Conversation view" section of `specs/webui.html` in full first; it has
the most criteria and several exist because of real bugs (scroll position, composition keys,
drafts).

## U6 — the rest

Lists (conversations, jobs, files tree), forms (new, fork, details), and the asset screens
(tools or plugins, skills, personas), metrics. One pull request per group.

## U7 — states

Empty lists, first run, loading, a failed request, the breaker banner, offline in the PWA. Each
gets a designed state instead of a blank or a bare sentence.

## U8 — the pass

Every screen again at 390 px and 1440 px, light and dark, with the keyboard only and with a
screen reader's rotor on the conversation. Fix what the pass finds. Retake the U1 screenshots and
show Jonas before and after.

## Findings

(Filled in by U1.)

## Chosen direction

(Filled in by U2.)
