#!/usr/bin/env python3
"""Combine a video with a synthesized voiceover MP3, padding audio to the video's length.

Usage: vo_mux.py INPUT.mov VOICEOVER.mp3 OUTPUT.mp4 [DELAY_MS]
Video stream is copied, never re-encoded; audio is delayed DELAY_MS (default 500)
and padded with silence to the input's full duration. Do not use -shortest.
Requires ffmpeg on PATH (packages tool, ffmpeg_8-headless).
"""
import shutil, subprocess, sys

def main():
    if len(sys.argv) not in (4, 5):
        sys.exit("usage: vo_mux.py INPUT VOICEOVER OUTPUT [DELAY_MS]")
    inp, vo, out = sys.argv[1], sys.argv[2], sys.argv[3]
    delay = sys.argv[4] if len(sys.argv) == 5 else "500"
    exe = shutil.which("ffmpeg")
    if not exe:
        sys.exit("ffmpeg not found on PATH: install it with the packages tool (ffmpeg_8-headless)")

    probe = subprocess.run([exe, "-i", inp], capture_output=True, text=True)
    # ffmpeg exits 1 here because no output file was given; the info is in stderr anyway
    for line in probe.stderr.splitlines():
        if line.strip().startswith("Duration:"):
            h, m, rest = line.strip().split()[1].strip(",").split(":")
            dur = int(h) * 3600 + int(m) * 60 + float(rest)
            break
    else:
        sys.exit("could not read duration from input")

    r = subprocess.run([
        exe, "-y", "-i", inp, "-i", vo,
        "-map", "0:v", "-map", "1:a",
        "-c:v", "copy", "-c:a", "aac", "-b:a", "192k", "-ar", "44100",
        "-af", f"adelay={delay}|{delay},apad", "-t", f"{dur:.3f}", out,
    ], capture_output=True, text=True)
    if r.returncode != 0:
        sys.exit(f"ffmpeg failed: {r.stderr.strip().splitlines()[-1] if r.stderr.strip() else 'unknown'}")
    print(f"wrote {out} ({dur:.1f}s, voiceover delayed {delay}ms, video stream copied)")

if __name__ == "__main__":
    main()
