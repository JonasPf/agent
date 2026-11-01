#!/usr/bin/env python3
"""Find when the narrator said each chunk, and lay the spoken chunks on a silent track.

Usage: place_chunks.py --check            find every chunk's start, before any speech
       place_chunks.py INPUT.mov          build voiceover_track.wav from chunks/NN.*

Reads chunks.json, a list of {"text", "starts_with"} in spoken order, or {"text", "at"}
for a video with no narration, and segments.txt, the transcript's "[start–end] words"
lines. A recording cut into parts has segments-000.txt, segments-001.txt, ... instead,
and each part's times are moved to where that part begins.

A chunk starts where its starts_with words are in the transcript, so no time is chosen
by hand. Each chunk is trimmed and levelled, and starts exactly at its time: one still
speaking when the next starts is cut off there, with a short fade, and is named. Only a
last chunk that runs past the end of the video stops the track being written. Requires
ffmpeg on PATH for the second form.
"""
import argparse, array, glob, json, os, re, shutil, subprocess, sys, tempfile, wave
from difflib import SequenceMatcher

RATE = 44100
PART_SECONDS = 2400
MIN_WORDS = 3
FUZZY = 0.8
# Frames a cut-off chunk fades over, so that it ends without a click: 20 ms.
FADE = 882
TARGET_DB = -20.0
WORDS_PER_SECOND = 2.3
# HeyGen refuses a call over 5,000 characters, after the agent has already paid to send it.
MAX_CHARS = 4500
BLOCK = 1 << 20
TRIM = ("silenceremove=start_periods=1:start_threshold=-45dB,areverse,"
        "silenceremove=start_periods=1:start_threshold=-45dB,areverse")
LINE = re.compile(r"^\[(\d+(?:\.\d+)?)\s*[–-]\s*(\d+(?:\.\d+)?)\]\s*(.*)$")

def norm(s):
    return re.sub(r"[^a-z0-9]", "", s.lower())

def tokens(s):
    return [t for t in (norm(w) for w in s.split()) if t]

def segment_files():
    if os.path.exists("segments.txt"):
        return ["segments.txt"]
    return sorted(glob.glob("segments-*.txt"))

def load_words():
    """Every transcript word with a time, spread evenly across its segment."""
    words = []
    for part, path in enumerate(segment_files()):
        shift = part * PART_SECONDS
        for line in open(path, encoding="utf-8"):
            m = LINE.match(line.strip())
            if not m:
                continue
            start, end = float(m.group(1)), float(m.group(2))
            raw = [w for w in m.group(3).split() if norm(w)]
            for i, w in enumerate(raw):
                words.append((shift + start + (end - start) * i / len(raw), norm(w), w))
    return words

def find(words, want, cursor):
    """The first place at or after cursor that says want, exactly or nearly."""
    n = len(want)
    for j in range(cursor, len(words) - n + 1):
        if [w[1] for w in words[j:j + n]] == want:
            return j, True
    target, best, at = "".join(want), 0.0, None
    for j in range(cursor, len(words) - n + 1):
        ratio = SequenceMatcher(None, target, "".join(w[1] for w in words[j:j + n])).ratio()
        if ratio > best:
            best, at = ratio, j
    return (at, False) if best >= FUZZY else (None, False)

def plan(chunks, words):
    """Each chunk's start, in spoken order, or the reasons there is none."""
    rows, errors, cursor, last = [], [], 0, 0.0
    for i, c in enumerate(chunks, 1):
        text = c.get("text", "").strip()
        if not text:
            errors.append(f"chunk {i:02d} has no text")
            continue
        if len(text) > MAX_CHARS:
            errors.append(f"chunk {i:02d} has {len(text):,} characters, over the {MAX_CHARS:,} one speech call can take: split it")
            continue
        if "at" in c:
            at, heard, exact = float(c["at"]), "(set by hand)", True
        else:
            want = tokens(c.get("starts_with", ""))
            if len(want) < MIN_WORDS:
                errors.append(f"chunk {i:02d}: starts_with needs at least {MIN_WORDS} words")
                continue
            j, exact = find(words, want, cursor)
            if j is None:
                errors.append(f"chunk {i:02d}: \"{c['starts_with']}\" is not in the transcript after {last:.1f} s")
                continue
            at, heard = words[j][0], " ".join(w[2] for w in words[j:j + 6])
            cursor = j + len(want)
        if at < last:
            errors.append(f"chunk {i:02d} starts at {at:.1f} s, before the chunk ahead of it")
        last = at
        rows.append(dict(n=i, at=at, text=text, heard=heard, exact=exact))
    return rows, errors

def show_plan(rows):
    print(" #    start    gap   est  says                            narrator at that moment")
    for k, r in enumerate(rows):
        gap = rows[k + 1]["at"] - r["at"] if k + 1 < len(rows) else None
        est = len(r["text"].split()) / WORDS_PER_SECOND
        mark = " TIGHT" if gap is not None and est > gap else ""
        near = "" if r["exact"] else "~ "
        gaps = f"{gap:6.1f}" if gap is not None else "     -"
        print(f"{r['n']:02d} {r['at']:8.2f} {gaps} {est:5.1f}  {r['text'][:30]:30}  {near}{r['heard'][:44]}{mark}")

def ffmpeg(exe, *args):
    r = subprocess.run([exe, "-y", "-hide_banner", *args], capture_output=True, text=True)
    if r.returncode != 0:
        sys.exit(f"ffmpeg failed: {r.stderr.strip().splitlines()[-1] if r.stderr.strip() else 'unknown'}")
    return r.stderr

def duration(path):
    exe = shutil.which("ffprobe")
    if not exe:
        sys.exit("ffprobe not found on PATH: install ffmpeg_8-headless with the packages tool")
    r = subprocess.run([exe, "-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", path],
                       capture_output=True, text=True)
    return float(r.stdout)

def prepare(exe, src, dst, scratch):
    """Trim a chunk's silent ends and bring it to the shared loudness."""
    trimmed = os.path.join(scratch, "trimmed.wav")
    ffmpeg(exe, "-i", src, "-ac", "1", "-ar", str(RATE), "-af", TRIM, trimmed)
    report = ffmpeg(exe, "-i", trimmed, "-af", "volumedetect", "-f", "null", "-")
    m = re.search(r"mean_volume: (-?[\d.]+) dB", report)
    gain = TARGET_DB - float(m.group(1)) if m else 0.0
    ffmpeg(exe, "-i", trimmed, "-af", f"volume={gain:.2f}dB,alimiter=limit=0.84:level=0",
           "-ac", "1", "-ar", str(RATE), "-c:a", "pcm_s16le", dst)
    with wave.open(dst, "rb") as w:
        return w.getnframes()

def silence(w, frames):
    empty = bytes(BLOCK)
    while frames > 0:
        n = min(frames * 2, BLOCK)
        w.writeframes(empty[:n])
        frames -= n // 2

def faded(pcm):
    """16-bit samples that fall away to nothing at the end."""
    samples = array.array("h", pcm)
    n = min(FADE, len(samples))
    for i in range(1, n + 1):
        samples[-i] = samples[-i] * (i - 1) // n
    return samples.tobytes()

def build(rows, video, folder, out):
    exe = shutil.which("ffmpeg")
    if not exe:
        sys.exit("ffmpeg not found on PATH: install it with the packages tool (ffmpeg_8-headless)")
    sources, missing = {}, []
    for r in rows:
        found = glob.glob(os.path.join(folder, f"{r['n']:02d}.mp3")) + glob.glob(os.path.join(folder, f"{r['n']:02d}.wav"))
        if found:
            sources[r["n"]] = found[0]
        else:
            missing.append(f"{r['n']:02d}")
    if missing:
        sys.exit(f"not spoken yet: {', '.join(missing)}")

    total = duration(video)
    with tempfile.TemporaryDirectory() as scratch:
        placed = []
        for r in rows:
            levelled = os.path.join(scratch, f"{r['n']:02d}.wav")
            frames = prepare(exe, sources[r["n"]], levelled, scratch)
            placed.append(dict(r, first=int(r["at"] * RATE), frames=frames, kept=frames, path=levelled))
        for p, after in zip(placed, placed[1:]):
            p["kept"] = min(p["frames"], after["first"] - p["first"])
            p["by"] = after["n"]
        end = placed[-1]["at"] + placed[-1]["frames"] / RATE
        if end > total:
            sys.exit(f"chunk {placed[-1]['n']:02d} ends at {end:.1f} s, after the video's {total:.1f} s: shorten it and speak only that chunk again")

        # Written as it goes: an hour of track is 300 MB, on a server with less than 2 GB.
        with wave.open(out, "wb") as w:
            w.setnchannels(1)
            w.setsampwidth(2)
            w.setframerate(RATE)
            written = 0
            for p in placed:
                silence(w, p["first"] - written)
                with wave.open(p["path"], "rb") as c:
                    pcm = c.readframes(p["kept"])
                w.writeframes(faded(pcm) if p["kept"] < p["frames"] else pcm)
                written = p["first"] + p["kept"]
            silence(w, int(total * RATE) - written)
    cut = 0
    for p in placed:
        note = ""
        if p["kept"] < p["frames"]:
            cut += 1
            note = f"  cut {(p['frames'] - p['kept']) / RATE:.1f} s short by chunk {p['by']:02d}"
        print(f"{p['n']:02d} {p['at']:8.2f} {p['frames'] / RATE:6.2f}{note}")
    spoken = sum(p["kept"] for p in placed) / RATE
    print(f"wrote {out}: {total:.1f} s, {spoken:.1f} s of speech, {len(placed)} chunks, {cut} cut off")

def main():
    ap = argparse.ArgumentParser(usage=__doc__)
    ap.add_argument("video", nargs="?")
    ap.add_argument("--check", action="store_true")
    ap.add_argument("--chunks", default="chunks.json")
    ap.add_argument("--dir", default="chunks")
    ap.add_argument("--out", default="voiceover_track.wav")
    a = ap.parse_args()
    if not a.check and not a.video:
        sys.exit("name the video, or pass --check")
    chunks = json.load(open(a.chunks, encoding="utf-8"))
    needs_words = any("at" not in c for c in chunks)
    if needs_words and not segment_files():
        sys.exit("segments.txt not found: save the transcript's lines there first")
    rows, errors = plan(chunks, load_words() if needs_words else [])
    if errors:
        sys.exit("\n".join(errors))
    if a.check:
        show_plan(rows)
        return
    build(rows, a.video, a.dir, a.out)

if __name__ == "__main__":
    main()
