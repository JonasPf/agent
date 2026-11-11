# Plan 02: voice-over as an MCP bundle for any agent

## Status

| Step | What | State |
|---|---|---|
| V1 | Read the existing skill; write the tool contract | Not started |
| V2 | Server skeleton, project files, ffmpeg handling | Not started |
| V3 | Cloud backend (OpenRouter): transcribe and speak | Not started |
| V4 | Script check, placing, muxing | Not started |
| V5 | Review page, three ways to show it | Not started |
| V6 | Packaging, bundled ffmpeg, install notes for other agents | Not started |
| V7 | Switch this agent's `voice-over` skill to the bundle | Not started |
| V8 | Local backends: transcription and speech without a key | Not started. Research first |

**Depends on plan 01:** step E (the `agent-plugins` repository exists), step F (a bundle can be
given a configuration value, here the key). Step I (MCP Apps) improves V5 but does not block it.
V1–V6 can be built and tested without this agent at all.

## Goal

Today voice-over is a skill for this agent only: 13 KB of instructions in
`skills/voice-over/SKILL.md` that drive five Python scripts through `bash`, the agent's
`call_model` tool, `packages` for ffmpeg, and `ask_operator` for the review page.

After this plan it is one MCP server, a single Go binary in an MCPB bundle, that works the same
in Claude Code, Codex, OpenCode and other MCP hosts, and in this agent. It needs nothing from its
host but the protocol.

## Decisions already made (with Jonas)

1. **Go, one static binary.** Not Python: other machines would need Python and its packages, and
   this agent's loader runs only `binary` servers.
2. **Its own gateway key.** The server calls the speech and transcription models itself, with an
   OpenRouter key given as a sensitive `user_config` value. In this agent that is a deliberate
   exception to "no program holds the model key": it is a **separate** key with a spend limit,
   never the agent's `OPENROUTER_API_KEY`.
3. **ffmpeg is bundled.** No reliance on the host's packages.
4. **Local backends exist as a second choice**, used when no key is given.
5. **The review page is shown three ways** from one page: MCP Apps where the host supports it,
   a localhost page where the server runs on the user's machine, and this agent's `ask_operator`
   panel until the agent hosts MCP Apps.

## What exists today (read all of it in V1)

| File | Lines | What it does |
|---|---|---|
| `skills/voice-over/SKILL.md` | ~180 | The whole procedure, and every trap found so far. The traps are worth more than the steps |
| `scripts/extract_audio.py` | 75 | Audio out of the video as 64 kbps mp3, split into parts when long; warns over 30 min, stops over 60 |
| `scripts/place_chunks.py` | 236 | Finds each line's start from `starts_with` in the transcript; `--check` prints the table; trims, levels, places speech into `voiceover_track.wav` |
| `scripts/make_review.py` | 316 | `--speak-all` / `--apply` list what must be spoken; builds `review/review.html` with screenshots inside it |
| `scripts/vo_mux.py` | 41 | Puts the track on the video, video stream copied |
| `scripts/estimate_cost.py` | 53 | What the speech will cost |
| `assets/review.html` | 564 | The review page; talks to the panel through `ask.answer`, `ask.hold`, `ask.save` |
| `assets/samples.json` | 382 KB | Voice samples the page plays |
| `references/voices.md` | | Voices and models |
| `evals/*` | 5 cases | End-to-end cases with graders |
| `testenv/skills/voice_over_*.py`, `voice_over_page_browser.mjs` | ~1,170 | Tests of the scripts on real video and of the page in a real browser. **These are the acceptance tests for the port** |

How the agent makes the calls the server must now make itself: `internal/app/call_model.go` and
`internal/app/gateway_kinds.go` (`speakAs` → `POST /audio/speech`, transcription →
`POST /audio/transcriptions`, on the OpenRouter base URL; retries, the WAV-or-MP3 naming rule,
cost reporting). Port that behaviour; do not guess the API.

## V1 — the tool contract

Read everything in the table. Then write `bundles/voice-over/CONTRACT.md` in `agent-plugins`:
each tool's name, description, parameters, what it writes, what it returns. Proposed shape
(adjust after reading the scripts, and keep the count small):

| Tool | Replaces | Returns |
|---|---|---|
| `voiceover_inspect {video}` | step 1 | duration, whether there is audio, resolution, fps |
| `voiceover_transcribe {video, language?, terms?}` | steps 2–3 | the transcript segments as text (the model needs them to write the script); writes `segments*.txt` |
| `voiceover_script {chunks: [{starts_with? , at?, text}]}` | step 4 and `--check` | the placement table: each line's start, what the narrator says there, `~` near matches, `TIGHT` lines. Writes `chunks.json`. Called again until the table is right |
| `voiceover_render {voice?, speak?: "all" \| "changed" \| "none"}` | steps 5–8 | speaks what is missing (in parallel, backing off on 429), places, muxes; returns the output path, model and voice, cost, every line cut short |
| `voiceover_review {}` | step 9 | builds the page and shows it (V5) |
| `voiceover_apply_review {}` | `--apply` | applies `review/approved.json`, re-renders what changed |
| `voiceover_voices {language?}` | `references/voices.md` | voices available on the active backend, with cost |

Rules for the contract:

- **State is files** in a project folder in the working directory, as now (`chunks.json`,
  `segments.txt`, `chunks/`, `review/`, `voiceover.mp4`). This agent starts the server anew for
  every call; other hosts keep it running. Both must work, so nothing may live only in memory.
- **The judgement stays with the model:** cleaning the transcript and writing the lines. Put the
  rules for it (keep the narrator's words, never invent a time, what `starts_with` is) into the
  description of `voiceover_script`, because a tool description is the one text every host shows
  the model. Also offer them as the server's `instructions` and as an MCP prompt.
- **Every trap in `SKILL.md` lands somewhere:** enforced in code where it can be (never a `:free`
  model; name files by what the provider actually returned), in a description otherwise. Make a
  checklist of the traps in the contract and tick each one off.
- One `speak` of all lines is one tool call. Today it is one model round per ten lines, against a
  limit of a hundred rounds; removing that is the main gain for this agent.

**Done when:** Jonas has read the contract (it is short) and the trap checklist is complete.

## V2 — skeleton

- `bundles/voice-over/` in `agent-plugins`: `main.go` (go-sdk server over stdio), `manifest.json`
  (MCPB 0.3, `binary`), packages `project` (the files), `media` (ffmpeg calls), `speech`
  (backends), `review` (the page).
- **ffmpeg lookup order:** `user_config.ffmpeg_path` if set; the bundled binary beside the server
  (`${__dirname}/bin/ffmpeg`); then `PATH`. Say which one is in use in `voiceover_inspect`'s result.
  Use `ffmpeg` alone where `ffprobe` was used, or bundle both.
- Tests generate their own short videos with ffmpeg (as `testenv/skills/video_edit.py` does);
  no binary fixtures in git.

## V3 — cloud backend

Two small interfaces, so V8 can add implementations without touching the tools:

```go
type Transcriber interface {
    Transcribe(ctx context.Context, audio string, o TranscribeOptions) ([]Segment, Cost, error)
}
type Speaker interface {
    Voices(ctx context.Context, language string) ([]Voice, error)
    Speak(ctx context.Context, text string, v Voice, out string) (path string, c Cost, err error)
}
```

- OpenRouter implementation ported from `gateway_kinds.go`. Defaults as the skill has them today:
  transcription `openai/whisper-large-v3-turbo` with timestamps, voice `gareth` on HeyGen. The
  skill records which other models fail and why (no times, one block); keep that list as a
  refusal with the reason.
- Key from `user_config.openrouter_api_key` (the manifest passes it as an environment variable).
  Never log it, never put it in a result.
- Tests against a fake gateway (`httptest`), including a 429 and a provider that returns MP3
  where WAV was asked for.

## V4 — check, place, mux

A faithful port of `place_chunks.py` and `vo_mux.py`. Port `testenv/skills/voice_over_limits.py`
and the placement cases of `voice_over_page.py` to Go tests first and make them pass. Behaviours
that must survive: a line starts exactly where it was put; a line still speaking when the next
starts is cut there and reported, never moved; a recording in parts has each part's times shifted
to where the part begins; the output is as long as the input with the video stream untouched.

## V5 — the review page, three ways

One page (`assets/review.html`), with its calls to the host behind one small adapter in the page:

| Where | How the page is shown | How Send gets back |
|---|---|---|
| A host with **MCP Apps** | the tool declares a `ui://` resource | the page calls `voiceover_save_review` through the host |
| The server runs **on the user's machine** (Claude Code, Codex, OpenCode) | the server listens on `127.0.0.1` on a free port, with a random token in the link; the tool returns the link (as a URL-mode elicitation if the client says it supports one, as text otherwise) | the page posts to that listener, which writes `review/approved.json` |
| **This agent**, until plan 01 step I | the tool writes `review/review.html` and its result tells the model to call `ask_operator` with that page | the panel's `ask.answer` / `ask.save`, as today |

- Which way: `user_config.review` = `auto` | `apps` | `browser` | `panel`. `auto` picks Apps if
  the client declares it, `panel` if the client's name is this agent's (`agent`, see
  `bundles.go`), `browser` otherwise.
- The localhost listener needs the process to stay alive: it stops after Send, or after two hours
  idle. It must never start in `panel` mode (in this agent the process ends with the call, and no
  port is reachable anyway).
- Read the MCP Apps and elicitation specifications before writing either path
  (`plans/README.md` lists what is already known and what was only skimmed).
- `voice_over_page_browser.mjs` is the page's acceptance test (phone widths, dragging, splitting,
  Send). Keep it running against the panel adapter and add a run against the localhost adapter.

## V6 — packaging

- **ffmpeg build:** static, LGPL only, only what is used — decoding the common containers and
  codecs of phone and screen recordings, encoding AAC, MP3 and PCM, PNG or JPEG for screenshots;
  the video stream is copied, so no H.264 encoder is needed. Build it in CI from source at a
  pinned version, or take a pinned build by digest. Ship its licence and a notice of the source.
  Get this checked before the first public release; it is the one legal item.
- One `.mcpb` per platform (`linux/amd64`, `linux/arm64`, `darwin/arm64`; Windows only if asked),
  so nobody downloads four ffmpegs.
- `README.md` with install steps for Claude Code, Codex, and OpenCode. **Look up each one's
  current way to add an MCP server when writing this; do not write it from memory.**
- `_meta["io.github.jonaspf.agent"].timeout_seconds`: a render of a long video takes minutes.
  Start with 1800 and say so in the manifest.

## V7 — switch this agent over

- Install the bundle (plan 01 step F) and give it its key.
- Replace `skills/voice-over` with a short skill (in `agent-plugins/skills/`) holding only what
  is this agent's: when to use the tools, and "call `ask_operator` with the page the review tool
  names". Everything else is in the tool descriptions.
- Run the five cases in `skills/voice-over/evals` against the new setup and compare rounds and
  cost per run with the old skill. Report both numbers to Jonas; fewer rounds is the claim this
  plan makes.

## V8 — local backends

**Research first, then decide; write the findings into this file.** Candidates named so far,
from memory and unverified: whisper.cpp for transcription with timestamps; Piper or Kokoro for
speech. For each, find out: licence (can it be redistributed or only downloaded?), how to run it
from Go without cgo (a helper executable is fine), model sizes, speed on a laptop CPU, memory,
and whether timestamps are per word or per phrase.

Design constraints already fixed:

- Same two interfaces as V3. Backend choice: `user_config.backend` = `auto` | `openrouter` |
  `local`; `auto` is OpenRouter when a key is set, local otherwise.
- **Models and helper programs are downloaded on first use**, by pinned URL and digest, into the
  user's cache directory. They are not in the bundle: they are hundreds of megabytes.
- **Off in this agent by default.** Its container has a 512 MB limit, and the old skill records
  that local Whisper was a gigabyte per conversation and ten times slower. In `panel` mode with no
  key, say that a key is needed rather than downloading a model.
- Say in every result which backend spoke, so nobody mistakes a local voice for the paid one.
- Local voices are worse than the hosted ones. Record a sample of each in the page's voice list
  (`testenv/skills/record_voice_samples.py` shows how the current samples were made).
