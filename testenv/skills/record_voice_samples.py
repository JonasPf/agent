"""Record the line each voice says on the voice-over review page, into the skill's samples.json.

Usage: OPENROUTER_API_KEY=... python3 testenv/skills/record_voice_samples.py

Run it when references/voices.md changes. It buys speech: about 2 cents for all 31 voices. Each
sample is levelled the way place_chunks.py levels a line, so voices compare at one loudness, and is
kept as base64, because a skill's folder holds text only: an .mp3 there would stop it loading.
"""
import base64, json, os, shutil, subprocess, sys, tempfile, urllib.error, urllib.request

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
SK = ROOT + "/skills/voice-over/scripts"
sys.path.insert(0, SK)
# A skill's folder holds text only: one .pyc beside the scripts and the skill will not load.
sys.dont_write_bytecode = True
import make_review, place_chunks

GATEWAY = "https://openrouter.ai/api/v1/audio/speech"
LINE = "Hello World 1 2 3"


def speak(key, model, voice, form):
    body = json.dumps(dict(model=model, input=LINE, voice=voice, response_format=form)).encode()
    req = urllib.request.Request(GATEWAY, body, {"Authorization": "Bearer " + key, "Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=60) as r:
        return r.read(), r.headers.get("Content-Type", "")


def fetch(key, model, voice, folder):
    """The voice's audio as a file: MP3 if it gives one, else raw samples, as call_model takes them."""
    try:
        audio, _ = speak(key, model, voice, "mp3")
        path = os.path.join(folder, "raw.mp3")
        open(path, "wb").write(audio)
        return path
    except urllib.error.HTTPError as e:
        if "pcm" not in e.read().decode(errors="replace"):
            raise
    audio, kind = speak(key, model, voice, "pcm")
    rate = kind.split("rate=")[1].split(";")[0] if "rate=" in kind else "24000"
    path = os.path.join(folder, "raw.wav")
    subprocess.run(["ffmpeg", "-y", "-hide_banner", "-loglevel", "error", "-f", "s16le", "-ar", rate, "-ac", "1", "-i", "-", path],
                   input=audio, check=True)
    return path


def main():
    key = os.environ.get("OPENROUTER_API_KEY")
    if not key:
        sys.exit("set OPENROUTER_API_KEY")
    exe = shutil.which("ffmpeg")
    samples = {}
    with tempfile.TemporaryDirectory() as folder:
        for option in make_review.voices():
            samples[option["model"]] = {}
            for voice in option["voices"]:
                raw = fetch(key, option["model"], voice, folder)
                level = os.path.join(folder, "level.wav")
                place_chunks.prepare(exe, raw, level, folder)
                mp3 = subprocess.run(["ffmpeg", "-hide_banner", "-loglevel", "error", "-i", level, "-ac", "1", "-ar", "24000",
                                      "-c:a", "libmp3lame", "-b:a", "48k", "-id3v2_version", "0", "-f", "mp3", "-"],
                                     capture_output=True, check=True).stdout
                samples[option["model"]][voice] = base64.b64encode(mp3).decode()
                print(f"{voice:10} {len(mp3) / 1024:5.1f} KB")
    with open(make_review.SAMPLES, "w", encoding="utf-8") as f:
        json.dump(samples, f, indent=0, sort_keys=True)
    print(f"{make_review.SAMPLES}: {os.path.getsize(make_review.SAMPLES) / 1e3:.0f} KB")


if __name__ == "__main__":
    main()
