#!/usr/bin/env python3
"""Say what speaking a script will cost, before anything is spoken.

Usage: estimate_cost.py [chunks.json]
Counts the characters in chunks.json and prices them on both speech models at the
gateway's current prices. Spends nothing.
"""
import json, os, sys, urllib.request

MODELS_URL = "https://openrouter.ai/api/v1/models?output_modalities=speech"
# name, gateway id, default voice, dollars per million characters as of 2026-10-31
OPTIONS = [
    ("HeyGen Turbo", "heygen/heygen-voice-1-turbo", "gareth", 4.0),
    ("ElevenLabs Flash", "elevenlabs/eleven-flash-v2.5", "brian", 40.0),
]
# What one round of the agent cost in the session this skill was worked out in:
# the low end after compaction on a cheap provider, the high end before it.
ROUND_LOW, ROUND_HIGH = 0.0012, 0.0067
ROUNDS_IN_A_TURN = 100

def live_prices():
    try:
        with urllib.request.urlopen(MODELS_URL, timeout=15) as r:
            models = json.load(r)["data"]
        return {m["id"]: float(m["pricing"]["prompt"]) * 1e6 for m in models}
    except Exception:
        return {}

def money(x):
    return f"${x:.3f}" if x < 0.01 else f"${x:.2f}"

def main():
    path = sys.argv[1] if len(sys.argv) > 1 else "chunks.json"
    if not os.path.exists(path):
        sys.exit(f"{path} not found: write the script first")
    chunks = json.load(open(path))
    chars = sum(len(c["text"]) for c in chunks)
    prices = live_prices()
    print(f"Script: {len(chunks)} chunks, {chars:,} characters")
    print("Speech:")
    for name, model, voice, price in OPTIONS:
        now = prices.get(model, price)
        print(f"  {name:17} {money(chars * now / 1e6):>7}   ${now:.0f} per million characters, voice {voice}, {model}")
    if not prices:
        print("  (prices from 2026-10-31: the gateway could not be reached)")
    low, high = len(chunks) * ROUND_LOW, len(chunks) * ROUND_HIGH
    print(f"Agent rounds, either way: about {money(low)} to {money(high)}, one round per chunk")
    if len(chunks) + 5 > ROUNDS_IN_A_TURN:
        print(f"Warning: {len(chunks)} chunks is more than one turn of {ROUNDS_IN_A_TURN} rounds if spoken one at a time. "
              "Send ten calls per response; if it stops anyway, the operator sends continue.")

if __name__ == "__main__":
    main()
