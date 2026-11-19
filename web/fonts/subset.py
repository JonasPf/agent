# Makes web/fonts/agent-serif-*.woff2, the serif the agent's replies are set in
# (webui.html, "Look"; ADR-093), from Charis SIL 6.101 by SIL International:
# https://github.com/google/fonts/tree/main/ofl/charissil (upstream
# https://github.com/silnrsi/font-charis). Charis SIL is under the SIL Open
# Font License with the Reserved Font Names "Charis" and "SIL"; a subset is a
# modified version, so it is renamed Agent Serif and ships with OFL.txt.
#
# Not shipped (.dockerignore). To run it again:
#   python3 -m venv /tmp/fonts && /tmp/fonts/bin/pip install fonttools brotli
#   curl -LO .../CharisSIL-{Regular,Italic,Bold}.ttf into <dir>
#   /tmp/fonts/bin/python -I web/fonts/subset.py <dir> web/fonts
import sys
from fontTools import subset
from fontTools.ttLib import TTFont

SRC, OUT = sys.argv[1], sys.argv[2]
UNICODES = ("U+0000-017F,U+0192,U+0218-021B,U+02BB-02BC,U+02C6-02C7,U+02D8-02DD,U+0300-0308,U+0329,"
            "U+1E9E,U+2000-206F,U+2070-209F,U+20AC,U+2116,U+2122,U+2190-2195,U+2212,U+2215,U+2248,"
            "U+2260,U+2264-2265,U+25CF,U+FEFF,U+FFFD")
KEEP = {0, 7, 8, 9, 10, 11, 12, 13, 14}  # copyright, trademark, vendor, designer, description, URLs, licence

for style in ("Regular", "Italic", "Bold"):
    font = TTFont(f"{SRC}/CharisSIL-{style}.ttf")
    opts = subset.Options()
    opts.flavor = "woff2"
    opts.layout_features = subset.Options().layout_features + ["onum", "pnum", "tnum", "lnum"]
    opts.name_IDs = ["*"]
    opts.name_languages = ["*"]
    opts.hinting = False
    opts.notdef_outline = True
    sub = subset.Subsetter(options=opts)
    sub.populate(unicodes=subset.parse_unicodes(UNICODES))
    sub.subset(font)
    names = {1: "Agent Serif", 2: style, 3: f"AgentSerif-{style}",
             4: "Agent Serif" if style == "Regular" else f"Agent Serif {style}",
             6: f"AgentSerif-{style}", 16: "Agent Serif", 17: style}
    table = font["name"]
    for rec in list(table.names):
        if rec.nameID in names:
            rec.string = names[rec.nameID]
        elif rec.nameID not in KEEP and ("Charis" in rec.toUnicode() or "SIL" in rec.toUnicode()):
            table.removeNames(nameID=rec.nameID)
    if "CFF " in font:
        raise SystemExit("unexpected CFF outlines")
    font.save(f"{OUT}/agent-serif-{style.lower()}.woff2")
    left = sorted({r.nameID for r in TTFont(f"{OUT}/agent-serif-{style.lower()}.woff2")["name"].names
                   if r.nameID not in KEEP and ("Charis" in r.toUnicode() or "SIL" in r.toUnicode())})
    print(style, "reserved names left outside notices:", left or "none")
