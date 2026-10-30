#!/usr/bin/env python3
"""Extract a video's audio as mono 16 kHz WAV, ready for a transcription model.

Usage: extract_audio.py INPUT.mov [OUTPUT.wav]
Writes OUTPUT.wav (default: audio.wav, next to the input's name) in the current
working directory. Requires ffmpeg on PATH (packages tool, ffmpeg_8-headless).
"""
import os, shutil, subprocess, sys

def main():
    if len(sys.argv) < 2:
        sys.exit("usage: extract_audio.py INPUT.mov [OUTPUT.wav]")
    inp = sys.argv[1]
    out = sys.argv[2] if len(sys.argv) > 2 else "audio.wav"
    exe = shutil.which("ffmpeg")
    if not exe:
        sys.exit("ffmpeg not found on PATH: install it with the packages tool (ffmpeg_8-headless)")
    r = subprocess.run([exe, "-y", "-i", inp, "-vn", "-ac", "1", "-ar", "16000", out],
                       capture_output=True, text=True)
    if r.returncode != 0 or not os.path.exists(out):
        sys.exit(f"ffmpeg failed: {r.stderr.strip().splitlines()[-1] if r.stderr.strip() else 'unknown'}")
    print(f"wrote {out} (mono, 16 kHz, from {inp})")

if __name__ == "__main__":
    main()
