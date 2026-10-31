#!/usr/bin/env python3
"""Extract a video's audio as mono 16 kHz MP3, ready for a transcription model.

Usage: extract_audio.py INPUT.mov [OUTPUT.mp3] [--long]
Writes OUTPUT.mp3 (default: audio.mp3) in the current working directory. 64 kbps
fits about 40 minutes in the 20 MB a call_model call carries; a longer recording
is also cut into audio-000.mp3, audio-001.mp3, ... of 40 minutes each.

Says how long the recording is first. Past WARN_MINUTES it warns, and past
MAX_MINUTES it stops before doing anything unless --long says the operator agreed.
Requires ffmpeg on PATH (packages tool, ffmpeg_8-headless).
"""
import os, shutil, subprocess, sys

MAX_BYTES = 20 << 20
PART_SECONDS = 2400
WARN_MINUTES = 30
MAX_MINUTES = 60
# measured on one demo: 27 chunks of about 220 characters in ten minutes
CHUNKS_PER_MINUTE = 2.7
CHARS_PER_MINUTE = 600

def run(exe, *args):
    r = subprocess.run([exe, "-y", *args], capture_output=True, text=True)
    if r.returncode != 0:
        sys.exit(f"ffmpeg failed: {r.stderr.strip().splitlines()[-1] if r.stderr.strip() else 'unknown'}")

def minutes(path):
    exe = shutil.which("ffprobe")
    if not exe:
        sys.exit("ffprobe not found on PATH: install ffmpeg_8-headless with the packages tool")
    r = subprocess.run([exe, "-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", path],
                       capture_output=True, text=True)
    try:
        return float(r.stdout) / 60
    except ValueError:
        sys.exit(f"could not read the length of {path}")

def check_length(mins, long_ok):
    """Say what a recording of this length means, before any of it is paid for."""
    chunks = round(mins * CHUNKS_PER_MINUTE)
    chars = mins * CHARS_PER_MINUTE
    size = f"{mins:.0f} minutes, roughly {chunks} chunks and {chars:,.0f} characters of speech"
    print(f"length: {size}")
    if mins > MAX_MINUTES and not long_ok:
        sys.exit(f"too long to start without asking: {size}. That is ${chars * 4 / 1e6:.2f} to ${chars * 40 / 1e6:.2f} of speech, "
                 f"more than one turn of rounds, and a lot of agent time before the first word is spoken. "
                 f"Tell the operator; if they agree, run this again with --long.")
    if mins > WARN_MINUTES:
        print(f"long: tell the operator now. Over {WARN_MINUTES} minutes a turn's hundred rounds may run out "
              f"if chunks are spoken one at a time, and they would need to send continue.")

def main():
    args = [a for a in sys.argv[1:] if a != "--long"]
    if not args:
        sys.exit("usage: extract_audio.py INPUT.mov [OUTPUT.mp3] [--long]")
    inp = args[0]
    out = args[1] if len(args) > 1 else "audio.mp3"
    exe = shutil.which("ffmpeg")
    if not exe:
        sys.exit("ffmpeg not found on PATH: install it with the packages tool (ffmpeg_8-headless)")
    check_length(minutes(inp), "--long" in sys.argv)
    run(exe, "-i", inp, "-vn", "-ac", "1", "-ar", "16000", "-b:a", "64k", out)
    if not os.path.exists(out):
        sys.exit("ffmpeg wrote nothing")
    size = os.path.getsize(out)
    print(f"wrote {out} ({size / (1 << 20):.1f} MB, mono, 16 kHz, from {inp})")
    if size > MAX_BYTES:
        stem = os.path.splitext(out)[0]
        run(exe, "-i", out, "-f", "segment", "-segment_time", str(PART_SECONDS), "-c", "copy", f"{stem}-%03d.mp3")
        parts = sorted(f for f in os.listdir(".") if f.startswith(stem + "-") and f.endswith(".mp3"))
        print(f"over 20 MB: cut into {len(parts)} parts of {PART_SECONDS // 60} minutes: {', '.join(parts)}")

if __name__ == "__main__":
    main()
