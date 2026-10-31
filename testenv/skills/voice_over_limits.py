"""The length checks: below, past the warning, past the limit, and with the operator's say-so."""
import json, os, re, resource, shutil, subprocess, sys, tempfile

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
SK = ROOT + "/skills/voice-over/scripts"
W = tempfile.mkdtemp(prefix="voice-over-limits-")
os.makedirs(W + "/chunks")
os.chdir(W)
bad = []


def sh(*a):
    return subprocess.run(a, capture_output=True, text=True)


def report(label, good, detail=""):
    print(("ok  " if good else "BAD ") + label + ("" if good else "  " + str(detail)[:300]))
    if not good:
        bad.append(label)


def audio(name, seconds):
    sh("ffmpeg", "-y", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", f"sine=f=220:d={seconds}", "-ac", "1", "-c:a", "aac", "-b:a", "16k", name)


def expect(label, r, code, want):
    text = r.stdout + r.stderr
    report(label, r.returncode == code and want in text, (r.returncode, text))


for mins, flag, code, want in ((29, [], 0, "wrote "), (31, [], 0, "long: tell the operator"), (61, [], 1, "too long to start"), (61, ["--long"], 0, "cut into 2 parts")):
    name = f"m{mins}.m4a"
    if not os.path.exists(name):
        audio(name, mins * 60)
    for f in os.listdir("."):
        if f.endswith(".mp3"):
            os.remove(f)
    expect(f"{mins} minutes {' '.join(flag)}", sh("python3", SK + "/extract_audio.py", name, *flag), code, want)
    if mins == 61 and not flag:
        report("too long writes nothing", not any(f.endswith(".mp3") for f in os.listdir(".")))

# a chunk too long for one speech call is refused before anything is paid for
open("segments.txt", "w", encoding="utf-8").write("[0.00–2.00] one two three four\n[3000.00–3002.00] five six seven eight\n[3500.00–3502.00] nine ten eleven twelve\n")
json.dump([{"starts_with": "one two three", "text": "x " * 3000}], open("chunks.json", "w"))
expect("a chunk over the limit", sh("python3", SK + "/place_chunks.py", "--check"), 1, "split it")

# more chunks than a turn has rounds
json.dump([{"at": i * 10, "text": "hello there"} for i in range(120)], open("chunks.json", "w"))
expect("120 chunks warn", sh("python3", SK + "/estimate_cost.py"), 0, "more than one turn")
json.dump([{"at": i * 10, "text": "hello there"} for i in range(40)], open("chunks.json", "w"))
report("40 chunks do not warn", "Warning" not in sh("python3", SK + "/estimate_cost.py").stdout)

# an hour of track must not sit in memory, on a server with less than 2 GB
audio("a.wav", 2)
for i in (1, 2, 3):
    shutil.copy("a.wav", f"chunks/{i:02d}.wav")
json.dump([{"starts_with": "one two three", "text": "a"}, {"starts_with": "five six seven", "text": "b"}, {"starts_with": "nine ten eleven", "text": "c"}], open("chunks.json", "w"))
r = sh("python3", SK + "/place_chunks.py", "m61.m4a")
report("a 61-minute track is written", r.returncode == 0, r.stderr)
scale = 1 if sys.platform != "darwin" else 1 / 1024
peak_mb = resource.getrusage(resource.RUSAGE_CHILDREN).ru_maxrss * scale / 1024
report("it never holds the track in memory", os.path.getsize("voiceover_track.wav") > 300e6 and peak_mb < 300, (os.path.getsize("voiceover_track.wav"), peak_mb))
sd = sh("ffmpeg", "-hide_banner", "-i", "voiceover_track.wav", "-af", "silencedetect=noise=-40dB:d=2", "-f", "null", "-").stderr
ends = [round(float(x)) for x in re.findall(r"silence_end: ([\d.]+)", sd)]
report("each chunk is where its words are", ends[:2] == [3000, 3500] or ends[:3] == [0, 3000, 3500], ends)

print(len(bad), "problems", bad)
os.chdir(ROOT)
shutil.rmtree(W)
sys.exit(1 if bad else 0)
