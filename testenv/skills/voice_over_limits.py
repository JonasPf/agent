"""The length checks: below, past the warning, past the limit, and with the operator's say-so."""
import array, json, os, re, resource, shutil, subprocess, sys, tempfile, wave

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

# a line still speaking when the next starts is cut off there, however long the overlap
os.makedirs("over/chunks")
os.chdir("over")


def tone(name, hz, seconds):
    sh("ffmpeg", "-y", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", f"sine=f={hz}:d={seconds}", "-ac", "1", name)


def place(*lines):
    json.dump([{"at": at, "text": "x"} for at in lines], open("chunks.json", "w"))
    return sh("python3", SK + "/place_chunks.py", "video.m4a")


def track():
    with wave.open("voiceover_track.wav", "rb") as w:
        return array.array("h", w.readframes(w.getnframes())), w.getframerate()


def pitch(pcm, rate, start, end):
    part = pcm[int(start * rate):int(end * rate)]
    crossings = sum(1 for a, b in zip(part, part[1:]) if a < 0 <= b)
    return crossings / (end - start)


audio("video.m4a", 8)
tone("chunks/01.wav", 440, 5)
tone("chunks/02.wav", 880, 2)
r = place(1, 3)
report("an overlap of 3 seconds is written, not refused", r.returncode == 0, r.stderr)
report("it says which line was cut, and by how much", "cut 3.0 s short by chunk 02" in r.stdout, r.stdout)
pcm, rate = track()
report("the first line plays until the second starts", abs(pitch(pcm, rate, 1.2, 2.9) - 440) < 15, pitch(pcm, rate, 1.2, 2.9))
report("the second starts where it was put", abs(pitch(pcm, rate, 3.02, 4.9) - 880) < 15, pitch(pcm, rate, 3.02, 4.9))
report("none of the first plays after that", max(map(abs, pcm[int(5.1 * rate):])) == 0)
report("the cut makes no click", abs(pcm[int(3 * rate) - 1]) < 300, pcm[int(3 * rate) - 1])
report("the track is as long as the video", abs(len(pcm) / rate - 8) < 0.05, len(pcm) / rate)
r = place(1, 1)
report("a line put on top of another replaces it, and says so", r.returncode == 0 and "cut 5.0 s short by chunk 02" in r.stdout, (r.stdout, r.stderr))
pcm, rate = track()
report("and only the later line plays", abs(pitch(pcm, rate, 1.02, 2.9) - 880) < 15, pitch(pcm, rate, 1.02, 2.9))
r = place(1, 7)
report("the video's end still refuses a line that runs past it", r.returncode == 1 and "after the video" in r.stderr, (r.stdout, r.stderr))
r = place(0.5, 5.8)
report("lines that do not meet are left alone", r.returncode == 0 and "short by" not in r.stdout and "0 cut off" in r.stdout, (r.stdout, r.stderr))
os.chdir(W)

print(len(bad), "problems", bad)
os.chdir(ROOT)
shutil.rmtree(W)
sys.exit(1 if bad else 0)
