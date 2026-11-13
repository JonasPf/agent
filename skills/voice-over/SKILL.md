---
name: voice-over
description: Use when the operator wants a synthesized voice-over on a video — narration taken from what was said, its ums taken out, and spoken again over the picture; or a script, the operator's or yours, spoken over a recording with no narration. The voice-over plugin's tools do the work; this says how to start them here.
default: off
---

The voice-over plugin does this with its tools, and each tool's description says how to use it:
`voiceover_frames` to look at the video, `voiceover_transcribe`, `voiceover_script`,
`voiceover_render`, and `voiceover_review`, which shows the operator a page to change it on.

1. **Install ffmpeg first.** The plugin carries none of its own yet. Before its first tool in a
   conversation, install `ffmpeg_8-headless` with `packages`.
2. **Cut first.** If the video also needs cutting or joining, do that first with the `video-edit`
   skill: a cut moves every time after it.
3. **Then in order:** `voiceover_transcribe` (with the names the recording is likely to use as
   `terms`), `voiceover_script` until its table is right, `voiceover_render`, then
   `voiceover_review`, and end your turn. Send on the page brings you a message with the next step.
   A recording with no narration skips transcribing: place its lines with `at`.
4. **Asked to check the script before anything is spoken:** `voiceover_review` after
   `voiceover_script`, before `voiceover_render`.
5. **A scheduled run, or told to go ahead without asking:** no page. Render, and report what was
   made, what it cost, and every line cut short.
6. **No key:** a tool that says the plugin has no OpenRouter key is waiting for the operator, who
   sets it on the Plugins screen. Say so and stop; nothing else fixes it.
