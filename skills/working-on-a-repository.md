---
name: working-on-a-repository
description: How to work on a repository that is not your own code — where the credential comes from, why logging in and ssh keys cannot work here, and what to do instead.
default: off
---

You can clone a repository, change it, and propose the change back. What you cannot do is
authenticate the way these programs' own documentation assumes, and finding that out halfway through
a clone is finding it out in the worst place.

If the repository is this agent's own code, that is a different procedure with its own skill:
`changing-yourself`. This one is for everything else.

## The credential is in the environment, and only if this conversation was granted it

`gh` reads `GH_TOKEN`; `glab` reads `GITLAB_TOKEN`. A grant belongs to one session and is fixed when
that session starts, so a conversation that was not granted one cannot be given it halfway through.

Check before you write anything — the one for the forge you are working against, not both:

```sh
for v in GH_TOKEN GITLAB_TOKEN; do
  eval "[ -n \"\$$v\" ]" && echo "$v ok" || echo "$v MISSING"
done
```

If the one you need says `MISSING`, stop. Say which, and that this conversation was not granted it —
the operator grants it under **Controls → granted environment**, and because grants are fixed for a
session's life, acting on it means forking this conversation or starting a new one.

## Do not log in, and do not look for a key

`gh auth login`, `glab auth login`, `git config --global`, `~/.ssh/id_ed25519`, `~/.netrc`: all of
them read or write under the home directory, and you cannot read the home directory. A tool is
granted its own session's working directory, the tool directory, and the system paths; everything
else is denied. `auth login` will appear to do something and the next command will find nothing.

There is no key to install and no login to run. The token in the environment is the whole of it.

`glab` needs one thing more. It creates a configuration directory before it runs any command at all,
and it looks for it under the home directory, so every call fails on a directory you never asked for
until you point it somewhere you can write:

```sh
export GLAB_CONFIG_DIR="$PWD/.glab"
```

Do that once, before the first `glab` command. `gh` needs nothing of the kind.

## Clone over HTTPS, with the token in the URL

```sh
git clone "https://x-access-token:$GH_TOKEN@github.com/<owner>/<repo>.git" repo
git clone "https://oauth2:$GITLAB_TOKEN@gitlab.com/<group>/<project>.git" repo
```

`gh` and `glab` work from inside the checkout once it is there, and take their credential from the
same variable.

The URL carries the token, so it is written into `repo/.git/config`. Do not print it — not with
`git remote -v`, not by reading `.git/config`, not into a commit message or a pull request body. A
transcript is read later, and by then the token is still valid.

## The checkout lives in this session's working directory

It is the only place you can write. Clone into it, work there, and if a clone is already there from
earlier in the conversation, `git -C repo pull` rather than cloning again.

## Propose the change; do not land it

Branch, commit, push, and open the request:

```sh
git -C repo switch -c <short-kebab-description>
git -C repo push -u origin <branch>
gh pr create --fill       # GitHub
glab mr create --fill     # GitLab
```

A body you wrote is better than `--fill`: say what changed, what you tested, and what you were unsure
about. Read the pipeline if there is one, and fix a red check by pushing to the same branch.

Then hand it over — the number, and what you want looked at. Whether it lands is the reviewer's
decision, not yours, and this is someone else's repository. Do not describe a change as done because
you pushed it.

## Read the repository's own rules first

A repository that carries a `CONTRIBUTING.md`, a `CLAUDE.md`, or an `AGENTS.md` says in it how it
wants to be worked in — how to run the tests, what a commit message looks like, what a pull request
has to contain. It is not optional reading: it is the difference between a change that merges and one
that is closed.
