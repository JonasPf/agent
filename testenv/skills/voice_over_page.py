"""Drive make_review.py as the agent would, on videos whose every 30 seconds has its own colour."""
import base64, glob, json, os, re, shutil, subprocess, sys, tempfile

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
SK = ROOT + "/skills/voice-over/scripts"
W = tempfile.mkdtemp(prefix="voice-over-page-")
os.chdir(W)
bad = []
COLOURS = ["red", "green", "blue", "yellow"]
RGB = {"red": (255, 0, 0), "green": (0, 128, 0), "blue": (0, 0, 255), "yellow": (255, 255, 0)}


def sh(*a):
    return subprocess.run(a, capture_output=True, text=True)


def report(label, good, detail=""):
    print(("ok  " if good else "BAD ") + label + ("" if good else "  " + str(detail)[:300]))
    if not good:
        bad.append(label)


def ff(*a):
    r = sh("ffmpeg", "-y", "-hide_banner", "-loglevel", "error", *a)
    if r.returncode:
        sys.exit("setup failed: " + r.stderr[-300:])


def make(*args):
    return sh("python3", SK + "/make_review.py", *args)


def page_data():
    html = open("review/review.html", encoding="utf-8").read()
    m = re.search(r'<script id="data" type="application/json">(.*?)</script>', html, re.S)
    return html, json.loads(m.group(1))


def colour_of(shot):
    head, body = shot.split(",", 1)
    open("shot.jpg", "wb").write(base64.b64decode(body))
    out = subprocess.run(["ffmpeg", "-v", "error", "-i", "shot.jpg", "-vf", "scale=1:1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-"],
                         capture_output=True).stdout
    return tuple(out[:3])


def near(a, b):
    return all(abs(x - y) < 40 for x, y in zip(a, b))


# a 100-second video: a new colour every 30 seconds, and keyframes far apart, as in a real recording
parts = [f"color=c={c}:s=320x240:r=10:d=30" for c in COLOURS[:3]] + ["color=c=yellow:s=320x240:r=10:d=10"]
ff(*sum((["-f", "lavfi", "-i", p] for p in parts), []), "-filter_complex", "concat=n=4:v=1:a=0", "-g", "400", "-c:v", "libx264", "-pix_fmt", "yuv420p", "video.mp4")
open("segments.txt", "w", encoding="utf-8").write(
    "[2.00–5.00] okay so this is the opening\n"
    "[33.00–36.00] and here is the middle part\n"
    "[62.50–66.00] finally we close the whole thing\n")
hostile = 'It says </script><img src=x onerror="parent.postMessage(1,\'*\')"> & more.'
json.dump([{"starts_with": "okay so this is", "text": "This is the opening."},
           {"starts_with": "and here is the", "text": hostile},
           {"text": "A line placed by hand.", "at": 80.25}], open("chunks.json", "w"))
r = make("video.mp4")
report("builds a page from a draft", r.returncode == 0 and os.path.exists("review/review.html"), r.stderr)
html, data = page_data()
report("one screenshot every 30 seconds", len(data["shots"]) == 4 and data["step"] == 30, len(data["shots"]))
report("each screenshot is the frame at its time, not the nearest keyframe",
       all(near(colour_of(s), RGB[c]) for s, c in zip(data["shots"], COLOURS)), [colour_of(s) for s in data["shots"]])
report("the page knows the video's length", abs(data["duration"] - 100) < 0.5, data["duration"])
report("each line starts where the narrator said it", [round(l["at"], 1) for l in data["lines"]] == [2.0, 33.0, 80.2] or
       [round(l["at"]) for l in data["lines"]] == [2, 33, 80], data["lines"])
report("draft words arrive unchanged", data["lines"][1]["text"] == hostile, data["lines"][1])
report("transcript text cannot close the data block", hostile not in html and "</script><img" not in html.split('id="data"')[1].split("</script>")[0])
report("the page holds no outside link", not re.search(r"https?://", html.split('id="data"')[0].split("<script")[0]), re.findall(r"https?://\S+", html)[:3])
report("both models and their voices are offered, from voices.md", [v["model"] for v in data["voices"]] ==
       ["heygen/heygen-voice-1-turbo", "elevenlabs/eleven-flash-v2.5"] and "gareth" in data["voices"][0]["voices"] and len(data["voices"][1]["voices"]) == 21, data["voices"])
report("every voice has a price", all(v["price"] > 0 for v in data["voices"]))
report("the page starts on the cheap voice", data["voice"] == "gareth" and data["model"].startswith("heygen"), (data["model"], data["voice"]))
report("only languages tried on both voices", [l[0] for l in data["languages"]] == ["de", "en", "es", "fr", "it", "nl", "pt"] or
       len(data["languages"]) == 7, data["languages"])
report("the page uses the placement script's own limits", data["pace"] == 2.3 and data["max_chars"] == 4500, data)
heard, unheard = [], []
for group in data["voices"]:
    for voice in group["voices"]:
        sample = data["samples"].get(group["model"], {}).get(voice)
        if not sample:
            unheard.append(voice)
            continue
        open("sample.mp3", "wb").write(base64.b64decode(sample))
        probe = sh("ffprobe", "-v", "error", "-show_entries", "stream=codec_name:format=duration", "-of", "default=nw=1:nk=1", "sample.mp3").stdout.split()
        if probe[:1] != ["mp3"] or not 0.8 < float(probe[1]) < 6:
            unheard.append((voice, probe))
        heard.append(sample)
report("every voice has its own sample, playable by a browser", not unheard and len(set(heard)) == 31, unheard)
report("the samples are text, as everything in a skill must be", open(SK + "/../assets/samples.json", "rb").read().isascii())
report("ask.answer and ask.hold are what it calls", "ask.answer(" in html and "ask.hold(" in html and "ask.save(" not in html)

# a skill holds text only, and one .pyc beside the scripts would stop the whole skill loading
# Apple's python keeps bytecode in a cache folder; the server's writes it beside the scripts, as this does.
as_on_the_server = f"""
import runpy, sys
sys.pycache_prefix = None
sys.path.insert(0, {SK!r})
sys.argv = ['make_review.py', 'video.mp4']
runpy.run_path({SK!r} + '/make_review.py', run_name='__main__')
"""
env = {k: v for k, v in os.environ.items() if k != "PYTHONDONTWRITEBYTECODE"}
shutil.rmtree(SK + "/__pycache__", ignore_errors=True)
subprocess.run(["python3", "-c", as_on_the_server], env=env, capture_output=True)
report("running it leaves nothing but text in the skill", not os.path.exists(SK + "/__pycache__"), os.listdir(SK))
shutil.rmtree(SK + "/__pycache__", ignore_errors=True)

# --voice starts the page on a voice the request named
r = make("video.mp4", "--voice", "brian")
_, data = page_data()
report("--voice picks its model too", r.returncode == 0 and data["voice"] == "brian" and data["model"].startswith("elevenlabs"), (r.stderr, data["voice"]))
r = make("video.mp4", "--voice", "nobody")
report("an unknown voice is refused, naming it", r.returncode != 0 and "nobody" in r.stderr, r.stderr)

# a bad draft stops with what place_chunks would say
good = open("chunks.json").read()
json.dump([{"starts_with": "this was never said", "text": "x"}], open("chunks.json", "w"))
r = make("video.mp4")
report("a chunk not in the transcript stops the build", r.returncode != 0 and "not in the transcript" in r.stderr, r.stderr)
json.dump([{"text": "After the end.", "at": 500}], open("chunks.json", "w"))
r = make("video.mp4")
report("a chunk past the end of the video stops the build", r.returncode != 0 and "after the video" in r.stderr, r.stderr)
open("chunks.json", "w").write(good)

# a silent video has no draft: the operator writes the script on the page
os.rename("chunks.json", "draft.keep")
r = make("video.mp4")
_, data = page_data()
report("no chunks.json gives an empty page", r.returncode == 0 and data["lines"] == [] and len(data["shots"]) == 4, r.stderr)
os.rename("draft.keep", "chunks.json")

# nothing else is left behind
report("only the page is written", sorted(os.listdir("review")) == ["review.html"], os.listdir("review"))

# a building page never reuses an old approval
make("video.mp4")
json.dump({"language": "", "model": "elevenlabs/eleven-flash-v2.5", "voice": "alice", "chunks": [{"at": 1, "text": "Old."}]},
          open("review/approved.json", "w"))
r = make("video.mp4")
_, data = page_data()
report("a rebuilt page keeps the chosen voice", data["voice"] == "alice" and data["model"].startswith("elevenlabs"), data["voice"])
report("a rebuilt page clears the old approval", not os.path.exists("review/approved.json"))

# applying an approval
def approve(**over):
    body = {"language": "", "model": "heygen/heygen-voice-1-turbo", "voice": "gareth",
            "chunks": [{"at": 1.5, "text": "First line."}, {"at": 40, "text": "Second line."}]}
    body.update(over)
    json.dump(body, open("review/approved.json", "w"))


r = make("--apply")
report("nothing approved is refused", r.returncode != 0 and "not approved" in r.stderr.lower(), r.stderr)
open("review/approved.json", "w").write("{}")
r = make("--apply")
report("an approval taken back is refused", r.returncode != 0 and "not approved" in r.stderr.lower(), r.stderr)

before = open("chunks.json").read()
for label, over, want in (("no lines", {"chunks": []}, "no lines"),
                          ("an unknown model", {"model": "x/y"}, "x/y"),
                          ("a voice of the other model", {"voice": "brian"}, "brian"),
                          ("lines out of order", {"chunks": [{"at": 9, "text": "b"}, {"at": 1, "text": "a"}]}, "before the chunk")):
    approve(**over)
    r = make("--apply")
    report(f"{label} is refused and chunks.json stays", r.returncode != 0 and want in r.stderr and open("chunks.json").read() == before, r.stderr)

def to_speak(out):
    """The lines --apply says to speak: each output path, then the words exactly."""
    return re.findall(r"^(chunks/\d+\.mp3)  (.*)$", out, re.M)


def spoken(n, what):
    open(f"chunks/{n:02d}.wav", "w").write(what)


approve(chunks=[{"at": 1.5, "text": "First line."}, {"at": 40, "text": "Second\nline."}])
os.makedirs("chunks")
open("chunks/01.mp3", "w").write("old speech")
r = make("--apply")
chunks = json.load(open("chunks.json"))
report("an approval replaces chunks.json, every line timed", r.returncode == 0 and chunks == [{"at": 1.5, "text": "First line."}, {"at": 40, "text": "Second line."}], r.stderr)
report("it names the model and voice to speak with", "heygen/heygen-voice-1-turbo" in r.stdout and "gareth" in r.stdout, r.stdout)
report("it states the cost", re.search(r"\$\d", r.stdout), r.stdout)
report("it names every line to speak, with its words as approved",
       to_speak(r.stdout) == [("chunks/01.mp3", "First line."), ("chunks/02.mp3", "Second line.")], r.stdout)
# chunks/ now also holds a note of the voice, so the folder stays; what matters is the speech in it
report("old speech cannot be placed under new lines", not os.path.exists("chunks/01.mp3") and os.path.exists("chunks.old/01.mp3"), os.listdir("."))
c = sh("python3", SK + "/place_chunks.py", "--check")
report("place_chunks accepts the result", c.returncode == 0 and "First line" in c.stdout, c.stderr)

# approved again after the speech: only what changed is spoken again
spoken(1, "first")
spoken(2, "second")
approve(chunks=[{"at": 3, "text": "First line."}, {"at": 40, "text": "Second line, changed."}])
r = make("--apply")
report("a line only moved keeps its speech", r.returncode == 0 and open("chunks/01.wav").read() == "first", (r.stderr, os.listdir("chunks")))
report("a line whose words changed is spoken again, and only it",
       not os.path.exists("chunks/02.wav") and to_speak(r.stdout) == [("chunks/02.mp3", "Second line, changed.")], r.stdout)

spoken(2, "second, changed")
approve(chunks=[{"at": 0.5, "text": "A new opening."}, {"at": 3, "text": "First line."}, {"at": 40, "text": "Second line, changed."}])
r = make("--apply")
report("a line added moves the others' speech along with them",
       open("chunks/02.wav").read() == "first" and open("chunks/03.wav").read() == "second, changed" and not os.path.exists("chunks/01.wav"),
       sorted(os.listdir("chunks")))
report("only the new line is to be spoken", to_speak(r.stdout) == [("chunks/01.mp3", "A new opening.")], r.stdout)

spoken(1, "opening")
approve(chunks=[{"at": 0.5, "text": "A new opening."}, {"at": 3, "text": "First line."}, {"at": 40, "text": "Second line, changed."}])
r = make("--apply")
report("an approval that changes nothing speaks nothing", r.returncode == 0 and to_speak(r.stdout) == [] and "already spoken" in r.stdout and
       len(glob.glob("chunks/0*.wav")) == 3, r.stdout)

approve(voice="meredith", chunks=[{"at": 0.5, "text": "A new opening."}, {"at": 3, "text": "First line."}, {"at": 40, "text": "Second line, changed."}])
r = make("--apply")
report("a new voice speaks every line again", not glob.glob("chunks/0*.wav") and [p for p, _ in to_speak(r.stdout)] == ["chunks/01.mp3", "chunks/02.mp3", "chunks/03.mp3"] and
       "meredith" in r.stdout, (r.stdout, os.listdir("chunks")))

def voice(path, seconds):
    """Speech as a voice hands it back: a tone, with a little silence before and after."""
    ff("-f", "lavfi", "-i", "anullsrc=r=24000:cl=mono:d=0.4", "-f", "lavfi", "-i", f"sine=f=220:r=24000:d={seconds}",
       "-f", "lavfi", "-i", "anullsrc=r=24000:cl=mono:d=0.6", "-filter_complex", "concat=n=3:v=0:a=1", path)


def heard(b64):
    open("heard.mp3", "wb").write(base64.b64decode(b64))
    probe = sh("ffprobe", "-v", "error", "-show_entries", "stream=codec_name:format=duration", "-of", "default=nw=1:nk=1", "heard.mp3").stdout.split()
    return probe[0], float(probe[1])


LENGTHS = (2.0, 3.5, 5.0)
for n, s in enumerate(LENGTHS, 1):
    voice(f"chunks/{n:02d}.wav", s)
os.remove("review/approved.json")
r = make("video.mp4")
_, data = page_data()
report("the page shown again after the speech starts from what was spoken, in its voice",
       r.returncode == 0 and data["voice"] == "meredith" and [l["text"] for l in data["lines"]] == ["A new opening.", "First line.", "Second line, changed."],
       (r.stderr, data["voice"], data["lines"]))
report("it knows the voice the speech is in", data.get("spoken") == {"model": "heygen/heygen-voice-1-turbo", "voice": "meredith"}, data.get("spoken"))
report("each spoken line is as long as it will be placed, without the silence at its ends",
       all(abs(l.get("length", 0) - s) < 0.05 for l, s in zip(data["lines"], LENGTHS)), [l.get("length") for l in data["lines"]])
sounds = [heard(l["audio"]) for l in data["lines"] if l.get("audio")]
report("and carries that speech, for the page to play", len(sounds) == 3 and all(c == "mp3" and abs(d - s) < 0.15 for (c, d), s in zip(sounds, LENGTHS)), sounds)

os.rename("chunks/03.wav", "three.wav")
make("video.mp4")
_, data = page_data()
report("a line not spoken yet has no length of its own", "length" not in data["lines"][2] and "audio" not in data["lines"][2] and "length" in data["lines"][1],
       [sorted(l) for l in data["lines"]])
os.rename("three.wav", "chunks/03.wav")
open("chunks/02.wav", "w").write("not sound")
r = make("video.mp4")
report("speech that cannot be read stops the page, naming it", r.returncode != 0 and "02" in r.stderr, r.stderr)
voice("chunks/02.wav", 3.5)
os.rename("chunks/voice.json", "voice.keep")
r = make("video.mp4")
_, data = page_data()
report("speech in no known voice is not offered as spoken", r.returncode == 0 and data.get("spoken") is None and not any("audio" in l for l in data["lines"]),
       (r.stderr, data.get("spoken")))
os.rename("voice.keep", "chunks/voice.json")

approve(language="de", voice="meredith", chunks=[{"at": 0.5, "text": "A new opening."}, {"at": 3, "text": "First line."}, {"at": 40, "text": "Second line, changed."}])
r = make("--apply")
report("a new language asks for a translation, and then speech of every line", r.returncode == 0 and "German" in r.stdout and "--speak-all" in r.stdout and
       to_speak(r.stdout) == [], r.stdout)
report("a new language keeps none of the old speech", not glob.glob("chunks/0*.wav"), os.listdir("chunks"))

# the draft is spoken whole, before the page: --speak-all lists every chunk and notes the voice
json.dump([{"starts_with": "okay so this is", "text": "This is the opening."},
           {"starts_with": "and here is the", "text": "Here is the  middle."},
           {"text": "A line placed by hand.", "at": 80.25}], open("chunks.json", "w"))
r = make("--speak-all")
report("--speak-all lists every chunk with its words",
       r.returncode == 0 and to_speak(r.stdout) == [("chunks/01.mp3", "This is the opening."), ("chunks/02.mp3", "Here is the middle."),
                                                    ("chunks/03.mp3", "A line placed by hand.")], (r.stdout, r.stderr))
report("in the voice spoken last, and says what it costs", "meredith" in r.stdout and re.search(r"\$\d", r.stdout), r.stdout)
voice("chunks/01.wav", 1.0)
r = make("--speak-all", "--voice", "brian")
report("--voice names the voice, and so its model", r.returncode == 0 and "brian" in r.stdout and
       json.load(open("chunks/voice.json")) == {"model": "elevenlabs/eleven-flash-v2.5", "voice": "brian"}, (r.stdout, r.stderr))
report("speech from before is moved aside, so none of it lands under the draft", not glob.glob("chunks/0*") and os.path.exists("chunks.old/01.wav"),
       os.listdir("chunks"))
r = make("--speak-all", "--voice", "nobody")
report("an unknown voice is refused there too", r.returncode != 0 and "nobody" in r.stderr, r.stderr)
shutil.rmtree("chunks")
os.remove("review/approved.json")
r = make("--speak-all")
report("with no voice named or spoken, the draft is spoken in gareth", r.returncode == 0 and "gareth" in r.stdout and
       json.load(open("chunks/voice.json"))["voice"] == "gareth", (r.stdout, r.stderr))
os.rename("chunks.json", "draft.keep")
r = make("--speak-all")
report("--speak-all with no script says so", r.returncode != 0 and "chunks.json" in r.stderr, r.stderr)
os.rename("draft.keep", "chunks.json")

for n, s in enumerate((2.0, 2.5, 3.0), 1):
    voice(f"chunks/{n:02d}.wav", s)
r = make("video.mp4")
_, data = page_data()
report("the draft's page has every line spoken, each at its narrator's moment",
       r.returncode == 0 and [round(l["at"]) for l in data["lines"]] == [2, 33, 80] and all("audio" in l for l in data["lines"]), (r.stderr, data["lines"]))
approve(chunks=[{"at": l["at"] + 1, "text": l["text"]} for l in data["lines"]])
r = make("--apply")
report("that draft approved with its lines only moved needs no more speech", r.returncode == 0 and to_speak(r.stdout) == [] and "already spoken" in r.stdout,
       (r.stdout, r.stderr))

# a page never goes over what ask_operator will load
sys.path.insert(0, SK)
ff("-f", "lavfi", "-i", "nullsrc=s=1600x1200:r=5:d=95", "-vf", "noise=alls=100:allf=t+u,format=yuv420p", "-c:v", "libx264", "-preset", "ultrafast", "-crf", "30", "noisy.mp4")
os.remove("chunks.json")
# The samples and the page itself are always there: what shrinks is the screenshots.
limit = os.path.getsize(SK + "/../assets/samples.json") + os.path.getsize(SK + "/../assets/review.html") + 400_000
r = sh("python3", "-B", "-c", f"""
import sys; sys.path.insert(0, {SK!r}); sys.argv = ['make_review.py', 'noisy.mp4']
import make_review
make_review.MAX_PAGE = {limit}
make_review.main()
""")
report("a page too large for ask_operator comes out smaller", r.returncode == 0 and os.path.getsize("review/review.html") <= limit, (r.stderr, os.path.getsize("review/review.html"), limit))
_, data = page_data()
report("it still has every screenshot", len(data["shots"]) == 4, len(data["shots"]))

# an hour and more stays a page, by spacing the screenshots out
ff("-f", "lavfi", "-i", "color=c=gray:s=64x48:r=1:d=7500", "-c:v", "libx264", "-preset", "ultrafast", "-g", "60", "long.mp4")
r = make("long.mp4")
_, data = page_data()
report("a two-hour video has at most 120 screenshots", r.returncode == 0 and len(data["shots"]) <= 125 and data["step"] % 30 == 0 and data["step"] > 30, (r.stderr, len(data["shots"]), data["step"]))

# an hour of speech still makes a page, by speaking it in a smaller file
json.dump([{"at": i * 120.0, "text": f"Line {i}."} for i in range(60)], open("chunks.json", "w"))
shutil.rmtree("chunks", ignore_errors=True)
os.makedirs("chunks")
json.dump({"model": "heygen/heygen-voice-1-turbo", "voice": "gareth"}, open("chunks/voice.json", "w"))
for n in range(1, 61):
    voice(f"chunks/{n:02d}.wav", 60)
r = make("long.mp4")
_, data = page_data()
size = os.path.getsize("review/review.html")
report("an hour of speech fits in what ask_operator will load, every line playable",
       r.returncode == 0 and size <= 15 << 20 and all("audio" in l for l in data["lines"]) and len(data["lines"]) == 60, (r.stderr, size))

print(len(bad), "problems", bad)
os.chdir(ROOT)
shutil.rmtree(W)
sys.exit(1 if bad else 0)
