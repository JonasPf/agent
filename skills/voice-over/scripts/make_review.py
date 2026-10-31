#!/usr/bin/env python3
"""Build the page where the operator checks the script before any speech is bought.

Usage: make_review.py INPUT.mov [--voice NAME]   write review/review.html
       make_review.py --apply                    turn review/approved.json into chunks.json

The page shows a screenshot every 30 seconds with the lines that start there. The
operator changes the words, moves the lines, and picks the language and the voice, and
Approve saves review/approved.json beside the page. It starts from chunks.json, whose
times are found as place_chunks.py finds them. With no chunks.json it starts empty, and
the operator writes the script on the page.

Show the page with ask_operator. It runs with no network, so the screenshots are inside
it, and they are made smaller when a page would be too big for ask_operator to load.
Requires ffmpeg and ffprobe on PATH (packages tool, ffmpeg_8-headless).
"""
import argparse, base64, json, math, os, re, shutil, subprocess, sys

# A skill's folder holds text only: one .pyc beside these scripts and the skill will not load.
sys.dont_write_bytecode = True
import estimate_cost, place_chunks

HERE = os.path.dirname(os.path.abspath(__file__))
TEMPLATE = os.path.join(HERE, "..", "assets", "review.html")
VOICES = os.path.join(HERE, "..", "references", "voices.md")
MARK = "__REVIEW_DATA__"
FOLDER = "review"
PAGE = os.path.join(FOLDER, "review.html")
APPROVED = os.path.join(FOLDER, "approved.json")
STEP = 30
MAX_SHOTS = 120
WIDTH = 800
QUALITY = "6"
# ask_operator loads up to 16 MiB. This leaves room for what the agent adds later.
MAX_PAGE = 15 << 20
SMALLER_TRIES = 6
# Each was spoken on both models and read back word for word by Whisper, on 2026-10-31.
LANGUAGES = [("en", "English"), ("de", "German"), ("es", "Spanish"), ("fr", "French"),
             ("it", "Italian"), ("nl", "Dutch"), ("pt", "Portuguese")]

def voices():
    """The speech options: voices from voices.md, prices from the gateway."""
    text = open(VOICES, encoding="utf-8").read()
    prices = estimate_cost.live_prices()
    options = []
    for name, model, default, price in estimate_cost.OPTIONS:
        section = text.split(f"## {name}\n", 1)[1].split("\n## ", 1)[0]
        options.append(dict(name=name, model=model, default=default, price=prices.get(model, price),
                            voices=re.findall(r"`([a-z]+)`", section)))
    return options

def draft(path, length):
    """The lines the page starts with, each at its time."""
    if not os.path.exists(path):
        return []
    chunks = json.load(open(path, encoding="utf-8"))
    needs_words = any("at" not in c for c in chunks)
    if needs_words and not place_chunks.segment_files():
        sys.exit("segments.txt not found: save the transcript's lines there first")
    rows, errors = place_chunks.plan(chunks, place_chunks.load_words() if needs_words else [])
    errors += [f"chunk {r['n']:02d} starts at {r['at']:.1f} s, after the video's {length:.1f} s"
               for r in rows if r["at"] >= length]
    if errors:
        sys.exit("\n".join(errors))
    return [dict(at=round(r["at"], 2), text=r["text"]) for r in rows]

def grab(exe, video, at):
    """The frame at this exact time. A keyframe can be 18 seconds away."""
    r = subprocess.run([exe, "-hide_banner", "-loglevel", "error", "-ss", f"{at:.3f}", "-i", video,
                        "-frames:v", "1", "-vf", f"scale='min({WIDTH},iw)':-2", "-q:v", QUALITY,
                        "-f", "image2pipe", "-c:v", "mjpeg", "-"], capture_output=True)
    if r.returncode or not r.stdout:
        sys.exit(f"no screenshot at {at:.0f} s: {r.stderr.decode(errors='replace').strip()[-200:]}")
    return r.stdout

def smaller(exe, jpeg, scale):
    r = subprocess.run([exe, "-hide_banner", "-loglevel", "error", "-f", "jpeg_pipe", "-i", "-",
                        "-vf", f"scale=iw*{scale:.3f}:-2", "-q:v", QUALITY,
                        "-f", "image2pipe", "-c:v", "mjpeg", "-"], input=jpeg, capture_output=True)
    if r.returncode or not r.stdout:
        sys.exit(f"could not make a screenshot smaller: {r.stderr.decode(errors='replace').strip()[-200:]}")
    return r.stdout

def encoded(jpeg):
    return "data:image/jpeg;base64," + base64.b64encode(jpeg).decode()

def fit(exe, shots, room):
    """Shrink the screenshots together until they fit. Their size goes with their area."""
    for _ in range(SMALLER_TRIES):
        size = sum(len(encoded(s)) for s in shots)
        if size <= room:
            return shots
        scale = max(0.4, min(0.9, 0.95 * math.sqrt(room / size)))
        shots = [smaller(exe, s, scale) for s in shots]
    sys.exit("the screenshots will not fit in a page")

def build(video, wanted, chunks):
    exe = shutil.which("ffmpeg")
    if not exe:
        sys.exit("ffmpeg not found on PATH: install it with the packages tool (ffmpeg_8-headless)")
    if not os.path.exists(video):
        sys.exit(f"{video} not found")
    options = voices()
    last = json.load(open(APPROVED, encoding="utf-8")) if os.path.exists(APPROVED) else {}
    voice = wanted or last.get("voice") or options[0]["default"]
    model = next((o["model"] for o in options if voice in o["voices"]), None)
    if not model:
        sys.exit(f"{voice} is not a voice of either model: see references/voices.md")

    length = place_chunks.duration(video)
    lines = draft(chunks, length)
    step = STEP * max(1, math.ceil(length / (STEP * MAX_SHOTS)))
    shots = [grab(exe, video, max(0, min(t, length - 1))) for t in range(0, math.ceil(length), step)]

    data = dict(video=os.path.basename(video), duration=length, step=step, voices=options, model=model,
                voice=voice, languages=LANGUAGES, lines=lines, pace=place_chunks.WORDS_PER_SECOND,
                max_push=place_chunks.MAX_PUSH, breath=place_chunks.BREATH, max_chars=place_chunks.MAX_CHARS)
    template = open(TEMPLATE, encoding="utf-8").read()
    # Inside a script block, the one thing that can end it is "<".
    rest = len(json.dumps(data).replace("<", "\\u003c"))
    shots = fit(exe, shots, MAX_PAGE - len(template.encode()) - rest - 2 * len(shots) - 2)
    data["shots"] = [encoded(s) for s in shots]
    html = template.replace(MARK, json.dumps(data).replace("<", "\\u003c"))

    os.makedirs(FOLDER, exist_ok=True)
    if os.path.exists(APPROVED):
        os.remove(APPROVED)
    with open(PAGE, "w", encoding="utf-8") as f:
        f.write(html)
    print(f"{PAGE}: {len(shots)} screenshots, {len(lines)} lines, {len(html) / 1e6:.1f} MB")

def apply_approval(chunks):
    a = json.load(open(APPROVED, encoding="utf-8")) if os.path.exists(APPROVED) else {}
    if not a:
        sys.exit("not approved yet: ask the operator to press Approve on the page, then Send")
    if not a.get("chunks"):
        sys.exit("the approved script has no lines")
    option = next((o for o in voices() if o["model"] == a.get("model")), None)
    if not option:
        sys.exit(f"{a.get('model')} is not one of the speech models")
    if a.get("voice") not in option["voices"]:
        sys.exit(f"{a.get('voice')} is not a voice of {option['model']}")
    language = a.get("language", "")
    if language and language not in dict(LANGUAGES):
        sys.exit(f"{language} is not a language this skill has tried")
    timed = [dict(at=float(c["at"]), text=c["text"]) for c in a["chunks"]]
    _, errors = place_chunks.plan(timed, [])
    if errors:
        sys.exit("\n".join(errors))

    with open(chunks, "w", encoding="utf-8") as f:
        json.dump(timed, f, ensure_ascii=False, indent=1)
    # A chunk's speech is found by its place in the list, and the list has changed.
    if os.path.isdir("chunks"):
        shutil.rmtree("chunks.old", ignore_errors=True)
        os.rename("chunks", "chunks.old")
        print("Earlier speech is now in chunks.old/. Speak every chunk again.")
    chars = sum(len(c["text"]) for c in timed)
    cost = estimate_cost.money(chars * option["price"] / 1e6)
    print(f"Approved: {len(timed)} chunks, {chars:,} characters, about {cost} with {a['voice']} on {option['name']}.")
    if language:
        print(f"Translate every text in {chunks} into {dict(LANGUAGES)[language]}, keep every at, then run "
              "make_review.py again so the operator sees it before it is spoken.")
    else:
        print(f"Speak with model {option['model']}, voice {a['voice']}.")

def main():
    ap = argparse.ArgumentParser(usage=__doc__)
    ap.add_argument("video", nargs="?")
    ap.add_argument("--voice", help="start the page on this voice")
    ap.add_argument("--apply", action="store_true")
    ap.add_argument("--chunks", default="chunks.json")
    a = ap.parse_args()
    if a.apply:
        apply_approval(a.chunks)
    elif a.video:
        build(a.video, a.voice, a.chunks)
    else:
        sys.exit("name the video, or pass --apply")

if __name__ == "__main__":
    main()
