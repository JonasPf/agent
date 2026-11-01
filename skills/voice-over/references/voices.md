# Voices

The review page plays a sample of a voice when the operator picks it, every voice saying "Hello World
1 2 3". You cannot hear them, and the choice is theirs. The samples are in `assets/samples.json`, for
the page: it is 380 KB of base64, so never read it.

| | HeyGen Turbo | ElevenLabs Flash |
|---|---|---|
| Model | `heygen/heygen-voice-1-turbo` | `elevenlabs/eleven-flash-v2.5` |
| Good for | cheap, and good enough for most videos | when quality matters more than price |
| Per million characters | $4 | $40 |
| Ten-minute video, about 6,000 characters | $0.02 | $0.24 |
| Reading pace, words a second | 2.3 to 2.9 | about 2.7 |
| Audio it returns | WAV | MP3 |
| Most characters in one call | 5,000 | not tested; chunks stay far below it |
| Default voice | `gareth` | `brian` |

Prices are as of 2026-10-31; `estimate_cost.py` reads the current ones.

## HeyGen Turbo

`bram`, `gareth`, `lucian`, `meredith`, `nadine`, `noelle`, `sabrina`, `tabitha`, `vincent`, `weston`

## ElevenLabs Flash

`adam`, `alice`, `bella`, `bill`, `brian`, `callum`, `charlie`, `chris`, `daniel`, `eric`, `george`,
`harry`, `jessica`, `laura`, `liam`, `lily`, `matilda`, `river`, `roger`, `sarah`, `will`
