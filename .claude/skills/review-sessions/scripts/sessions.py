"""Read the agent's sessions where they live, and print only what a review needs.

Runs on the server, fed through ssh, so no transcript is copied anywhere:

  ssh "$REVIEW_SSH" python3 - list < sessions.py
  ssh "$REVIEW_SSH" python3 - show ID [--from SEQ] [--to SEQ] < sessions.py
  ssh "$REVIEW_SSH" python3 - failures ID < sessions.py

REVIEW_SSH is the server's ssh target, kept in .env, which is not committed.

list prints one line per session, oldest first, with the counts that point at
trouble: tool calls that failed, and calls repeated right after a failure.
show prints one session in order, every entry on a line or two, cut short where
it runs long; --from and --to keep a stretch of a long one. failures prints each
failed call with what was asked, so a long session can be read where it went
wrong. --root reads another state directory, such as a copy for a test.
"""
import argparse, json, os, sys

ROOT = "/var/lib/docker/volumes/agent-state/_data"


def cut(s, n):
    s = " ".join(str(s or "").split())
    return s if len(s) <= n else s[:n] + f" …[+{len(s) - n}]"


def result(e):
    """A tool result as (ok, text). The agent stores {"ok": bool, "content" or "error"}."""
    r = e.get("tool_result")
    if isinstance(r, dict):
        return r.get("ok", True), r.get("content") if r.get("ok", True) else r.get("error")
    return True, r


def load(root, sid):
    d = os.path.join(root, "data", "sessions", sid)
    meta = json.load(open(os.path.join(d, "meta.json"), encoding="utf-8"))
    path = os.path.join(d, "transcript.jsonl")
    entries = [json.loads(l) for l in open(path, encoding="utf-8") if l.strip()] if os.path.exists(path) else []
    return meta, entries


def counts(entries):
    users = calls = failed = again = rounds = 0
    last_failed = None
    for e in entries:
        role = e.get("role")
        if role == "user":
            users += 1
        elif role == "assistant":
            rounds += 1
            for c in e.get("tool_calls") or []:
                calls += 1
                if c.get("name") == last_failed:
                    again += 1
        elif role == "tool":
            ok, _ = result(e)
            if not ok:
                failed += 1
            last_failed = None if ok else e.get("tool_name")
    return users, rounds, calls, failed, again


def list_sessions(root):
    base = os.path.join(root, "data", "sessions")
    rows = []
    for sid in os.listdir(base):
        try:
            meta, entries = load(root, sid)
        except (OSError, ValueError) as err:
            rows.append(("", f"{sid}  unreadable: {err}"))
            continue
        users, rounds, calls, failed, again = counts(entries)
        rows.append((meta.get("created_at", ""),
                     f"{sid}  {meta.get('created_at', '')[:10]}..{meta.get('last_active_at', '')[:10]}  "
                     f"{meta.get('model', '?')}  skills={','.join(meta.get('enabled_skills') or []) or '-'}  "
                     f"user={users} rounds={rounds} calls={calls} failed={failed} again={again}  "
                     f"${meta.get('cost', 0):.2f}  {cut(meta.get('title'), 60)}"))
    print("id  created..last active  model  skills  user messages, rounds, tool calls, failed, repeated after a failure  cost  title")
    for _, line in sorted(rows):
        print(line)


def show(root, sid, first=None, last=None):
    meta, entries = load(root, sid)
    entries = [e for e in entries if (first is None or e.get("seq", 0) >= first) and (last is None or e.get("seq", 0) <= last)]
    print(json.dumps({k: meta.get(k) for k in ("id", "title", "model", "enabled_skills", "forked_from",
                                                "cost", "prompt_tokens", "cached_tokens", "created_at",
                                                "last_active_at")}, indent=1))
    for e in entries:
        seq, role = e.get("seq"), e.get("role")
        if e.get("type") == "event":
            extra = f" covers_through={e['covers_through']}" if e.get("covers_through") else ""
            extra += f" reverts_from={e['reverts_from']}" if e.get("reverts_from") else ""
            print(f"\n#{seq} EVENT {e.get('event_kind') or ''}{extra} {cut(e.get('text'), 600)}")
        elif role == "user":
            print(f"\n#{seq} USER: {cut(e.get('text'), 3000)}")
        elif role == "assistant":
            if e.get("text"):
                print(f"\n#{seq} ASSISTANT: {cut(e.get('text'), 1500)}")
            for c in e.get("tool_calls") or []:
                print(f"#{seq}   -> {c.get('name')}({cut(c.get('arguments'), 300)})")
        elif role == "tool":
            ok, text = result(e)
            mark = "" if ok else "FAILED "
            print(f"#{seq}   <- {e.get('tool_name')}: {mark}{cut(text, 300 if ok else 800)}"
                  + (f"  STDERR: {cut(e.get('stderr'), 300)}" if e.get("stderr") else ""))
        else:
            print(f"\n#{seq} {role or e.get('type')}: {cut(e.get('text'), 300)}")


def failures(root, sid):
    _, entries = load(root, sid)
    asked = {}
    for e in entries:
        for c in e.get("tool_calls") or []:
            asked[c.get("id")] = (e.get("seq"), c)
        if e.get("role") == "tool":
            ok, text = result(e)
            if not ok:
                seq, c = asked.get(e.get("tool_call_id"), (None, {}))
                print(f"#{seq} -> #{e.get('seq')}  {e.get('tool_name')}({cut(c.get('arguments'), 300)})\n    {cut(text, 600)}")


def main():
    ap = argparse.ArgumentParser(usage=__doc__)
    ap.add_argument("what", choices=["list", "show", "failures"])
    ap.add_argument("ids", nargs="*")
    ap.add_argument("--from", dest="first", type=int)
    ap.add_argument("--to", dest="last", type=int)
    ap.add_argument("--root", default=ROOT)
    a = ap.parse_args()
    if a.what == "list":
        list_sessions(a.root)
    elif not a.ids:
        sys.exit(f"name the session to {a.what}")
    else:
        for sid in a.ids:
            if a.what == "show":
                show(a.root, sid, a.first, a.last)
            else:
                failures(a.root, sid)


if __name__ == "__main__":
    main()
