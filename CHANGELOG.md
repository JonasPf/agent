# Changelog

## 2026-10-30

- **The voice-over skill comes with the agent.** Send a video and ask for a
  voice-over: the agent transcribes the narration, tidies it, speaks it in a
  synthesized voice, and puts it back on the video at full length. A silent
  screen recording gets a narration written from what it shows. It used to
  exist only where it had been installed by hand; every deployment now has
  it.

## 2026-10-29

- **The agent installs the programs it needs.** A new `packages` tool finds
  a program in Nix's stable release and installs it, so a conversation that
  needs `ffmpeg`, `imagemagick`, or almost anything else asks for it instead
  of working around its absence. What a conversation installs is on its own
  PATH and nobody else's, but it is downloaded only once: two conversations
  installing the same program share one copy, and neither can change it. A
  fork has its origin's programs without copying them, and deleting a
  conversation removes the ones no other conversation uses. An install that
  would leave less than 2 GB of disk free is refused.

## 2026-10-28

- **A skill reads like a document.** Opening a skill on the Skills screen
  shows it rendered, with its description set apart at the top: that one
  line is what every conversation sees, and how the agent decides to open
  the rest. Switch to Source to see the file as written. Editing is its own
  screen, with a preview, and Save brings you back to the skill.
- **A skill can have more than one file.** Add a file to one of your skills
  — a checklist, a table, a longer example — and the skill becomes a folder
  with SKILL.md and the files beside it. Each file is a tab, and a CSV shows
  as a table. The agent reads SKILL.md first and opens the other files
  when SKILL.md says to.
- **Remembering is keeping records.** Asking the agent to remember, note,
  or keep a list of something now sends it to the same skill as tracking a
  measurement: a list goes in `lists/`, a fact or preference in `notes/`,
  and a series in `log/`, all in the conversation's own folder. Before it
  writes anything with a date it now checks the clock and lets the shell
  write the date, because a long conversation was putting tomorrow's date
  on evening entries.
- **The agent can call other models.** A new tool, `call_model`, makes one
  call to any model OpenRouter offers while the conversation stays on its
  own: have a speech model read a script aloud, transcribe a recording with
  timestamps, show an image or a clip to a model that sees, put a hard
  question to a model that reasons, or a yes/no question to a decision
  model. The agent makes the call with its own key, and what it costs is
  added to the conversation's cost. A voice-over no longer needs a 1 GB
  download in every conversation.
- **The agent can write you a skill.** When a task of several steps has
  worked, the agent offers to save it as a skill. Say yes and it drafts one
  in the conversation's files, under `skills/<name>/`: the steps that
  worked and the traps it hit, any scripts it needs, and a few test cases
  it runs before handing it over. Nothing is installed until you do it.
- **Install a skill from a conversation's files.** In Files, a folder
  holding `SKILL.md` has **install as skill**. If you already have a skill
  of that name, it asks before replacing it, so you can go round a few
  times until a skill is right. Installed, the skill opens to be read.
- **A skill's scripts run.** A skill installed with scripts can have them
  run by any conversation that has the skill; no conversation can change
  them. Reading a skill now shows it as stored, front matter included.
- **A skill's name is lowercase words joined by hyphens,** like
  `voice-over`, the same form other agents' skills use. A name written any
  other way (`Voice_Over`) is refused when you save or install it, and says
  how to write it.
- **A turn going in circles stops.** If the agent makes the same failing
  call three times in a row, it stops and says which call and what error,
  instead of trying again until the 100-round limit. Send "continue" to
  carry on. A provider that garbled tool calls had one conversation repeat
  a broken call 96 times; it would now have stopped after three.
- **Forking says it is working.** A fork copies the conversation's files
  as well as its words, which for a large one takes a minute. The fork
  screen now says how much it will copy, and the button reads "Copying
  2.4 GB…" until the fork is ready, instead of looking as though nothing
  happened. Starting a conversation says "Starting…" the same way.

## 2026-10-27

- **A conversation's name is a name.** The title a new conversation is given
  is now asked for as a name for your first message, not as an answer to it.
  A message like "make a voiceover for this clip" could come back titled with
  the model's reply — "I can't help with this request as stated" — and now
  can't: anything longer than six words is set aside for the first line of
  your message.
- **A failed title no longer stops the agent.** If the call that names a new
  conversation failed, the agent crashed. Now the conversation is named after
  the first line of your message and everything carries on.
- **A file the conversation names opens from there.** When the agent says
  where it put something, or a tool says what it wrote, the file's name is
  a link: tap it to open or download the file, with no trip to Files. Your
  attachments are links too. Only names of files that are really there
  become links.
- **A fork keeps what its conversation installed.** A program downloaded or
  a script installed with pip in a conversation stopped working in a fork
  of it: the copy dropped links and made every file non-executable. A fork
  now copies files with their permissions and links as links, and a link
  into the original's folder points into the fork's own, so it keeps
  working after the original is deleted.

## 2026-10-26

- **A conversation keeps its own variables.** Under "variables", when you
  start or fork a conversation or later in its Details, add a name and a
  value: a GitHub token, a key for one service. Its tools and checks get them
  from the next call on, and no other conversation does. A value is hidden
  once entered and never shown again, and an export carries only the names.
  A change made mid-conversation leaves a line in the conversation saying
  which variables changed. The old "granted environment" is gone: nothing is
  passed through from the agent's own environment any more. A conversation
  that had names granted keeps them, with the values they had.
- **Starting or forking a conversation sets everything about it.** The title
  and when it compacts are on that screen too, next to the model, tools,
  skills, persona, and variables. Details keeps only what can still change:
  the title, compaction, variables, export, and delete. Export moved there
  from the Files screen.
- **The compaction banner has a ?** Hover over it or tap it for what
  compacting does: when it happens, what is summarised and what is kept word
  for word, and that nothing is deleted.

## 2026-10-25

- **Your phone is told about messages you haven't read, even with the app
  closed.** Open More and turn on push notifications under "device". On an
  iPhone, add the app to your home screen first and turn them on from there.
  A message is sent to your devices if it is still unread ten seconds after
  the agent wrote it. If you read it on any device in that time, nothing is
  sent. Tapping the notification opens that conversation. The same screen
  sends a test push, lists every device that is signed up, and shows what
  each one's push service last answered. A device the push service no
  longer knows is removed by itself. The agent still has no way to send a
  notification of its own.
- **The Metrics screen is easier to read at a glance.** It opens with four
  totals: conversations, tool calls and how many failed, skill reads, and
  what jobs cost. Each tool's calls are a bar, with the failed share in red
  at its end, and its last failure is one line underneath; tap it to read
  all of it. Each skill's bar shows how many of the conversations offered
  it actually read it. Each job is a card with its cost, its schedule,
  whether a check runs first, and its wakes. Tap a bar for its numbers, or
  a **?** for what a term means. Money is shown to the cent.
- **The Tools screen says what the model sees and what the agent uses.**
  Each tool is one line until you tap it. Opened, it shows what the model
  is sent: the description and every parameter. A parameter with no
  description is flagged, because the model sees only its type. Below that
  is what the agent uses to run the tool: its timeout, table prefix,
  credentials and extra folders. Two buttons show the manifest and the
  exact entry sent to the model. The sandbox is shown once at the top,
  instead of on every tool. "How a tool call works" explains the four
  steps of a call, and a **?** beside any unclear term explains it.
- **A tool's manifest keeps the agent's own settings under `agent`.** The
  top level now holds only what the model is sent: `name`, `description`
  and `parameters`. The table prefix, timeout, extra reads and environment
  moved into an `agent` block. A manifest still in the old shape, or with a
  misspelt setting, no longer loads, and the Tools screen names the field.
- **Every parameter of the supplied tools is described.** The five that
  weren't now say what they do: `edit`'s `replace_all`, `read`'s `offset`
  and `limit`, `schedule`'s `action`, and `session_search`'s `limit`. The
  model no longer has to guess from the name.

## 2026-10-23

- **A Metrics screen shows what gets used.** Open More, then Metrics. It
  shows how often each skill is read, and in how many of the conversations
  that were offered it. It shows how often each tool is called, how often
  it fails, and what its last failure said. For each job it shows how many
  wakes acted, were skipped or failed, and what they cost. Choose the last
  7 days, the last 30, or all time. The numbers are counted from the
  conversations themselves, so deleting a conversation removes its numbers
  too.

## 2026-10-22

- **A full disk no longer garbles a conversation.** When the server's disk
  filled up mid-write, half a message reached the file, the next message was
  glued onto it, and after a restart both were gone and their numbers were
  reused. Now a failed write leaves nothing behind, the next message always
  starts on its own line, and numbering continues after the highest number
  already used. Going back to a message after a damaged line works again.
- **The agent can read a search result in full.** A search used to show
  only a few words around each match. When a compaction hid the agent's own
  last answer, it spent four hours searching to piece it back together. It
  can now open any search result whole by its number.
- **A conversation also compacts after a turn that failed.** Before, only a
  turn that finished normally triggered compaction, so a failed turn's growth
  was sent again, in full, on every call of the next one.
- **Evals run on the model conversations use.** The default eval model is
  now `z-ai/glm-5.3`. The cheaper one before it failed basic cases too often
  to tell a good tool description from a bad one.

- **A turn that will not stop now stops at 100 rounds.** One turn ran 728 tool
  calls over four hours and cost $10. Now a turn that calls tools a hundred
  times without answering stops there, and a "round limit" line in the
  conversation says so. Reply "continue" to let it carry on.
- **The shell can wait 15 minutes in one go.** It used to give up after two
  and a half, so to wait for a long script the agent asked "done yet?" again
  and again, 265 times in one case, each a paid model call. It now waits
  inside one command. `file` is installed too.
- **A missed edit points at the line it meant.** When the text the agent
  tries to replace isn't quite what the file says, the error now quotes the
  closest line, or says only the spacing differs, so it can retry without
  reading the file again.
- **Skills get read when they apply.** The agent read a skill in under a
  third of the turns where one applied, and forgot it after the conversation
  compacted. The skill list now says to read a skill before starting the work
  and again after compaction, and each skill says when to use it. New
  conversations get the full change; existing ones get the clearer
  `skill_read` instructions.
- **The notes tool is gone.** It was used in one conversation, and nothing it
  saved was ever read back. Those 16 notes are now in that conversation's
  `notes/from-notes-tool.md`. Keeping records in files, which the
  keeping-records skill describes, does the same job.
- **The two code-changing skills are gone.** `changing-yourself` and
  `working-on-a-repository` were off unless a conversation turned them on,
  and none ever did. The agent can still clone, branch and open a pull
  request when asked to and granted a token.
- **An answer stays in its own conversation.** Leaving a conversation while
  its answer was still arriving left that half-written answer at the bottom
  of every conversation you opened after it, each one saying "generating".
  Now each conversation shows only its own answer.

## 2026-10-16

- **The revert button is easier to spot.** The faint grey ↶ beside each of
  your messages is now a ↩️ emoji on a small button with its own border, and
  a little bigger on a phone. It does the same thing as before.
- **See which provider wrote each answer.** One model is run by many
  providers, and the gateway picks one per message. When one of them breaks,
  its answers come out as garbage while the others are fine. Each answer now
  names its provider on the small line under it, next to the cache figure, so
  a run of nonsense can be traced to who produced it. Older answers name none.
- **Take a conversation back to an earlier message.** Each message you sent,
  and each job's wake, now has a small ↶ beside its time. Tapping it asks
  first, then removes that message and everything after it from the
  conversation, and puts your message back in the box so you can change it
  and send again. Files the agent wrote and jobs it created are not undone,
  and the warning says so. Nothing is erased: search and the history still
  find what was taken back.

## 2026-10-15

- **A finished background job reads as finished.** A script the agent started
  in the background and then checked on kept answering "still running" after
  it had ended, so a conversation could poll it for hours without ever moving
  on. Finished processes are now cleaned up, and a check sees them gone.
- **Changes made at the same moment no longer trip over each other.** A
  conversation renamed or read while the agent was working on it could, rarely,
  lose the change or show figures half updated, and a title the agent wrote
  could replace one you had just chosen. Every change is now applied in turn.
- **The first message after a break is cheaper.** The first message of a
  conversation, or the first after five quiet minutes, wasn't cached, so each
  step of the agent's work on it paid full price for everything sent before.
  It is cached now, like every other message.
- **See what the cache is saving you.** The line under a conversation now
  shows how much of it the provider has cached and how long that lasts,
  counting down, and says when it has expired. It also estimates what the
  next message will cost and whether it will be read from the cache. Each
  answer notes how much of its call came from the cache, or that none did.
- **A model's prices fit on one line again.** Each figure about a model —
  price in, price out, context, and the rest — was drawn as a box of its own,
  stacked down the screen. They are a compact row under the model's name
  again.
- **A double tap starts one conversation.** Pressing Start conversation or
  Create fork twice used to make two. The button now waits for the first
  press, and if the agent refuses, it says why and lets you try again.
- **Opening part of the prompt leaves the rest alone.** Expanding a section
  of a conversation's prompt used to redraw the whole conversation, closing
  any tool results you had opened, and opened that section in every other
  conversation too. It now opens just that section, just there.
- **An old link to a deleted conversation says so.** It used to open a blank
  page under the previous conversation's header, whose buttons still acted on
  that conversation. It now explains that the conversation no longer exists.
- **An open conversation keeps its header up to date.** The title it gets
  after its first answer, and its cost and context after each turn, used to
  appear only after leaving and coming back; the header said "New session"
  and the cost stayed at nothing. They now update as they change.
- **Nothing goes missing when you open a busy conversation.** Opening a
  conversation while the agent was answering, on a slow connection, could
  leave your message or its reply off the screen until the page reconnected.
  Everything that arrives while it loads is now shown.
- **Leaving a screen quickly no longer leaves bits of it behind.** On a slow
  connection, moving on from a new conversation, a fork, a conversation's
  details, More, or a skill before it finished loading could finish drawing
  it underneath the next screen, header buttons included. A skill that no
  longer exists now says so instead of showing a blank page.
- **An odd character can no longer freeze a conversation.** An answer
  holding a particular invisible character could hang the page every time the
  conversation was opened. Such characters are now shown as �.
- **Cron schedules fire when cron would.** "0 9 1 * 1" now means the first
  of the month and every Monday, as in any crontab, rather than only a Monday
  that falls on the first, which could be months away. Sunday may be written
  as 7, and "1/6" in the hours means 1, 7, 13, and 19.
- **The agent can search for follow-up.** When the agent searched its own
  history for a word with a hyphen or a dot in it, such as follow-up or
  notes.md, the search failed with a database error. It now finds them, the
  same way the search screen always has.
- **A new skill or persona cannot overwrite an old one.** Saving a new one
  under a name already in use used to replace the existing one without a
  word. It is now refused, saying the name is taken; open the existing one to
  change it.
- **A conversation's files stay in its own folder.** A tool could leave a
  link there pointing elsewhere on the server, and downloading or deleting
  through it reached files outside the conversation. Links that lead out are
  now refused, and an upload never writes through one.
- **Two photos are two files.** A phone names every camera photo
  image.jpg, so attaching two used to keep only the last one, and an upload
  could quietly replace a file the agent had written. A name that is taken
  now gets a number (image-2.jpg), and the message names each file where it
  landed.
- **Archiving is gone.** Nothing in the interface could archive a
  conversation, so the archived section of the list only ever held what the
  API had put there. Conversations are now simply kept or deleted, in one
  list, newest first.
- **A refused change to a conversation changes nothing.** A rename sent
  together with something that cannot change was refused but still renamed
  the conversation. A blank title is now refused by the agent itself, not
  only by the details screen.
- **A fork of a compacted conversation reads what the original reads.** The
  copy brought one already-summarised message back into the agent's view,
  and could break the fork outright when that message was a tool's result.
  A compacted fork also now says where it was copied from.
- **Deleting a conversation that is working deletes it.** The agent used to
  finish whatever it was doing first, still paying for it, and the
  conversation then reappeared in the list as a broken stub. Deleting now
  stops the work at once, and the conversation and its jobs stay gone.
- **The newest turns now survive a compaction.** A compaction is meant to
  fold the older part of a conversation into a summary and keep the most
  recent turns word for word. Those recent turns were being dropped: the agent
  saw the summary and nothing else. It now sees the summary followed by the
  turns it kept, as intended.
- **A compacted conversation reads the way the agent reads it.** The summary
  now comes first, followed by the turns it carried over word for word,
  grouped and labelled as such, then everything since. What the summary
  replaced is no longer drawn, and the "show hidden turns" button is gone.
- **Search your own history.** Search from a conversation's header, or from a
  compaction's summary, looks in that conversation, can widen to all of them,
  and searches as you type. Each result says who said it, when, and whether
  the agent can still see it. Tapping one opens the conversation's full
  history at that message, folded turns included and dimmed.
- **Compacting on request is safer.** Compact, in a conversation's menu, now
  waits for the agent to finish what it is doing rather than folding the
  conversation underneath it, and with nothing to fold it says so instead of
  quietly doing nothing.
- **Choose when each conversation compacts.** A conversation's details screen
  now sets the size it compacts at (forty thousand tokens unless you change
  it) and how much it keeps word for word. It can be changed at any time, and
  a fork keeps its original's settings.

## 2026-10-06

- **Busy turns no longer slow every open page.** While the agent works
  through tool calls, each page showing the conversation used to fetch the
  whole conversation list again for every step, on every device. The list is
  now fetched only when an unread count actually changes.
- **The conversation list loads in a blink.** Building it used to re-measure
  every conversation's context and walk every file it holds, every time.
  Those figures are now kept until something changes them, so the list
  arrives in a few milliseconds instead of a noticeable pause with many
  conversations or a large checked-out repository.
- **Long answers stream smoothly on a phone.** The conversation used to be
  rebuilt from the top for every step of a turn, and a reply redrawn from its
  first word for every few new ones. Now only what is new is drawn. A tool
  result you opened stays open while the agent carries on, and scrolling up
  to read while an answer arrives no longer pulls you back down.
- **Coming back to the app never waits on OpenRouter.** The first thing a
  page does when you return to it used to wait for OpenRouter to report your
  credit, once an hour, and a slow day there could leave the page hanging or
  mark the container unhealthy. The credit is now fetched in the background
  and the rail updates when it arrives.
- **Coming back to a long conversation is quick.** Returning to the page, or
  reconnecting, used to download the whole conversation again, tool output
  and all, and draw it from the top. It now fetches only what was written
  while you were away. Opening a conversation also loads it in one round trip
  instead of two.
- **Starting a server no longer stalls the agent.** A shell command that left
  something running in the background, such as a local server, used to hold
  the agent up until the command's two-minute timeout. It now returns as soon
  as the command itself is done. A command that runs too long is stopped along
  with everything it started.
- **Forking and comparing are quicker, and quiet.** Copying a conversation
  into a fork, or into each model of a comparison, used to write it one
  message at a time, and could set off a notification for a copied reply as
  if it were new. The copy is now written in one go, without notifications.
- **The agent starts faster.** Every conversation is now read back in
  parallel when the agent starts, and rebuilding the search index, when it
  has to, is one step rather than one per message.
- **Less to download.** The app, its conversations and lists are now sent
  compressed, typically a fifth of the size or less. Files you download from
  a conversation are sent exactly as stored.

## 2026-10-05

- **Start typing straight away.** On a computer, opening a conversation puts
  the cursor in the message box. On a phone it does not, so the keyboard
  stays down until you tap.
- **Up arrow brings back what you sent.** In the message box, up steps back
  through your earlier messages in this conversation and down steps forward
  again, ending at whatever you had half typed.

## 2026-10-01

- **Laptop and phone stay in step.** A message sent on one device now appears
  on the other without a reload, even if the other was asleep or in the
  background when you sent it. Coming back to the page brings the conversation
  and the unread badges up to date.
- **No crash when the password has lapsed.** Coming back to the page after
  the browser forgot the password could crash Brave on a phone, because the
  page asked for several things at once and each one asked for the password.
  It now checks once first, and if the password is gone it reloads, so you are
  asked for it once, the same way as when you first opened the page.

## 2026-09-29

- **Reminders count as unread.** A message from a scheduled job used to leave
  the unread badge at zero; it now counts like any other reply. The badge
  counts messages rather than turns, so two answers waiting show as 2.
- **Opening a conversation clears its badge.** The badge could stay after you
  had read a conversation, most often on a phone coming back from the
  background. It now clears the moment the conversation opens. A reply that
  arrives while you are looking at it never adds to the badge at all.
- **A conversation opens where you stopped reading.** If it has unread
  messages, the first of them is at the top of the screen under an "unread"
  line, so you read down from there instead of starting at the end.

## 2026-09-25

- **You can see the agent thinking.** After you send a message, a "thinking"
  line now sits where the answer will appear, until the first word arrives.
  Comparisons show one in each column whose model has not started answering.
- **A finished answer comes into view.** When a turn ends, the conversation
  scrolls down to the answer, even if you had scrolled up while waiting. In a
  comparison, each column scrolls to its own answer when that model finishes.
  A column you scrolled back up in stays where you left it until then.
- **A model's price matches everywhere.** The model shown on a configuration
  screen used to keep the price from when the page first loaded, so it could
  disagree with the model dialog after a price change. Both now show the
  current price.

## 2026-09-24

- **Controls is now Details, and it reads rather than asks.** That screen used
  to carry a full configuration editor — model, tools, skills, persona,
  grants — ending in a Fork button, which is the same screen Fork already
  opens from the same menu. It now states what the conversation runs on and
  leaves it at that: the settings are there to read, greyed and unpressable,
  and Fork is where a different configuration is chosen. What is left that you
  can actually do to a conversation — rename it, delete it — stayed.
  The duplicate Files button went too; the header has had that link all along.

## 2026-09-23

- **A conversation can be renamed.** The title the agent writes after your
  first message is a guess at what the conversation turned out to be about,
  and it is often wrong by the end. Open a conversation's Details and the
  title is now the first thing on the screen, in a box: change it, tap
  Rename, and the rail and the list follow. Everything else on that screen is
  still fixed for the conversation's life and still changes only by forking.
- **Search works again, through a search API.** It used to read a search
  engine's own results page in a browser, which is the one page a search
  company will not serve a robot. It now asks an API and gets an answer to the
  question it asked. This needs a key: put `TAVILY_API_KEY` in the agent's
  `.env` — a free account covers a thousand searches a month and takes no card
  — and restart. Without one, a search fails and says exactly that, rather than
  looking broken. The key goes to `web_search` and to nothing else; the Tools
  screen names it on that tool's card, and the shell never sees it.
- **Search no longer hands back somebody else's results.** Bing has been
  answering automated queries with pages that look right — the query in the
  title, ten results below it — and are filled with unrelated pages, a
  different set every time: gift card retailers and Manhattan attractions in
  reply to a question about OCR. Nothing on the page said so, so the agent read
  them as the answer. A search whose results bear no relation to what was asked
  now fails and says that is what happened, rather than showing them.
- **The shell can now read documents without building its own tools first.**
  Asked to go through a folder of scans, the agent used to spend a dozen turns
  assembling a PDF reader and an OCR engine by hand before it could read the
  first page. It now has them: `pdftotext`, `tesseract` in English and German,
  `pip`, `numpy`, `pillow`, `jq`, and `unzip`, beside the `curl`, `wget`,
  `python3` and `perl` that were already there.
- **Installing a Python package works.** `pip install --user` failed on a home
  directory nothing was allowed to write, with an error that pointed at a path
  no one had chosen. Each conversation now has its own home directory, inside
  its own files, so a package it installs stays with it and is invisible to
  every other conversation.
- **Stop now works the moment a conversation says it is working.** Pressing stop
  just after sending could answer "this conversation is not working on
  anything" while the turn went ahead and ran. A turn is now stoppable from the
  moment it joins the queue, and one stopped before it started never runs. A
  turn waiting behind another can be stopped too, oldest first.
- **A stopped turn says where it got to.** One stopped before the agent reached
  the model no longer claims to have been stopped part way through.
- **Stopping a scheduled wake no longer counts as a failure.** It was logged as
  a failed run, counted against the job, and counted toward the breaker that
  pauses every job in the agent — so stopping three wakes stopped everything.
  A stopped wake is now logged as stopped, and the conversation says so.
- **Ask several models the same question and keep the answer you like best.**
  Compare, in a conversation's menu, puts one message to as many models as you
  choose at once. Each answers in a fork of the conversation — the whole
  transcript, a copy of the files, its own tools — and they run side by side,
  filling in as they go. Keep one and that conversation carries on; the other
  candidates are deleted, transcripts and files with them. The conversation you
  started from is never sent the message: it stays where it is, keeps its jobs,
  and records what was asked and who was asked it — and that record is a link
  back to the comparison, as is the *comparing* mark on any candidate in the
  list and the header of any candidate you open, so leaving the screen does not
  lose it. Asking five models sends the conversation five times over, none of it
  from cache, and the screen says what that comes to before you spend it.

## 2026-09-21

- **Opening one conversation after another no longer mixes the two.** Tapping a
  conversation while a previous one was still loading could leave the older one
  on screen while the address bar named the newer, and lay a second message box
  under it. The conversation on screen is now the one you asked for, with one
  message box.
- **A turn can be stopped.** While a conversation is working, the status line
  now carries a stop next to the count. It ends the turn where it is — the
  model call and whatever tool is running under it — and the transcript says
  it was stopped, so it does not read as the agent breaking. A scheduled wake
  stops the same way.
- **A long task no longer stops halfway.** A turn was cut off after twelve
  rounds of tool calls, and cut off silently: the conversation simply went
  quiet, with nothing said about why. A turn now runs until the agent is
  finished. If you set one going on something large, watch what it costs.

## 2026-09-20

- **Uploads can be 250 MB.** The limit was 100 MB, which a saved web page with
  its images inside can pass on its own. Anything larger is still refused
  before it is sent, and a file of exactly 250 MB now goes through.
- **A session archive may be 500 MB**, so a session holding a large file can be
  exported and imported back. An archive being imported is read from disk
  rather than held in memory, which it no longer has room for.

## 2026-09-19

- **Attaching a file shows it.** A file picked with ＋ now waits above the
  message as a chip showing how much has uploaded. If it fails, the chip says
  why, and tapping it tries again. You can pick several files at once, and take
  one off with ×.
- **The agent is told what you attached.** Sending adds a line naming each
  file, so you no longer have to say you uploaded something. A message can be
  only files. It will not send while a file is still uploading.
- A file over 100 MB is refused straight away, rather than after it has all
  been sent.
- **The Files screen is a tree.** Directories are listed, empty ones too, each
  with what it holds, and fold and unfold when tapped. A large tree, like a
  cloned repository, opens folded.

## 2026-09-18

- **Memory is gone.** The agent no longer carries anything between
  conversations on its own: there is no memory section at the top of a
  conversation, no memory tool to write one with, and no Memory panel. What one
  conversation holds, another now reaches by searching for it — the agent can
  search every transcript when you ask it to, and the asking is a visible line
  in the conversation. Anything that was stored is deleted with the feature.
- A standing instruction — always use UTC, never touch the production database
  — now belongs in a **persona**. You write it once, you can read exactly what
  it says, and you choose it when you start a conversation. That is where the
  agent reads it on every single turn, rather than from a store that quietly
  steered answers you never connected to it.
- **Notes now belong to the conversation that wrote them.** Listing notes shows
  this conversation's; to see every conversation's the agent has to ask, and
  that ask appears in the transcript as the call it is. The Notes panel still
  shows all of them, because the panel is yours rather than a conversation's.
- **A conversation's prompt never changes now.** It is written when the
  conversation starts and is what every turn sends for the rest of its life, so
  editing a persona or a skill leaves the conversations already running exactly
  as they were — they take it up when you fork them. A compaction no longer
  writes a second prompt, and the cache behind a long conversation stops being
  thrown away at every fold.
- **The built-in persona writes for a phone.** Replies lead with the answer,
  use short bullets and numbered steps, name concrete paths and times, and end
  with the one next thing to do rather than a pleasantry. It also no longer
  agrees just to please: it says when you are wrong, reports a failure as a
  failure, and skips the praise. It never writes out a secret's value, treats
  what it reads on the web as information rather than orders, and asks before
  anything hard to undo. It no longer claims to remember things between
  conversations, which stopped being true when memory went.
- A new skill, **researching**, is on in every conversation. When you ask the
  agent to find something out, it plans a few narrow searches, reads the pages
  rather than the search snippets, and tells you which facts come from an
  official source and which are unverified. It gives every claim a link and a
  date. Long research is written to a file as it goes, so it survives the
  conversation being compacted.
- **Personas** have moved off the side rail and into **More**, next to Search.
  The rail keeps Jobs, Tools, and Skills.
- Opening a skill or persona, or starting a new one, now has its own address.
  The **back button** returns to the list instead of leaving it, and saving
  takes you back to that list too.

## 2026-09-17

- You can now write **skills** yourself, from the interface, without a shell.
  **Panels → Skills** lists what is there and opens each one: the skills that
  ship with the agent are read-only, and the ones you write can be created,
  edited, given a one-line description, switched between on-by-default and
  off-unless-chosen, and deleted. A skill you add is indexed in the
  conversations you start after it, not the ones already running.
- **Personas** are new, and have a panel of their own beside Skills. A persona
  is the opening section of the system prompt — who the agent is — and you pick
  one when you start a conversation, under **Controls → persona**. Choosing one
  replaces the built-in persona *entirely*, its working rules about scheduling
  and unattended wakes included, so write in the parts you want kept. The
  built-in persona is always there and is what every existing conversation runs
  on.
- A conversation can now run with **memory off**, chosen under **Controls →
  memory** before it starts. It gets no memory section and no memory tool, and
  nothing said in it reaches anywhere else. Nothing already stored is deleted,
  and every other conversation still has it.
- Persona and memory join the model, tools, skills, and grants as things fixed
  for a conversation's life: to change one, fork, and both transcripts say what
  changed.
- What you write is stored beside your data rather than inside the image, so it
  survives an upgrade. Neither directory is writable by a tool — the agent still
  cannot write its own skills or personas, and the kernel is what stops it.

- The image now carries `glab`, GitLab's command-line client, beside `gh`. A
  conversation can work on a GitLab project the way it already works on a GitHub
  one, and `GITLAB_TOKEN` joins `GH_TOKEN` among the credentials you can grant a
  conversation under **Controls → granted environment**.
- A new skill, **working on a repository**, says how to do that on anyone's
  repository: where the credential comes from, why `gh auth login` and ssh keys
  cannot work in here, how to clone over HTTPS instead, and that a pushed branch
  is a proposal rather than a finished change. Like *changing yourself*, it stays
  out of a conversation that did not ask for it — tick it under **Controls** when
  you set out to use it.

## 2026-09-16

- A tool call in a conversation now carries a **logs** toggle when the tool
  wrote anything to standard error. It stays closed, including on a call that
  failed and is already showing its error, and what it holds travels with the
  conversation when you copy it. These logs were always kept; there was simply
  nowhere to read them.
- When `web_fetch` cannot read a page — a 404, a refusal, a connection that
  never answered — it now names `web_browse` as the next thing to try, and says
  to look elsewhere rather than fetch the same URL again if that fails too. It
  used to say this only about pages it had already fetched successfully.
- `web_fetch` also recognises a page that never rendered by the template syntax
  left in its text — a binding nothing replaced, an attribute expression that
  leaked into the page — and not only by how little text there is. A shell with
  a large navigation bar used to pass as a page that simply says little.

## 2026-09-15

- On the Tools screen, each tool's sandbox is split in two: **Every tool**,
  which is the same on every card, and **This tool only**, which lists the
  paths that tool's manifest asked for (the browser tools ask for `/proc`,
  `/sys`, and `/var`).
- While the agent is working, the status line under the message box says so
  and counts the seconds, starting the moment you send. Reopening the
  conversation mid-turn keeps the count going from when the turn started.
- **Copy** in a conversation's header puts the whole conversation on the
  clipboard as Markdown: every message, each tool call and its result (long
  results clipped), and compaction summaries.
- The shell has `curl`, `wget`, `python3`, and `perl`.

- **The interface moves from port 8080 to 7770.** A proxy or port mapping
  that points at 8080 must point at 7770, or the interface will not answer.
  8080 is left free for the development servers tools start.
- Tools can no longer reach the interface's own API. They reach the agent
  through an API of their own, which offers memory, their own conversation's
  jobs, the skills that conversation has, and search. Nothing on it creates,
  forks, or messages a conversation, so a tool can no longer start a session
  holding `GH_TOKEN` or schedule a job into one.
- Inside the container, a tool may open TCP connections only to common ports:
  22, 53, 80, 443, 3000, 3306, 4000, 5000, 5173, 5432, 6379, 8000, 8080, 8443,
  8888, 9418, and 27017. Add others with `AGENT_TOOL_PORTS`. Linux older than
  6.7 cannot enforce this, and the agent says so.
- The Tools screen shows, for each tool, which paths it may read and write and
  which ports it may connect to, and says when either is not enforced.
- A job's check command no longer sees the agent's whole environment. It gets
  what a `bash` call in the same conversation gets: basics like `PATH`, plus
  that conversation's grants. Before this, a check the model wrote could read
  `OPENROUTER_API_KEY`.

- The model is picked in a dialog that slides in over the screen. Each model
  shows its price per million tokens in and out, its context window, and its
  Artificial Analysis intelligence, coding, and agentic scores where OpenRouter
  reports them, with a link to its OpenRouter page. The list can be searched and
  sorted by smartest, cheapest, largest context, or newest.
- A new conversation starts on the model you last picked, for a new
  conversation or for a fork onto a different model, and still does after a
  restart. `AGENT_MODEL` is gone: nothing reads it any more.
- Granted environment is a button. It opens a list of every variable name the
  agent could grant, the ones from `.env` first, each with a checkbox. Values
  are never shown, and the model key is never listed.
- A skill can ship switched off with `default: off` in its frontmatter, and
  `changing-yourself` now does. Tick it under skills for a conversation that is
  meant to change the agent. A conversation already running with the default
  skills loses it at its next compaction, when its prompt is rewritten.

- `web_browse` and `web_search` introduce themselves as an ordinary Chrome of
  the installed version, without the "HeadlessChrome" that headless chromium
  announces by default. Sites that turn automation away by that one word now
  serve the page. Checks that test the browser itself, like Cloudflare's
  "Just a moment…", still stop it; nothing answers or evades them.
- When a page is a bot check, `web_browse` fails and says whose it is —
  Cloudflare, DataDome, HUMAN — instead of returning the check's text as though
  it were the page.
- `bash` tells the model what its sandbox refuses: only the workspace is
  writable, so scratch files go under `$TMPDIR`, and a browser will not start
  from the shell. Web pages are for `web_fetch` and `web_browse`.
- Eighteen settings become eight. Seven paths only ever said where two roots
  were, so there are now two: `AGENT_STATE` for what outlives the container and
  `AGENT_HOME` for what the image ships. Four numbers that nothing ever set are
  constants — two of them were defaults for `compact_at_tokens` and
  `keep_verbatim_tokens`, which a session already carries and the interface
  already edits. And the eval model configures `cmd/eval` rather than the agent,
  so it is a flag on that command: `go run ./cmd/eval -model <id>`.
- `.env.example` lists every setting the agent reads, with its default, and a
  test keeps it that way in both directions. Thirteen were missing, including
  `AGENT_EVAL_MODEL`; the README documented `AGENT_ROTATE_TOKENS`, which nothing
  has read since compaction replaced rotation.
- The eval model is `google/gemma-4-26b-a4b-it`. The free tier it replaces was
  withdrawn, and a withdrawn model failed every case at once in a way that read
  as broken cases — so a run now checks the gateway lists the model first and
  says so if it does not. The new one is cheap rather than free, and takes under
  two seconds a case where the last free one took three and a half minutes.
- `web_fetch` is back, as its own tool: one HTTP request, no browser, for an
  API, a raw file, a feed, a README — most of what is actually asked for, in
  milliseconds instead of seconds. It is the first thing to reach for, and
  `web_browse` is for pages that need scripts.
- When a fetched page turns out to be assembled by its scripts, `web_fetch` says
  so and names `web_browse`. That is the difference from the old fallback it
  replaces: the substitution is offered, not made, so a shell can never be
  mistaken for a page that simply says little.
- A conversation grants its tools the environment they may read. `granted_env`
  on a session names the variables its tools receive — names, never values — and
  is fixed for that session's life like the model and the tools beside it. A
  credential now reaches the one conversation doing the work rather than every
  conversation there will ever be, which is what a tool manifest could only say.
  Set it under **Controls → granted environment**.
- `OPENROUTER_API_KEY` cannot be granted to anything, ever.
- The `changing-yourself` skill checks for `AGENT_REPO` and `GH_TOKEN` before it
  starts, and says which is missing instead of failing halfway through a change
  it cannot push.
- `web_fetch` is now `web_browse`, which is what it does. It opens the page in a
  browser, every time; the plain-HTTP fallback is gone, because a page that
  needed its scripts came back looking exactly like a page that simply says
  little, and whoever asked could not tell which they had.
- The browser is installed in the image at one path instead of searched for.
  `AGENT_BROWSER` and `PLAYWRIGHT_BROWSERS_PATH` are gone with the search.
- Fixes `web_browse` failing on every call in production, where it was meant to
  fall back and could not.

## 2026-09-14

- The version screen shows what changed and nothing else. The changelog's note
  to whoever writes it is a rule for this repository, kept in `CLAUDE.md`, and
  the screen is served the dated entries from the first one on.
- The repository ships an image, not a deployment. The Compose file is gone and
  nothing in the code, the workflows, or the image names a hosting platform; how
  to run it on one is documentation, with Dokploy worked through as an example.
- One volume. Everything that outlives the container is under `/app/state`, and
  the two directories inside it are the agent's own convenience rather than a
  boundary — Landlock is the boundary, and a tool confined to one session is now
  shown to reach nothing else under a shared root.
- `task db:clear` backs the database up again. It had been looking for it beside
  the transcripts, where it stopped living when Landlock gave it a directory of
  its own, and had been quietly leaving it behind.

## 2026-09-11

- Compact a conversation in place: a compaction rewrites the session it happens
  in rather than seeding a successor, so a conversation keeps one identity for
  its whole life.
- `bash` says when it truncated its output instead of handing the model a body
  cut without notice.
- Every tool has a page of its own in the spec, saying what its manifest cannot.
- One sandbox mechanism: Landlock, with no second policy to keep in step.
- The interface warns when nothing is enforcing the sandbox, and the browser
  tools use whatever browser is actually installed.

## 2026-09-09

- A tool asks for the paths it needs, and the environment is hidden behind
  `/proc`.
- The sandbox grants `/sys` and `/var` as well: a runtime is more than its
  interpreter.
- Tools are confined with Landlock, which works in an unprivileged container
  where bubblewrap could not.
- The principle is written down: no surprises, in two halves. Changing the agent
  is composition, not a feature.

## 2026-09-08

- The deployment refuses to start without the credential in front of the agent,
  and routes from the compose file rather than a file placed by hand.
- A sandbox is claimed only after it has been proven to run.
- Nothing personal is published: fixtures are anonymised and the operator's
  notes are untracked and out of the build context.

## 2026-09-07

- The agent deploys as an image built by CI and proposes changes to itself
  through pull requests.
- A tool is given only the environment its manifest names.
- The interface is one responsive shell at every width.
- Finished jobs can be deleted in one action.
- The browser tools run inside the sandbox and no longer phone home while
  fetching a page.
- The agent is rebuilt around a tool subprocess contract, per-job pricing, and a
  task-driven workflow.

## 2026-09-01

- First working agent: sessions, jobs, memory, tools, and the web interface.
