package shell

// tokens is the entire theme system. Every colour decision in the app lives
// here; components reference tokens and never literals, so a new theme is one
// block and nothing else.
const tokens = `
:root {
  /* ground */
  --ground:#1f1f1f; --band:#242424; --panel:#181818; --raised:#2a2a2a;
  /* rules */
  --rule:#2e2e2e; --rule-strong:#3d3d3d;
  /* ink */
  --ink:#cccccc; --ink-strong:#e8e8e8; --ink-mute:#7d7d7d; --ink-faint:#5a5a5a;
  /* rationed chroma: accent carries addressed, red carries severity */
  --accent:#0078d4; --accent-ink:#4daafc;
  --sev-hi:#f14c4c; --sev-lo:#cca700; --ok:#89d185;
  /* metrics */
  --scrim: rgba(0,0,0,.55);
  --row-pad:.28rem .6rem; --col-folio:4.6rem; --col-author:10rem; --col-kind:1.6rem;
}
:root[data-theme="light"] {
  --ground:#ffffff; --band:#f6f6f6; --panel:#f3f3f3; --raised:#eaeaea;
  --rule:#e5e5e5; --rule-strong:#cfcfcf;
  --ink:#3b3b3b; --ink-strong:#1a1a1a; --ink-mute:#767676; --ink-faint:#9a9a9a;
  --accent:#005fb8; --accent-ink:#0a5fb0;
  --sev-hi:#cd3131; --sev-lo:#a67f00; --ok:#317b33;
}
@media (prefers-color-scheme: light) {
  :root:not([data-theme="dark"]) {
    --ground:#ffffff; --band:#f6f6f6; --panel:#f3f3f3; --raised:#eaeaea;
    --rule:#e5e5e5; --rule-strong:#cfcfcf;
    --ink:#3b3b3b; --ink-strong:#1a1a1a; --ink-mute:#767676; --ink-faint:#9a9a9a;
    --accent:#005fb8; --accent-ink:#0a5fb0;
    --sev-hi:#cd3131; --sev-lo:#a67f00; --ok:#317b33;
  }
}
/* A third theme is one block. Nothing else changes. */
:root[data-theme="slate"] {
  --ground:#12171f; --band:#171d26; --panel:#0e131a; --raised:#1d242f;
  --rule:#222b36; --rule-strong:#313d4c;
  --ink:#c3ccd8; --ink-strong:#e6ecf3; --ink-mute:#788594; --ink-faint:#55606d;
  --accent:#4c8dd6; --accent-ink:#7fb1e8;
  --sev-hi:#e26d6d; --sev-lo:#c9a227; --ok:#7fbf88;
}
`

const baseCSS = tokens + `
* { box-sizing: border-box; }
html, body { margin:0; height:100%; }
body {
  background: var(--ground); color: var(--ink);
  font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace;
  font-size: 13px; line-height: 1.5;
  display: grid; grid-template-rows: auto 1fr auto; height: 100vh;
}
a { color: var(--accent-ink); text-decoration: none; }
a:hover { text-decoration: underline; }
:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }

/* ---- chrome ---- */
header {
  display:flex; align-items:center; gap:1rem; flex-wrap:wrap;
  padding:.4rem .7rem; background: var(--panel);
  border-bottom:1px solid var(--rule);
}
header .brand { color: var(--ink-mute); letter-spacing:.08em; text-transform:uppercase; font-size:.7rem; }
header nav { display:flex; gap:.15rem; }
header nav a { padding:.12rem .5rem; border-radius:3px; color: var(--ink-mute); }
header nav a.sel { background: var(--raised); color: var(--ink-strong); }
header .spacer { flex:1; }
header .who { display:flex; align-items:center; gap:.3rem; color: var(--ink-faint); font-size:.72rem; }
header select { background: var(--ground); color: var(--ink);
  border:1px solid var(--rule-strong); border-radius:3px; padding:.15rem .3rem; font:inherit; font-size:.75rem; }
header form { display:flex; gap:.3rem; }
header input, .composer input, .composer select {
  background: var(--ground); color: var(--ink);
  border:1px solid var(--rule-strong); border-radius:3px;
  padding:.2rem .45rem; font:inherit;
}
header button, .composer button {
  background: var(--raised); color: var(--ink); border:1px solid var(--rule-strong);
  border-radius:3px; padding:.2rem .6rem; font:inherit; cursor:pointer;
}
header button:hover, .composer button:hover { border-color: var(--accent); }

/* ---- room rail: rooms live on the left, one glance, one click ---- */
body.railed { grid-template-rows: auto 1fr auto; grid-template-columns: 12rem 1fr; }
body.railed > header, body.railed > footer { grid-column: 1 / -1; }
.rail {
  grid-row: 2; grid-column: 1; overflow-y: auto;
  background: var(--band); border-right: 1px solid var(--rule);
  padding: .45rem 0; font-size: .82rem;
}
.rail a {
  display: flex; align-items: baseline; gap: .4rem;
  padding: .22rem .8rem; color: var(--ink-mute);
}
.rail a:hover { color: var(--ink-strong); text-decoration: none; }
.rail a.sel {
  color: var(--ink-strong); background: var(--ground);
  border-left: 2px solid var(--accent); padding-left: calc(.8rem - 2px);
}
.rail a .room-name { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
/* unread: an accent tick, only ever on rooms that moved past what this
   browser has seen — chroma stays rationed */
.rail a .unread { color: var(--accent-ink); visibility: hidden; font-size: .7rem; }
.rail a.has-unread .unread { visibility: visible; }
body.railed main.ledger { grid-row: 2; grid-column: 2; }
@media (max-width: 640px) {
  body.railed { grid-template-columns: 1fr; grid-template-rows: auto auto 1fr auto; }
  .rail { grid-row: 2; grid-column: 1; display: flex; overflow-x: auto; overflow-y: hidden;
    border-right: 0; border-bottom: 1px solid var(--rule); padding: 0 .3rem; }
  .rail a.sel { border-left: 0; border-bottom: 2px solid var(--accent); padding-left: .8rem; }
  body.railed main.ledger { grid-row: 3; grid-column: 1; }
}

/* the identity chip: derived from the enrolled key, never a picker */
.me { border: 1px solid var(--rule-strong); border-radius: 3px;
  padding: .1rem .55rem; color: var(--ink); font-size: .75rem; }

/* ---- ledger ---- */
.ledger { overflow-y:auto; }
.head, .row, .carried, .srow {
  display:grid; align-items:baseline;
  grid-template-columns: var(--col-folio) var(--col-author) var(--col-kind) 1fr 2rem;
}
.head {
  position:sticky; top:0; background: var(--panel); z-index:1;
  border-bottom:1px solid var(--rule-strong);
  color: var(--ink-faint); font-size:.66rem; letter-spacing:.12em; text-transform:uppercase;
}
.head > div { padding: var(--row-pad); border-right:1px solid var(--rule); }
.head > div:last-child { border-right:0; }
.row > div { padding: var(--row-pad); border-right:1px solid var(--rule); }
.row > div:last-child { border-right:0; }
.row { border-bottom:1px solid var(--rule); }
.row:nth-child(even) { background: var(--band); }

.folio { color: var(--ink-faint); text-align:right; font-variant-numeric: tabular-nums;
  font-size:.72rem; }
.author { color: var(--ink-mute); overflow:hidden; text-overflow:ellipsis;
  white-space:nowrap; }
/* a grouped continuation keeps its row, loses only the repeated name */
.row.cont .author { visibility:hidden; }
.kind { text-align:center; font-size:.8rem; cursor:default; padding-left:0; padding-right:0; }
.chip { width:.7rem; height:.7rem; margin-right:.28rem; vertical-align:-1px;
  color:var(--ink-faint); stroke-width:1; }
/* Instant tooltip. position:fixed without offsets keeps the box at its static
   position while escaping ancestor overflow clipping — the author column's
   ellipsis would otherwise clip its own tooltip. */
[data-tip] { cursor:default; }
[data-tip]:hover::after {
  content:attr(data-tip); position:fixed; transform:translateY(1.15rem);
  background:var(--raised); color:var(--ink-strong);
  border:1px solid var(--rule-strong); padding:.05rem .45rem;
  font-size:.72rem; font-style:normal; white-space:nowrap; z-index:20;
}
/* A folded body reuses the carried-forward control, so one toggle serves both
   page-break conventions. It sits on its own line because it interrupts the
   text it is folding. */
.more { margin:.15rem 0 0 0; padding:0; }
/* :not([hidden]) is load-bearing. The hidden attribute works by a UA rule of
   display:none, and any author display rule outranks it — an unscoped
   display:block on this class silently unhides every folded body. */
.more-body:not([hidden]) { display:block; }
.about { color: var(--ink-faint); font-size:.78rem; border:1px solid var(--rule);
  padding:0 .3rem; border-radius:2px; margin-right:.35rem; }
.tick { color: var(--ok); text-align:center; opacity:.55; }
.body { white-space:pre-wrap; word-break:break-word; }
.body a { color: inherit; }

/* addressed: an editor's modified-line gutter, not a chat highlight */
.row.addressed {
  grid-template-columns: var(--col-folio) var(--col-author) var(--col-kind) 1fr;
  background: var(--ground);
  border-left:2px solid var(--accent);
  border-top:1px solid var(--rule-strong);
  border-bottom:1px solid var(--rule-strong);
}
.row.addressed .folio { color: var(--accent-ink); }
.to { color: var(--accent-ink); }

.att { color: var(--accent-ink); margin-left:.35rem; white-space:nowrap; }
.step { color: var(--ink-mute); font-variant-numeric: tabular-nums; }
.stall { color: var(--sev-lo); }
.sev { font-size:.68rem; letter-spacing:.06em; }
.sev-p0, .sev-p1 { color: var(--sev-hi); }
.sev-p2 { color: var(--sev-lo); }
.sev-p3 { color: var(--ink-faint); }

.row.struck .body { color: var(--ink-faint); text-decoration: line-through; }
.row.struck .erased { text-decoration:none; font-style:italic; }

.carried {
  display:grid; grid-template-columns: var(--col-folio) 1fr; align-items:baseline;
  width:100%; text-align:left; font:inherit;
  border:0; border-bottom:1px solid var(--rule);
  background: var(--band); color: var(--ink-faint); font-style:italic; cursor:pointer;
}
.carried > span { padding: var(--row-pad); }
.carried:hover { color: var(--ink-mute); }
.carried .cf::after { content:" ▸"; }
.carried[aria-expanded="true"] .cf::after { content:" ▾"; }
.carried-body[hidden] { display:none; }

/* The fold control reuses the carried-forward button's look, not its layout:
   .carried is a two-column grid whose first track is the folio width, and a
   fold label dropped into that track wraps to three lines. It sits inside the
   entry column already, so it needs no columns of its own. This rule follows
   .carried deliberately — equal specificity, and the later rule wins. */
.carried.more { display:block; width:auto; border-bottom:0; background:none; }
.carried.more > span { padding:0; }

/* ---- foot ---- */
footer {
  border-top:1px solid var(--rule-strong); background: var(--panel);
}
.balance {
  display:flex; gap:1.4rem; flex-wrap:wrap;
  padding:.3rem .7rem; color: var(--ink-faint); font-size:.72rem;
}
.balance b { color: var(--ink); font-weight:500; }
.composer { display:flex; gap:.3rem; padding:.4rem .7rem; border-top:1px solid var(--rule); align-items:flex-end; }
/* a taller instrument for longer thought: ~3 lines by default, grows as typed */
.composer textarea { flex:1; resize:vertical; min-height:3.4rem; max-height:14rem;
  font:inherit; line-height:1.5; background: var(--ground); color: var(--ink);
  border:1px solid var(--rule-strong); border-radius:3px; padding:.3rem .45rem; }
/* pending attachments, one chip each, removable until posted */
.cchips { display:flex; gap:.4rem; flex-wrap:wrap; padding:0 .7rem; }
.cchips:empty { display:none; }
.cchips { padding-top:.3rem; }
.cchips .chip { display:inline-flex; align-items:center; gap:.3rem;
  border:1px solid var(--rule-strong); background: var(--raised);
  padding:.05rem .5rem; font-size:.75rem; color: var(--ink); }
.cchips .chip button { background:none; border:0; color: var(--ink-mute);
  cursor:pointer; font:inherit; padding:0; }
.cchips .chip button:hover { color: var(--sev-hi); }
/* A refusal has to be readable without hovering. */
.composer-error {
  padding:.4rem .7rem; border-top:1px solid var(--sev-hi);
  color: var(--sev-hi); font-size:.78rem; line-height:1.4;
}
.composer input[name=text], .composer #ctext { flex:1; }
.composer .tok { width:16rem; }
body[data-signing="false"] .composer .tok { display:none; }

/* ---- search ---- */
.srow { grid-template-columns: var(--col-folio) 3rem 3rem var(--col-author) var(--col-kind) 1fr; }
.rank { color: var(--ink-faint); text-align:right; font-variant-numeric: tabular-nums; }
.rank.vec { color: var(--ink-faint); opacity:.5; }
.empty { padding:2rem .7rem; color: var(--ink-faint); }

/* ---- founder claim card: shown once, to the first seat on a fresh hub ---- */
.claim { margin:1.2rem auto; width:min(34rem, 92vw); background: var(--raised);
  border:1px solid var(--rule-strong); padding:1rem 1.2rem; }
.claim h2 { font-size:.95rem; color: var(--ink-strong); margin:0 0 .4rem; }
.claim p { color: var(--ink-mute); font-size:.82rem; margin:.3rem 0; line-height:1.5; }
.claim input, .claim select { font:inherit; padding:.35rem .5rem;
  background: var(--panel); color: var(--ink); border:1px solid var(--rule-strong); }
.claim .invite-name { margin:.6rem 0; }
.claim-actions { display:flex; gap:.5rem; margin-top:.6rem; flex-wrap:wrap; }
.claim-actions button { font:inherit; font-size:.82rem; padding:.3rem .7rem; cursor:pointer;
  background: var(--raised); color: var(--ink); border:1px solid var(--rule-strong); }
.claim-actions button:hover { border-color: var(--accent); }
.claim-err { color: var(--sev-hi); min-height:1.2em; font-size:.8rem; }

/* ---- settings dialog: the ledger's back office, same hairlines ---- */
.gear { display:inline-flex; align-items:center; }
.gear svg { display:block; }
.settings {
  background: var(--panel); color: var(--ink);
  border:1px solid var(--rule-strong); border-radius:0; padding:0;
  width:min(36rem, 92vw); font: inherit;
}
.settings::backdrop { background: var(--scrim); }
.set { display:grid; grid-template-columns: 9rem 1fr; min-height:19rem; }
.set-nav {
  display:flex; flex-direction:column; align-items:stretch; gap:.1rem;
  border-right:1px solid var(--rule); padding:.6rem 0;
  background: var(--band);
}
.set-h {
  color: var(--ink-faint); font-size:.72rem; letter-spacing:.08em;
  padding:.5rem .8rem .15rem;
}
.set-nav button {
  background:none; border:0; color: var(--ink-mute);
  text-align:left; padding:.28rem .8rem; cursor:pointer; font:inherit;
}
.set-nav button:hover { color: var(--ink-strong); }
.set-nav button.sel {
  color: var(--ink-strong); background: var(--panel);
  border-left:2px solid var(--accent); padding-left:calc(.8rem - 2px);
}
.set-spacer { flex:1; }
.set-close { color: var(--ink-faint); font-size:.78rem; }
.set-panels { padding:1rem 1.2rem; min-width:0; }
.set-panels h2 { font-size:.9rem; color: var(--ink-strong); margin:0 0 .6rem; }
.set-panels p, .set-panels label { color: var(--ink-mute); margin:.3rem 0; }
.set-panels form { display:flex; gap:.3rem; margin:.6rem 0; }
.set-panels input, .set-panels select {
  flex:1; background: var(--raised); color: var(--ink);
  border:1px solid var(--rule-strong); padding:.3rem .5rem; font:inherit;
}
.set-panels button[type=submit] {
  background: var(--raised); color: var(--ink);
  border:1px solid var(--rule-strong); padding:.3rem .7rem; cursor:pointer; font:inherit;
}
.set-panels button[type=submit]:hover { border-color: var(--accent); }
.set-note { font-size:.78rem; color: var(--ink-faint); }
/* The invite form stacks: an actor input, a superuser toggle, a room-scope
   disclosure, then the mint button — one control per line. The panel's default
   row-flex is for the two-field room form; here each child owns its own row. */
.invite-form { flex-direction:column; align-items:stretch; gap:.5rem; }
.invite-form > input { flex:none; width:100%; }
.invite-form > button[type=submit] { align-self:flex-start; }
.invite-scope > summary { cursor:pointer; color: var(--ink-mute); font-size:.8rem; margin:.15rem 0; }
.invite-scope[open] > summary { color: var(--ink); margin-bottom:.35rem; }
.set-rooms { display:flex; flex-wrap:wrap; gap:.2rem 1rem; margin:.3rem 0; }
.set-rooms label { display:flex; align-items:center; gap:.35rem; color: var(--ink); font-size:.82rem; cursor:pointer; }
.invite-super-row { display:flex; align-items:center; gap:.4rem; color: var(--ink-mute); font-size:.82rem; cursor:pointer; }
.invite-super-row input, .set-rooms input { flex:none; width:auto; }
/* the seat name: a namespace picker + a bare-name input, joined as one field */
.invite-name { display:flex; gap:0; }
.invite-name select { flex:none; width:auto; border-right:0; border-radius:0; }
.invite-name input { flex:1; }
/* create a room inline from the picker */
.invite-newroom { display:flex; gap:.3rem; margin:.4rem 0 .2rem; }
.invite-newroom input { flex:1; }
.invite-newroom button { background: var(--raised); color: var(--ink);
  border:1px solid var(--rule-strong); padding:.25rem .6rem; cursor:pointer; font:inherit; font-size:.8rem; }
.invite-newroom button:hover { border-color: var(--accent); }
/* the copy button matches the mint button and does not resize when its label
   changes to "copied" — an inline-block with a min-width holds its shape */
.invite-copy { display:inline-block; margin-top:.5rem; background: var(--raised);
  color: var(--ink); border:1px solid var(--rule-strong); padding:.3rem .7rem;
  cursor:pointer; font:inherit; }
.invite-copy:hover { border-color: var(--accent); }
.set-out { display:block; margin-top:.5rem; word-break:break-all; color: var(--ok); }
.set-out:empty { display:none; }
.set-list { margin:.4rem 0; padding:0; list-style:none; }
.set-list li, .set-seat {
  display:flex; justify-content:space-between; gap:.6rem;
  padding:.22rem 0; border-bottom:1px solid var(--rule);
}
.set-mute { color: var(--ink-faint); }
@media (max-width: 640px) { .set { grid-template-columns: 1fr; } .set-nav { flex-direction:row; flex-wrap:wrap; border-right:0; border-bottom:1px solid var(--rule); } }

@media (prefers-reduced-motion: reduce) { * { transition:none !important; animation:none !important; } }
@media (max-width: 640px) {
  :root { --col-folio:3.4rem; --col-author:4.5rem; --col-kind:1.4rem; }
  body { font-size:12px; }
}
`

// liveScript is a ~30-line client for the same SSE patch protocol datastar
// consumes (event: datastar-patch-elements, mode/selector/elements lines). It
// is vendored rather than loaded from a CDN so the binary has no network
// dependency to boot; swapping in the datastar runtime is a script-tag change,
// because the wire format is already its own.
const liveScript = `
<script>
(function(){
  var body = document.getElementById('ledger-body');
  if (!body || !window.EventSource) return;
  var room = document.body.getAttribute('data-room') || 'core';
  // A search page is this same view with one more filter, so it uses this same
  // script. Bespoke JS on one page is a second place for the resume rule, the
  // dedupe rule and the scroll rule to drift.
  var query = document.body.getAttribute('data-q') || '';
  // Resume after the last folio the server already rendered. Without this the
  // stream replays the whole backlog and appends duplicates of every row that
  // was collapsed into a carried-forward line (those have no DOM node to
  // dedupe against).
  var last = document.body.getAttribute('data-head') || '0';
  var es = null;

  // The left column is the reader's clock: today shows the time, older rows
  // the date; the full local timestamp (and the folio, for refs) rides the
  // hover tip. The server sent UTC facts; formatting is the browser's job.
  function fmtTime(iso){
    var d = new Date(iso);
    if (isNaN(d)) return '';
    var now = new Date();
    if (d.toDateString() === now.toDateString())
      return d.toLocaleTimeString([], {hour:'numeric', minute:'2-digit'});
    var day = d.toLocaleDateString([], {month:'short', day:'numeric'});
    return d.getFullYear() === now.getFullYear() ? day : day + ' ' + d.getFullYear();
  }
  function stamp(root){
    [].forEach.call(root.querySelectorAll('.row[data-ts]:not([data-stamped])'), function(r){
      r.setAttribute('data-stamped','1');
      var cell = r.querySelector('.folio');
      if (!cell) return;
      var iso = r.getAttribute('data-ts');
      var label = fmtTime(iso);
      if (label) cell.textContent = label;
      cell.setAttribute('data-tip',
        (cell.getAttribute('data-tip')||'') + ' · ' + new Date(iso).toLocaleString());
    });
  }
  // Consecutive rows from one author group: the name prints once, the rows
  // keep their height (density is a commitment). Addressed rows always name
  // their author — attention stands alone.
  function groupAuthors(){
    if (!document.body.classList.contains('railed')) return;
    var prev = null;
    [].forEach.call(body.querySelectorAll('.row'), function(r){
      var a = r.querySelector('.author');
      if (!a) { prev = null; return; }
      if (prev === a.textContent && !r.classList.contains('addressed')) r.classList.add('cont');
      else r.classList.remove('cont');
      prev = a.textContent;
    });
  }
  // Unread marks: a room whose head moved past what this browser last saw
  // there. The current room is always caught up by being looked at.
  function paintRail(){
    var rail = document.querySelector('nav.rail');
    if (!rail) return;
    [].forEach.call(rail.querySelectorAll('a[data-room]'), function(a){
      var rm = a.getAttribute('data-room');
      var head = parseInt(a.getAttribute('data-head')||'0', 10);
      var seen = parseInt(localStorage.getItem('comms.seen.'+rm)||'0', 10);
      if (rm === room){
        if (head > seen) localStorage.setItem('comms.seen.'+rm, String(head));
        a.classList.remove('has-unread');
        return;
      }
      a.classList.toggle('has-unread', head > seen);
    });
  }

  function handle(e){
    var mode='append', selector='#ledger-body', html=[];
    e.data.split('\n').forEach(function(line){
      if (line.indexOf('mode ')===0) mode = line.slice(5).trim();
      else if (line.indexOf('selector ')===0) selector = line.slice(9).trim();
      else if (line.indexOf('elements ')===0) html.push(line.slice(9));
    });
    var target = document.querySelector(selector);
    if (!target) return;
    var seq = e.lastEventId;
    if (seq) last = seq;
    if (seq && target.querySelector('[data-seq="'+seq+'"]')) return; // resume overlap
    // Measure before inserting: whether the reader was at the bottom is a
    // fact about the view before the new row grew it.
    var main = document.querySelector('main.ledger');
    var nearBottom = main && (main.scrollHeight - main.scrollTop - main.clientHeight < 48);
    if (mode==='append') target.insertAdjacentHTML('beforeend', html.join('\n'));
    else target.innerHTML = html.join('\n');
    if (target === body){
      if (seq) localStorage.setItem('comms.seen.'+room, seq);
      stamp(body);
      groupAuthors();
      // Follow the tail only for a reader already at it. Yanking the view on
      // every append made history unreadable in a live room.
      if (mode==='append' && nearBottom) main.scrollTop = main.scrollHeight;
      return;
    }
    // A rail replacement repaints unread marks and never scrolls.
    paintRail();
  }
  function connect(){
    es = new EventSource('/stream?room=' + encodeURIComponent(room) +
                         '&after=' + encodeURIComponent(last) +
                         (query ? '&q=' + encodeURIComponent(query) : ''));
    es.addEventListener('datastar-patch-elements', handle);
    // EventSource retries transient drops itself; a fatal close (the 401
    // after a hub restart — sessions are in-memory) previously froze the
    // ledger silently while posting kept working. Keep re-dialling: when the
    // hub (or a fresh session via reload) is back, the stream resumes from
    // the last folio.
    es.onerror = function(){
      if (es && es.readyState === 2) {
        es = null;
        var bar = document.getElementById('composer-error');
        if (bar) { bar.textContent = 'live updates interrupted — retrying; reload if this persists'; bar.hidden = false; }
        setTimeout(function(){ if (!document.hidden && !es) connect(); }, 5000);
      }
    };
  }
  // The stream socket belongs to the visible tab only. Browsers cap HTTP/1.1
  // at ~6 connections per host, and a hidden tab holding its stream open
  // starves the pool: the seventh tab's page load queues forever behind them —
  // a "hang" with a perfectly healthy server. Hidden tabs hand the socket
  // back and resume from the last folio they saw when shown again.
  // ponytail: many visible windows can still fill the pool; HTTP/2 lifts it.
  document.addEventListener('visibilitychange', function(){
    if (document.hidden) { if (es) { es.close(); es = null; } }
    else if (!es) connect();
  });
  stamp(document);
  groupAuthors();
  paintRail();
  if (!document.hidden) connect();
})();
</script>`

// gearGlyph is drawn, not typed: the header's one icon, in the ledger's own
// hairline weight.
const gearGlyph = `<svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.2" aria-hidden="true"><circle cx="8" cy="8" r="2.4"/><path d="M8 1.2v2.1M8 12.7v2.1M1.2 8h2.1M12.7 8h2.1M3.2 3.2l1.5 1.5M11.3 11.3l1.5 1.5M12.8 3.2l-1.5 1.5M4.7 11.3l-1.5 1.5"/></svg>`

// settingsModal is the gear's dialog: a left rail of sections, panels on the
// right. The admin rail entries render hidden and are shown only when GET
// /caps says the seat holds the invite capability — visibility is convenience;
// every action re-proves the capability on a signed request server-side.
const settingsModal = `
<dialog id="settings" class="settings" aria-label="settings">
  <div class="set">
    <nav class="set-nav">
      <span class="set-h">general</span>
      <button type="button" data-panel="theme" class="sel">theme</button>
      <span class="set-h set-admin" hidden>admin</span>
      <button type="button" data-panel="invite" class="set-admin" hidden>invite</button>
      <button type="button" data-panel="rooms" class="set-admin" hidden>rooms</button>
      <button type="button" data-panel="seats" class="set-admin" hidden>seats</button>
      <span class="set-spacer"></span>
      <button type="button" id="set-close" class="set-close">close esc</button>
    </nav>
    <div class="set-panels">
      <section data-panel="theme">
        <h2>you</h2>
        <p class="set-note"><label for="actor">acting seat </label>
        <select id="actor" aria-label="acting seat"></select></p>
        <p class="set-note">Identity is your enrolled key; the page derives it.
        Switching seats here is an operator act — posts sign as whichever seat
        holds a key in this browser.</p>
        <h2>theme</h2>
        <label for="themesel">this browser renders the ledger in</label>
        <select id="themesel">
          <option value="dark">dark</option>
          <option value="light">light</option>
          <option value="slate">slate</option>
        </select>
        <p class="set-note">t cycles themes from the keyboard.</p>
        <p class="set-note">comms is open source — <a href="https://github.com/escherize/comms" target="_blank" rel="noopener">github.com/escherize/comms</a></p>
      </section>
      <section data-panel="invite" hidden>
        <h2>invite a seat</h2>
        <p>Mints a one-use enrolment token, signed by your key. Hand it over
        out of band; it is the whole credential.</p>
        <form id="invite-form" class="invite-form">
          <div class="invite-name">
            <select id="invite-kind" aria-label="seat kind">
              <option value="human">human:</option>
              <option value="agent">agent:</option>
            </select>
            <input id="invite-actor" placeholder="sarah, or you/claude-1" autocomplete="off">
          </div>
          <label id="invite-super-row" class="invite-super-row"><input type="checkbox" id="invite-super"> superuser (all rooms, and can invite others)</label>
          <details id="invite-scope" class="invite-scope">
            <summary>all rooms &middot; scope to specific rooms &rarr;</summary>
            <div id="invite-rooms" class="set-rooms"></div>
            <div id="invite-newroom" class="invite-newroom">
              <input id="invite-newroom-name" placeholder="+ new room" autocomplete="off">
              <button type="button" id="invite-newroom-go">create</button>
            </div>
            <p class="set-note">Scoping to specific rooms turns on read sessions
            for the whole hub — members sign in to read.</p>
          </details>
          <button type="submit">mint token</button>
        </form>
        <output id="invite-out" class="set-out"></output>
        <button type="button" id="invite-copy" class="invite-copy" hidden>copy prompt for the agent</button>
      </section>
      <section data-panel="rooms" hidden>
        <h2>rooms</h2>
        <ul id="rooms-list" class="set-list"></ul>
        <form id="room-form">
          <input id="room-name" placeholder="new room: a-z 0-9 - _" autocomplete="off">
          <button type="submit">create</button>
        </form>
        <p class="set-note">Rooms are created, never destroyed — the log is
        append-only, and a room's history outlives the wish to tidy it.</p>
        <output id="room-out" class="set-out"></output>
      </section>
      <section data-panel="seats" hidden>
        <h2>seats</h2>
        <div id="seats-list" class="set-list"></div>
        <p class="set-note">Revoking a key is an incident action, deliberately
        not a click: on the hub box, comms --h-server lists the operator
        surface.</p>
      </section>
    </div>
  </div>
</dialog>`

// settingsScript wires the dialog. It signs admin actions with the same
// IndexedDB key the composer enrols — marshal once, sign that string, send
// that string, the one rule every signer in this system follows.
const settingsScript = `
<script>
(function(){
  var dlg=document.getElementById('settings'), gear=document.getElementById('gear');
  if(!dlg||!gear) return;

  var DB='comms.keys', STORE='keys';
  function idb(){ return new Promise(function(res,rej){
    var r=indexedDB.open(DB,1);
    r.onupgradeneeded=function(){ r.result.createObjectStore(STORE); };
    r.onsuccess=function(){ res(r.result); }; r.onerror=function(){ rej(r.error); };
  });}
  function idbGet(k){ return idb().then(function(db){ return new Promise(function(res,rej){
    var t=db.transaction(STORE,'readonly').objectStore(STORE).get(k);
    t.onsuccess=function(){ res(t.result); }; t.onerror=function(){ rej(t.error); };
  });});}
  function hex(buf){ return Array.prototype.map.call(new Uint8Array(buf),
    function(b){ return ('0'+b.toString(16)).slice(-2); }).join(''); }
  function me(){ var a=document.getElementById('actor'); return a?a.value:''; }

  function signedPost(path, obj){
    var body=JSON.stringify(obj);
    return idbGet(me()).then(function(pair){
      if(!pair) throw new Error('no key in this browser for '+me()+' — post once to enrol, then retry');
      return crypto.subtle.sign({name:'Ed25519'}, pair.privateKey,
          new TextEncoder().encode(body))
        .then(function(sig){ return fetch(path,{method:'POST',
          headers:{'Content-Type':'application/json','X-Signature':hex(sig)},
          body:body}); });
    }).then(function(r){ return r.json().then(function(j){
      if(!r.ok) throw new Error(j.detail||j.invariant||'refused'); return j; }); });
  }

  function show(name){
    var els=dlg.querySelectorAll('[data-panel]');
    for(var i=0;i<els.length;i++){
      var el=els[i];
      if(el.tagName==='BUTTON') el.classList.toggle('sel', el.dataset.panel===name);
      else el.hidden = el.dataset.panel!==name;
    }
    if(name==='rooms') loadRooms();
    if(name==='seats') loadSeats();
    if(name==='invite') loadInviteRooms();
  }
  dlg.addEventListener('click', function(e){
    var b=e.target.closest('button[data-panel]');
    if(b) show(b.dataset.panel);
  });

  gear.addEventListener('click', function(){
    dlg.showModal();
    var sel=document.getElementById('themesel');
    sel.value=document.documentElement.getAttribute('data-theme')||'dark';
    fetch('/caps?actor='+encodeURIComponent(me()))
      .then(function(r){ return r.json(); })
      .then(function(j){
        var admin=(j.capabilities||[]).indexOf('invite')>=0;
        var els=dlg.querySelectorAll('.set-admin');
        for(var i=0;i<els.length;i++) els[i].hidden=!admin;
      }).catch(function(){});
  });
  document.getElementById('set-close').addEventListener('click', function(){ dlg.close(); });
  // The claim card's "add rooms" / "invite someone" doors open these panels
  // rather than duplicating their forms.
  window.commsSettings=function(panel){ gear.click(); show(panel); };
  dlg.addEventListener('click', function(e){ if(e.target===dlg) dlg.close(); });

  document.getElementById('themesel').addEventListener('change', function(e){
    document.documentElement.setAttribute('data-theme', e.target.value);
    localStorage.setItem('comms.theme', e.target.value);
  });

  // The checked rooms, or 'all' when the picker is untouched — the common case
  // stays one click. A checkbox list confined to the minter's own rooms means
  // the server-side subset rule can never be tripped from the UI.
  function chosenScope(){
    var su=document.getElementById('invite-super');
    if(su && su.checked) return 'superuser';
    var box=document.getElementById('invite-rooms');
    var rooms=[];
    if(box){ box.querySelectorAll('input').forEach(function(b){ if(b.checked) rooms.push(b.value); }); }
    return rooms.length===0 ? 'all' : rooms.join(',');
  }
  function scopeLabel(scope){
    if(scope==='superuser') return 'superuser — all rooms + can invite';
    return (!scope||scope==='all')?'all rooms':scope;
  }
  // Superuser is all-rooms by definition, so checking it disables the
  // room-scope picker — the two are mutually exclusive.
  (function(){
    var su=document.getElementById('invite-super'), sc=document.getElementById('invite-scope');
    if(su&&sc) su.addEventListener('change', function(){ sc.style.display = su.checked ? 'none' : ''; });
  })();

  // The seat's namespace (human:/agent:) is a picker, not something to type —
  // it decides how the seat's posts are read, so it is a choice, not prose. If
  // someone pastes a fully-qualified seat anyway, respect it rather than
  // double-prefixing.
  function chosenSeat(){
    var name=document.getElementById('invite-actor').value.trim();
    if(!name) return '';
    if(name.indexOf('human:')===0 || name.indexOf('agent:')===0) return name;
    var kind=document.getElementById('invite-kind').value;
    return kind+':'+name;
  }

  document.getElementById('invite-form').addEventListener('submit', function(e){
    e.preventDefault();
    var out=document.getElementById('invite-out');
    var target=chosenSeat();
    if(!target){ out.textContent='name the seat to invite'; return; }
    var scope=chosenScope();
    out.textContent='minting…';
    document.getElementById('invite-copy').hidden=true;
    signedPost('/invite',{actor:target, as:me(), rooms:scope})
      .then(function(j){
        var granted=j.scope||scope;
        out.textContent='token for '+target+' ('+scopeLabel(granted)+'): '+j.token+'  (one use — hand it over out of band)';
        var copy=document.getElementById('invite-copy');
        var isAgent=target.indexOf('agent:')===0;
        copy.hidden=false;
        copy.textContent=isAgent ? 'copy prompt for the agent' : 'copy invite for the human';
        copy.onclick=function(){
          var text=invitePrompt(target, j.token, granted);
          navigator.clipboard.writeText(text).then(
            function(){ copy.textContent=isAgent ? 'copied — paste it into the agent\'s session' : 'copied — send it to them'; },
            function(){ out.textContent=text; });
        };
      })
      .catch(function(ex){ out.textContent=ex.message; });
  });

  // The room picker is filled from /rooms — which the server already filters to
  // the minter's own rooms — so a scoped admin can only offer rooms it holds.
  // preselect keeps a set of rooms checked across a reload (creating a room
  // mid-flow must not clear the boxes already ticked).
  function loadInviteRooms(preselect){
    var box=document.getElementById('invite-rooms');
    if(!box) return;
    var keep={};
    box.querySelectorAll('input').forEach(function(b){ if(b.checked) keep[b.value]=true; });
    (preselect||[]).forEach(function(r){ keep[r]=true; });
    fetch('/rooms',{headers:{'Accept':'application/json'}})
      .then(function(r){ return r.json(); })
      .then(function(j){
        box.innerHTML='';
        (j.rooms||[]).forEach(function(room){
          var lbl=document.createElement('label');
          var cb=document.createElement('input');
          cb.type='checkbox'; cb.value=room; cb.checked=!!keep[room];
          lbl.appendChild(cb);
          lbl.appendChild(document.createTextNode(' '+room));
          box.appendChild(lbl);
        });
      }).catch(function(){});
  }

  // Create a room without leaving the invite panel, then re-check the boxes so
  // the new room is selected and nothing already ticked is lost.
  (function(){
    var btn=document.getElementById('invite-newroom-go');
    var inp=document.getElementById('invite-newroom-name');
    if(!btn||!inp) return;
    function make(){
      var name=inp.value.trim();
      if(!name) return;
      signedPost('/rooms',{name:name, as:me()})
        .then(function(){ inp.value=''; loadInviteRooms([name]); })
        .catch(function(ex){ document.getElementById('invite-out').textContent=ex.message; });
    }
    btn.addEventListener('click', make);
    inp.addEventListener('keydown', function(e){ if(e.key==='Enter'){ e.preventDefault(); make(); } });
  })();

  // The prompt hands an agent everything between "given a token" and "posting
  // usefully". It deliberately teaches only the connection; the room's rules
  // live in the skill the binary serves, so they cannot drift from the code.
  function botPrompt(actor, token, scope){
    var rooms=(!scope||scope==='all')?'all rooms':scope;
    return [
      'You have a seat on a comms hub: this team\'s shared room for humans and AI agents.',
      '',
      'Seat:  '+actor,
      'Rooms: '+rooms,
      'URL:   '+location.origin,
      '',
      '1. Install (onto PATH):',
      '',
      '    curl -fsSL '+location.origin+'/install | sh',
      '',
      '2. Join — run this at your project\'s root, then restart your session',
      '   (it enrols your seat and wires the room into your harness\'s turn loop):',
      '',
      '    comms join \''+location.origin+'/#setup='+token+'\'',
      '',
      '3. Learn the tool:',
      '',
      '    comms --help',
      '    comms skill comms',
      '',
      'Every verb answers --help.'
    ].join('\n');
  }

  // The human version: not CLI assembly, just the two ways a person joins —
  // click the setup link, or one enrol command if they prefer the terminal.
  function humanBlurb(actor, token, scope){
    var rooms=(scope==='superuser')?'all rooms, and can invite others (superuser)':((!scope||scope==='all')?'all rooms':scope);
    return [
      'You\'ve been invited to a comms hub — a shared room where the team and their',
      'AI agents post signed, permanent, typed notes.',
      '',
      'Seat:  '+actor,
      'Rooms: '+rooms,
      '',
      'Join in your browser:',
      '  '+location.origin+'/#setup='+token,
      '  Open it, confirm your name, and you\'re in.'
    ].join('\n');
  }

  // Pick the right prompt for the seat: agents get the harness assembly steps,
  // humans get the join-in-a-browser blurb.
  function invitePrompt(actor, token, scope){
    return actor.indexOf('agent:')===0
      ? botPrompt(actor, token, scope)
      : humanBlurb(actor, token, scope);
  }

  function loadRooms(){
    fetch('/rooms',{headers:{'Accept':'application/json'}})
      .then(function(r){ return r.json(); })
      .then(function(j){
        var ul=document.getElementById('rooms-list'); ul.innerHTML='';
        (j.rooms||[]).forEach(function(room){
          var li=document.createElement('li'); li.textContent=room; ul.appendChild(li);
        });
      }).catch(function(){});
  }
  document.getElementById('room-form').addEventListener('submit', function(e){
    e.preventDefault();
    var out=document.getElementById('room-out');
    var name=document.getElementById('room-name').value.trim();
    if(!name){ out.textContent='name the room'; return; }
    signedPost('/rooms',{name:name, as:me()})
      .then(function(j){
        out.textContent=j.outcome==='exists' ? name+' already exists' : 'created '+name;
        loadRooms();
      })
      .catch(function(ex){ out.textContent=ex.message; });
  });

  function loadSeats(){
    fetch('/actors',{headers:{'Accept':'application/json'}})
      .then(function(r){ return r.json(); })
      .then(function(j){
        var div=document.getElementById('seats-list'); div.innerHTML='';
        (j.actors||[]).forEach(function(a){
          var row=document.createElement('div'); row.className='set-seat';
          var name=document.createElement('span'); name.textContent=a.actor||a.name||JSON.stringify(a);
          var st=document.createElement('span'); st.className='set-mute';
          st.textContent=a.key_status||a.status||'';
          row.appendChild(name); row.appendChild(st); div.appendChild(row);
        });
      }).catch(function(){});
  }
})();
</script>`

const themeScript = `
<script>
(function(){
  var k='comms.theme', s=localStorage.getItem(k);
  if(s) document.documentElement.setAttribute('data-theme', s);
  window.cycleTheme=function(){
    var order=['dark','light','slate'],
        cur=document.documentElement.getAttribute('data-theme')||'dark',
        next=order[(order.indexOf(cur)+1)%order.length];
    document.documentElement.setAttribute('data-theme', next);
    localStorage.setItem(k, next);
  };
  // Expansion: the carried-forward control toggles its own group. Rendered
  // hidden rather than fetched, so this is a class flip with no round trip.
  document.addEventListener('click', function(e){
    var b = e.target.closest && e.target.closest('.carried');
    if(!b) return;
    var body = document.getElementById(b.getAttribute('aria-controls'));
    if(!body) return;
    var open = b.getAttribute('aria-expanded') === 'true';
    b.setAttribute('aria-expanded', open ? 'false' : 'true');
    body.hidden = open;
  });

  document.addEventListener('keydown', function(e){
    if(e.target.tagName==='INPUT'||e.target.tagName==='SELECT'||e.target.tagName==='TEXTAREA') return;
    // A bare letter is a hotkey; the same letter with a modifier belongs to the
    // browser. Without this, cmd-C focused the composer and swallowed the copy,
    // which is the kind of theft a reader blames on their own hands.
    if(e.metaKey || e.ctrlKey || e.altKey) return;
    // And a selection means the reader is reading, not navigating.
    var sel = window.getSelection && window.getSelection();
    if(sel && !sel.isCollapsed) return;
    if(e.key==='/'){ e.preventDefault(); var q=document.getElementById('q'); if(q) q.focus(); }
    if(e.key==='t'){ cycleTheme(); }
    if(e.key==='c'){ var c=document.getElementById('ctext'); if(c){ e.preventDefault(); c.focus(); } }
    // Room switching: [ and ] move through the nav without a mouse.
    if(e.key==='[' || e.key===']'){
      var links=[].slice.call(document.querySelectorAll('nav.rail a, header nav a'));
      var at=links.findIndex(function(a){ return a.classList.contains('sel'); });
      if(links.length>1 && at>=0){
        var next=(at + (e.key===']' ? 1 : links.length-1)) % links.length;
        window.location = links[next].getAttribute('href');
      }
    }
    // e expands or collapses every carried-forward group in the room.
    if(e.key==='e'){
      var any=document.querySelector('.carried[aria-expanded="false"]');
      [].forEach.call(document.querySelectorAll('.carried'), function(b){
        var body=document.getElementById(b.getAttribute('aria-controls'));
        if(!body) return;
        b.setAttribute('aria-expanded', any ? 'true' : 'false');
        body.hidden = !any;
      });
    }
  });
})();
</script>`

const roomHTML = `<!doctype html>
<html lang="en" data-theme="dark">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{ROOM}} · comms</title>
<style>` + baseCSS + `</style>
</head>
<body class="railed" data-room="{{ROOM}}" data-head="{{HEAD}}" data-signing="{{SIGNING}}">
<!--
THESIS: The room IS the book. Every event is a numbered entry in an append-only
journal; nothing is erased, corrections are new entries, and state as of any row
is visible in the margin. It refuses the chat timeline of avatars and bubbles,
because this is a record, not a conversation.
OWN-WORLD: Ledger grammar, editor skin. Near-neutral ground in the VS Code
register; ambient rows band by luminance, never hue; hairline rules; chroma
rationed to accent-for-addressed and red-for-severity. Folio (seq) right-aligned
in tabular figures; a tick column closes each row. No cards, bubbles, or avatars.
STORY: The reader sees agents have been busy (banded rows, a carried-forward
line) and that exactly one thing wants them (an accent-ruled row breaking the
rhythm), then expands, answers, and trusts nothing was quietly removed.
FIRST VIEWPORT: folio · author · kind · entry · ✓, ambient banded, consecutive
ambient collapsed to "carried forward — N entries", addressed breaking the band
with an accent gutter and full-width body, redactions struck with hash attested.
Foot is a running balance; composer below it.
FORM: Double-entry ledger — candidate 7 of the grounded list, seed ac01beef,
skinned quiet per the user's direction.
FINISH: unreviewed and undocumented is unfinished; this build ends with the
finish review, the verdict, and DESIGN.md.
-->
<header>
  <span class="brand">comms</span>
  <span class="spacer"></span>
  <form action="/search" method="get">
    <input id="q" name="q" placeholder="search  /" autocomplete="off">
  </form>
  <span class="who me" id="me" title="your enrolled seat — identity is derived from your key, not chosen">…</span>
  <button type="button" id="gear" class="gear" title="settings" aria-haspopup="dialog" aria-controls="settings">` + gearGlyph + `</button>
</header>

<nav class="rail" id="rail" aria-label="rooms">{{RAIL}}</nav>

<main class="ledger">
  <div class="head">
    <div>when</div><div>author</div><div title="kind">·</div><div>entry</div><div>✓</div>
  </div>
  <div id="ledger-body">{{ROWS}}</div>
</main>

<footer>
  <div class="balance">
    <span>room <b>{{ROOM}}</b></span>
    <span>ambient <b>{{AMBIENT}}</b></span>
    <span>addressed open <b>{{ADDRESSED}}</b></span>
    <span>index current <b>read-your-writes</b></span>
    <span>balance at folio <b>{{HEAD}}</b></span>
    {{PROGRESS}}
  </div>
  <div id="composer-error" class="composer-error" hidden></div>
  <div id="cchips" class="cchips"></div>
  <form class="composer" id="composer">
    <select name="kind" id="ckind" aria-label="entry kind">
      <option value="chat">chat</option>
      <option value="finding">finding</option>
      <option value="til">til</option>
      <option value="status">status</option>
    </select>
    <textarea id="ctext" name="text" rows="3" placeholder="entry, or /finding /til /status /ask /answer /handoff /pr — enter posts, shift+enter for a new line  (c to focus)" aria-label="entry"></textarea>
    <input type="file" id="cfile" accept=".md,.markdown,.txt,text/markdown,text/plain" multiple hidden>
    <button type="button" id="cattach" title="attach a markdown file">▤</button>
    <input id="enroltoken" class="tok" placeholder="enrolment token (first post only)" aria-label="enrolment token" autocomplete="off">
    <button type="submit">post</button>
  </form>
</footer>
` + settingsModal + liveScript + composeScript + themeScript + onboardScript + settingsScript + `
</body>
</html>`

// onboardScript owns who the composer posts as. The actor list is the live
// roster, not a shipped guess; "new seat…" is how a name that is not enrolled
// yet gets chosen (an invited seat about to redeem its token); and #setup=
// is the handoff from a first-run serve, which mints a token before anyone
// exists to name, so the naming happens here.
const onboardScript = `
<script>
(function(){
  var a=document.getElementById('actor');
  if(!a) return;
  var ak='comms.actor';

  function opt(v,label){
    var o=document.createElement('option');
    o.value=v; o.textContent=label||v.replace(/^(human|agent):/,'');
    return o;
  }
  // The header chip derives identity; nobody picks who they are up there.
  var meChip=document.getElementById('me');
  function paintMe(){
    if(!meChip) return;
    var v=a.value;
    meChip.textContent=(v && v!=='__new__') ? v : 'no seat yet';
  }
  function setActor(v){
    if(![].some.call(a.options,function(o){return o.value===v;}))
      a.insertBefore(opt(v), a.lastElementChild);
    a.value=v; localStorage.setItem(ak,v);
    paintMe();
    // The composer re-decides whether the token field is needed for this seat.
    if(window.commsTokenVis) window.commsTokenVis();
  }
  function note(msg){
    var bar=document.getElementById('composer-error');
    if(bar){ bar.textContent=msg; bar.hidden=false; }
  }

  var rosterLoaded=false;
  fetch('/actors',{headers:{'Accept':'application/json'}})
    .then(function(r){ return r.json(); })
    .then(function(j){
      rosterLoaded=true;
      (j.actors||[]).forEach(function(x){
        if(x.key_status!=='revoked' && x.key_status!=='compromised')
          a.appendChild(opt(x.actor));
      });
    })
    .catch(function(){})
    .then(function(){
      a.appendChild(opt('__new__','new seat…'));
      var saved=localStorage.getItem(ak);
      // Restore the remembered seat only if the live roster still has it: a
      // revoked seat, or one from another hub on this origin (localhost dev),
      // must not stay the default identity and fail every post. When the
      // roster fetch itself failed, keep the memory — offline is not revoked.
      if(saved && saved!=='__new__'){
        var known=[].some.call(a.options,function(o){ return o.value===saved; });
        if(known || !rosterLoaded) setActor(saved);
        else localStorage.removeItem(ak);
      }
      paintMe();
      setup();
    });

  // Naming happens in an inline input where the select was, never a modal
  // dialog: those block the renderer, and the page has a test saying so.
  function askName(cb){
    var box=document.createElement('input');
    box.className='tok'; box.value='human:'; box.autocomplete='off';
    box.placeholder='human:<you> or agent:<owner>/<name>';
    a.hidden=true; a.parentNode.insertBefore(box, a);
    box.focus(); box.setSelectionRange(box.value.length, box.value.length);
    var settled=false;
    function complete(name){ return name && name!=='human:' && name!=='agent:'; }
    function done(commit){
      if(settled) return; settled=true;
      var name=box.value.trim();
      box.remove(); a.hidden=false;
      if(commit && complete(name)) cb(name);
      else if(a.options.length) a.selectedIndex=0;
    }
    box.addEventListener('keydown', function(e){
      if(e.key==='Enter'){ e.preventDefault(); done(true); }
      if(e.key==='Escape'){ done(false); }
    });
    // Blur only commits a name the user actually finished typing. A blur firing
    // on page load (the composer stealing focus) with a bare "human:" left the
    // dropdown on the "__new__" placeholder, so the first post was refused
    // "no namespace" — the exact wall a newcomer hits on the setup link. An
    // incomplete blur now just leaves the box open to come back to.
    box.addEventListener('blur', function(){ if(complete(box.value.trim())) done(true); });
  }

  a.addEventListener('change', function(){
    paintMe();
    if(a.value!=='__new__'){ localStorage.setItem(ak, a.value); return; }
    askName(function(name){
      setActor(name);
      note('first post as '+name+' needs its enrolment token in the token field');
    });
  });

  // The composer's no-seat rescue lives in another IIFE; these are its door.
  // Without them the guard was an uncaught ReferenceError — the failure it
  // exists to prevent.
  window.commsAskName=askName;
  window.commsSetActor=setActor;
  window.commsNote=note;

  // A pasted token names its seat, so the dropdown follows the token: the
  // hub is asked whose it is and the actor is set to match, instead of the
  // person having to make two fields agree by hand.
  var tokenBox=document.getElementById('enroltoken');
  if(tokenBox) tokenBox.addEventListener('input', function(){
    var tok=tokenBox.value.trim();
    if(!/^[0-9a-f]{32}$/.test(tok)) return;
    fetch('/invites/whose',{method:'POST',
      headers:{'Content-Type':'application/json'},
      body:JSON.stringify({token:tok})})
      .then(function(r){ return r.ok ? r.json() : null; })
      .then(function(j){
        if(!j || !j.actor) return;
        if(j.actor==='*'){
          note('first-seat token — name yourself, then post anything to enrol');
          askName(function(name){ setActor(name); });
          return;
        }
        setActor(j.actor);
        note('this token enrols '+j.actor+' — post anything to claim the seat');
      })
      .catch(function(){});
  });

  // A setup link pasted into an already-open tab is a same-document fragment
  // navigation: no reload, no script re-run, nothing visibly happens. Reload
  // so the load path below handles it.
  window.addEventListener('hashchange', function(){
    if(/^#setup=/.test(location.hash)) location.reload();
  });

  function setup(){
    var m=location.hash.match(/^#setup=([0-9a-f]{32})$/);
    if(!m) return;
    var token=m[1];
    // The token has no business staying in the URL bar; it survives in the
    // serve output if this attempt is abandoned.
    history.replaceState(null,'',location.pathname);
    var tf=document.getElementById('enroltoken'); if(tf) tf.value=token;
    // A token in hand must be visible even for a seat that has a stale key.
    if(window.commsTokenVis) window.commsTokenVis();
    var focusComposer=function(){ var c=document.getElementById('ctext'); if(c) c.focus(); };
    // The token knows its seat — an invite for human:sarah names her — so ask
    // the hub and pre-fill it rather than making the person retype what the link
    // already carried. Only a bootstrap ('*') token has nobody to name yet, so
    // that one still asks.
    fetch('/invites/whose',{method:'POST',
      headers:{'Content-Type':'application/json'},
      body:JSON.stringify({token:token})})
      .then(function(r){ return r.ok ? r.json() : null; })
      .then(function(j){
        if(j && j.actor && j.actor!=='*'){
          setActor(j.actor);
          var rooms=(j.scope && j.scope!=='all') ? j.scope : 'all rooms';
          note('you are '+j.actor+' ('+rooms+') — post anything below and this browser is enrolled');
          focusComposer();
          return;
        }
        if(j && j.actor==='*'){
          // Bootstrap token: nobody holds a seat yet. The founder gets a claim
          // card, not a bare ledger — an explicit enrol beats the invisible
          // enrol-on-first-post, which left people unsure it had worked.
          claimCard(token);
          return;
        }
        // The lookup did not resolve: the redeemer names the seat by hand.
        note('name yourself, then post anything below; the first post enrols this browser');
        askName(function(name){
          setActor(name);
          note('seat '+name+' ready — post anything below; the first post enrols this browser');
          focusComposer();
        });
      })
      .catch(function(){
        note('name yourself, then post anything below to enrol this browser');
        askName(function(name){ setActor(name); focusComposer(); });
      });
  }

  // The founder flow: orientation, a name, one claim button that enrols on the
  // spot. It sits above the composer, never blocking it, and only ever renders
  // for a bootstrap token — which is single-use, so the card cannot reappear.
  function claimCard(token){
    var main=document.querySelector('main.ledger');
    if(!main) return;
    var card=document.createElement('section');
    card.className='claim';
    card.innerHTML=
      '<h2>claim this hub</h2>'+
      '<p>This is a comms hub — a shared room where a team and its AI agents '+
      'post signed, permanent, typed notes. Nobody holds a seat here yet; '+
      'claiming it makes you the owner.</p>'+
      '<div class="invite-name">'+
        '<select id="claim-kind" aria-label="seat kind">'+
          '<option value="human">human:</option><option value="agent">agent:</option>'+
        '</select>'+
        '<input id="claim-name" placeholder="your name, e.g. sarah" autocomplete="off">'+
      '</div>'+
      '<div class="claim-actions"><button id="claim-go">claim this hub</button></div>'+
      '<div class="claim-err" id="claim-err"></div>';
    main.insertBefore(card, main.firstChild);
    var nameEl=document.getElementById('claim-name');
    nameEl.focus();
    function claim(){
      var errEl=document.getElementById('claim-err');
      var bare=nameEl.value.trim();
      if(!bare){ errEl.textContent='name yourself'; return; }
      var name=(bare.indexOf('human:')===0||bare.indexOf('agent:')===0)
        ? bare : document.getElementById('claim-kind').value+':'+bare;
      var tf=document.getElementById('enroltoken'); if(tf) tf.value=token;
      setActor(name);
      errEl.textContent='claiming…';
      // commsKeyFor is the composer's enrol path: token in the field means
      // generate a fresh key, register it, clear the token. One enrol rule.
      window.commsKeyFor(name).then(function(){ claimed(card, name); })
        .catch(function(ex){ errEl.textContent=String(ex && ex.message || ex); });
    }
    document.getElementById('claim-go').addEventListener('click', claim);
    nameEl.addEventListener('keydown', function(e){
      if(e.key==='Enter'){ e.preventDefault(); claim(); } });
  }

  // Post-claim next steps: two optional doors into the settings panels the
  // owner now controls, and the biggest button is the one that just posts.
  function claimed(card, name){
    card.innerHTML=
      '<h2>the hub is yours</h2>'+
      '<p>You are '+name+'. Your key was created in this browser and never '+
      'leaves it — the token is spent and you will not need it again.</p>'+
      '<div class="claim-actions">'+
        '<button id="claim-rooms">add rooms</button>'+
        '<button id="claim-invite">invite someone</button>'+
        '<button id="claim-post">just post →</button>'+
      '</div>';
    document.getElementById('claim-rooms').addEventListener('click',
      function(){ window.commsSettings('rooms'); });
    document.getElementById('claim-invite').addEventListener('click',
      function(){ window.commsSettings('invite'); });
    document.getElementById('claim-post').addEventListener('click', function(){
      card.remove();
      var c=document.getElementById('ctext'); if(c) c.focus();
    });
  }
})();
</script>`

// composeScript turns the composer into a signed command submission.
//
// The private key is generated in the browser as NON-EXTRACTABLE and kept in
// IndexedDB, so it never becomes readable JavaScript and cannot be exfiltrated
// by script that reaches this page. Only the public half is ever sent, and only
// once, when the actor enrolls. The server stores public keys and verifies; it
// never sees a private key.
const composeScript = `
<script>
(function(){
  var f=document.getElementById('composer');
  if(!f) return;

  // The composer is a place to think: ~3 lines tall, grows as typed. Enter
  // posts, shift+enter breaks a line — the convention every chat trained.
  var ta=document.getElementById('ctext');
  if(ta){
    ta.addEventListener('keydown', function(e){
      if(e.key==='Enter' && !e.shiftKey){ e.preventDefault(); f.requestSubmit(); }
    });
    ta.addEventListener('input', function(){
      ta.style.height='auto';
      ta.style.height=Math.min(ta.scrollHeight+2, 224)+'px';
    });
  }

  // Attachments: pick a markdown file, it uploads content-addressed at once,
  // and the post carries the reference. Chips are removable until posted;
  // an orphan upload is served to nobody, so an abandoned pick costs nothing.
  var pending=[];
  var fileIn=document.getElementById('cfile');
  var attachBtn=document.getElementById('cattach');
  var chips=document.getElementById('cchips');
  function renderChips(){
    if(!chips) return;
    chips.innerHTML='';
    pending.forEach(function(p, i){
      var c=document.createElement('span'); c.className='chip';
      c.appendChild(document.createTextNode('▤ '+p.title+' '));
      var x=document.createElement('button'); x.type='button'; x.textContent='×';
      x.setAttribute('aria-label','remove '+p.title);
      x.addEventListener('click', function(){ pending.splice(i,1); renderChips(); });
      c.appendChild(x);
      chips.appendChild(c);
    });
  }
  if(attachBtn && fileIn){
    attachBtn.addEventListener('click', function(){ fileIn.click(); });
    fileIn.addEventListener('change', function(){
      [].forEach.call(fileIn.files, function(file){
        file.text().then(function(content){
          return fetch('/artifacts',{method:'POST',
            headers:{'Content-Type':'text/markdown'}, body:content})
            .then(function(r){ return r.json().then(function(j){
              if(!r.ok) throw new Error(j.detail||'artifact refused');
              pending.push({hash:j.hash, title:file.name});
              renderChips();
            });});
        }).catch(function(ex){ if(ta) fail(ta, String(ex && ex.message || ex)); });
      });
      fileIn.value='';
    });
  }
  function clearPending(){
    pending=[];
    renderChips();
    if(ta) ta.style.height='';
    updateTok();
  }

  // The token field earns its place: shown only while this browser holds no
  // key for the acting seat, or while a token is actually in hand. Once
  // enrolled ("logged in"), it disappears — and a key.* refusal brings it
  // back, because that is the moment a fresh token becomes the fix.
  var tokField=document.getElementById('enroltoken');
  function updateTok(){
    if(!tokField) return;
    if(tokField.value){ tokField.hidden=false; return; }
    var actor=(document.getElementById('actor')||{}).value;
    if(!actor || actor==='__new__'){ tokField.hidden=false; return; }
    idbGet(actor).then(function(pair){ tokField.hidden=!!pair; })
      .catch(function(){ tokField.hidden=false; });
  }
  window.commsTokenVis=updateTok;
  updateTok();
  var actorSel=document.getElementById('actor');
  if(actorSel) actorSel.addEventListener('change', updateTok);

  var DB='comms.keys', STORE='keys';
  function idb(){ return new Promise(function(res,rej){
    var r=indexedDB.open(DB,1);
    r.onupgradeneeded=function(){ r.result.createObjectStore(STORE); };
    r.onsuccess=function(){ res(r.result); }; r.onerror=function(){ rej(r.error); };
  });}
  function idbGet(k){ return idb().then(function(db){ return new Promise(function(res,rej){
    var t=db.transaction(STORE,'readonly').objectStore(STORE).get(k);
    t.onsuccess=function(){ res(t.result); }; t.onerror=function(){ rej(t.error); };
  });});}
  function idbPut(k,v){ return idb().then(function(db){ return new Promise(function(res,rej){
    var t=db.transaction(STORE,'readwrite').objectStore(STORE).put(v,k);
    t.onsuccess=function(){ res(); }; t.onerror=function(){ rej(t.error); };
  });});}

  function hex(buf){ return Array.prototype.map.call(new Uint8Array(buf),
    function(b){ return ('0'+b.toString(16)).slice(-2); }).join(''); }

  // Returns the actor's keypair, enrolling a new one the first time. The
  // private key is created non-extractable, so this handle is the only way to
  // use it and there is no way to read it out.
  function keyFor(actor){
    var tf=document.getElementById('enroltoken');
    var token=tf && tf.value.trim();
    // A token in the field means "enrol this browser now" — a #setup= link the
    // operator just minted. That must win over any stale key a previous session
    // left in IndexedDB (a seat re-created on a fresh hub has a new server-side
    // key; signing with the old one is key.unknown). So when a token is present
    // we always generate and enrol fresh; only with no token do we reuse the
    // cached key for a seat that has already posted.
    return (token ? Promise.resolve(null) : idbGet(actor)).then(function(pair){
      if(pair) return pair;
      if(!token) return Promise.reject(new Error(
        'first post as '+actor+' needs an enrolment token — run: comms invite '+actor+
        ' and paste it in the token field'));
      return crypto.subtle.generateKey({name:'Ed25519'}, false, ['sign','verify'])
        .then(function(kp){
          return crypto.subtle.exportKey('raw', kp.publicKey).then(function(raw){
            return fetch('/keys',{method:'POST',
              headers:{'Content-Type':'application/json'},
              body:JSON.stringify({actor:actor, public_key:hex(raw), token:token.trim()})
            }).then(function(r){
              if(!r.ok) return r.json().then(function(j){
                // Two tabs, one token: the other tab spent it, and its key is
                // already in the shared IndexedDB. Fall back to that key
                // rather than wedging on the spent token forever.
                if(/already used/i.test(j.detail||'')){
                  return idbGet(actor).then(function(pair){
                    if(pair){ if(tf) tf.value=''; return pair; }
                    throw new Error(j.detail||'enrolment refused');
                  });
                }
                throw new Error(j.detail||'enrolment refused'); });
              // The token is single-use and now spent: clear it so the next
              // post signs with the enrolled key instead of resending a used
              // token, which the server rejects "already used".
              if(tf) tf.value='';
              return idbPut(actor, kp).then(function(){ return kp; });
            });
          });
        });
    });
  }
  // The claim card enrols through this same path, so there is exactly one
  // enrol rule in the page.
  window.commsKeyFor=keyFor;

  // Each entry turns the rest of the line into a typed command body. An
  // unknown verb is refused locally with the list, rather than posting chat
  // that happens to start with a slash.
  var SLASH={
    finding: function(rest){
      var m=rest.match(/^(p[0-3])\s+([\s\S]+)$/i);
      if(!m) return {error:'usage: /finding p0|p1|p2|p3 <what you found>'};
      return {kind:'finding', body:{severity:m[1].toLowerCase(), text:m[2]}};
    },
    til: function(rest){
      if(!rest) return {error:'usage: /til <what you learned>'};
      return {kind:'til', body:{text:rest}};
    },
    status: function(rest){
      var m=rest.match(/^(\d+)\/(\d+)\s+([\s\S]+)$/);
      if(m) return {kind:'status', body:{step:+m[1], of:+m[2], text:m[3]}};
      if(!rest) return {error:'usage: /status [3/7] <what you are doing>'};
      return {kind:'status', body:{text:rest}};
    },
    ask: function(rest){
      var m=rest.match(/^@(\S+)\s+([\s\S]+)$/);
      if(!m) return {error:'usage: /ask @someone <question>'};
      // A bare @name is sent as typed; the server resolves it against the
      // roster, the same way the client's --to does, so one rule serves both.
      return {kind:'question', body:{text:m[2]}, recipient:m[1]};
    },
    answer: function(rest){
      // No recipient: the core derives it from the question's author, so the
      // browser and the CLI share one rule instead of each inferring their own.
      var m=rest.match(/^#?(\d+)\s+([\s\S]+)$/);
      if(!m) return {error:'usage: /answer <seq> <your answer>'};
      return {kind:'answer', body:{text:m[2]}, refs:[m[1]]};
    },
    handoff: function(rest){
      var m=rest.match(/^@(\S+)\s+([\s\S]+)$/);
      if(!m) return {error:'usage: /handoff @someone <what they are taking over>'};
      return {kind:'handoff', body:{text:m[2]}, recipient:m[1]};
    },
    pr: function(rest){
      if(!/^https?:\/\//.test(rest)) return {error:'usage: /pr <url>'};
      return {kind:'pr.link', body:{url:rest}};
    }
  };

  // A red border and a title attribute is a failure nobody sees. Somebody typed
  // "hi", pressed enter, clicked post, and reported that nothing happened —
  // because the page cannot sign over plain HTTP to anything but localhost, and
  // said so only on hover.
  function fail(text, msg){
    text.setAttribute('title', msg);
    text.style.borderColor='var(--sev-hi)';
    var bar = document.getElementById('composer-error');
    if(bar){ bar.textContent = msg; bar.hidden = false; }
  }
  function clearFail(text){
    text.style.borderColor='';
    text.removeAttribute('title');
    var bar = document.getElementById('composer-error');
    if(bar){ bar.hidden = true; }
  }

  f.addEventListener('submit', function(e){
    e.preventDefault();
    var text=document.getElementById('ctext'), kind=document.getElementById('ckind');
    if(!text.value.trim()) return;
    var actor=(document.getElementById('actor')||{value:'bcm'}).value;

    // The seat must be a real name before a post can carry it. "__new__" is the
    // dropdown's placeholder, not a seat — posting as it is refused by the server
    // with "no namespace", and on the first-seat setup path that refusal is the
    // first thing a newcomer sees. Catch it here, keep what they typed, and ask
    // for the name instead of letting the post fail. This covers every way the
    // placeholder survives: an inline name box that blurred empty on load, or a
    // first-timer who typed in the composer without touching it.
    if(!actor || actor==='__new__'){
      var held=text.value;
      window.commsAskName(function(name){
        window.commsSetActor(name);
        text.value=held;
        window.commsNote('seat '+name+' ready — press post again; the first post enrols this browser');
        text.focus();
      });
      return;
    }

    // Slash-commands are the human's fast path to the same typed kinds agents
    // post. The dropdown stays as the discoverable route; this is the quick one.
    var raw=text.value.trim(), k=kind.value, body={};
    var m=raw.match(/^\/(\S+)\s*([\s\S]*)$/);
    if(m){
      var verb=m[1].toLowerCase(), rest=m[2].trim();
      var spec=SLASH[verb];
      if(!spec){
        fail(text, 'unknown command /'+verb+' — try: '+Object.keys(SLASH).join(', '));
        return;
      }
      var parsed=spec(rest);
      if(parsed.error){ fail(text, parsed.error); return; }
      k=parsed.kind; body=parsed.body;
      if(parsed.recipient) body.__recipient=parsed.recipient;
      if(parsed.refs) body.__refs=parsed.refs;
    } else {
      body.text=raw;
      if(k==='finding') body.severity='p2';
    }
    var recipient=body.__recipient||''; delete body.__recipient;
    var refs=body.__refs||null; delete body.__refs;
    var cmdObj={room:document.body.getAttribute('data-room'),
      author:actor, kind:k, body:body,
      idem:(crypto.randomUUID?crypto.randomUUID():String(Date.now()+Math.random()))};
    if(recipient) cmdObj.recipient=recipient;
    if(refs) cmdObj.refs=refs;
    if(pending.length) cmdObj.attachments=pending.map(function(p){
      return {hash:p.hash, title:p.title}; });
    var payload=JSON.stringify(cmdObj);

    // When the server accepts unsigned commands (-insecure, localhost demos),
    // post without enrolling. The client must not demand a key the server is
    // not going to check.
    if(document.body.getAttribute('data-signing') !== 'true'){
      fetch('/commands',{method:'POST',headers:{'Content-Type':'application/json'},body:payload})
        .then(function(r){ return r.json(); }).then(function(j){
          if(j.invariant){ if(/^(key|enrolment)\./.test(j.invariant) && tokField) tokField.hidden=false;
        fail(text, j.invariant+': '+j.detail); }
          else { text.value=''; clearFail(text); clearPending(); }
        }).catch(function(err){ fail(text, String(err && err.message || err)); });
      return;
    }

    if(!crypto.subtle){
      fail(text, 'This page cannot sign, so it cannot post: the browser only exposes '+
        'Web Crypto over HTTPS or on localhost, and this is plain HTTP to '+
        location.hostname + '. Reach the hub over HTTPS (tailscale serve, or a '+
        'reverse proxy), or use the comms CLI, which signs without a browser.');
      return;
    }

    keyFor(actor).then(function(kp){
      return crypto.subtle.sign({name:'Ed25519'}, kp.privateKey,
        new TextEncoder().encode(payload));
    }).then(function(sig){
      return fetch('/commands',{method:'POST',
        headers:{'Content-Type':'application/json','X-Signature':hex(sig)},
        body:payload});
    }).then(function(r){ return r.json(); }).then(function(j){
      if(j.invariant){ if(/^(key|enrolment)\./.test(j.invariant) && tokField) tokField.hidden=false;
        fail(text, j.invariant+': '+j.detail); }
      else { text.value=''; clearFail(text); clearPending(); }
    }).catch(function(err){ fail(text, String(err && err.message || err)); });
  });
})();
</script>`

const searchHTML = `<!doctype html>
<html lang="en" data-theme="dark">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>search · comms</title>
<style>` + baseCSS + `</style>
</head>
<body data-room="{{ROOM}}" data-head="{{HEAD}}" data-q="{{Q}}">
<header>
  <span class="brand">comms</span>
  <nav><a href="/">rooms</a><a class="sel" href="/search">search</a></nav>
  <span class="spacer"></span>
  <form action="/search" method="get">
    <input id="q" name="q" value="{{Q}}" placeholder="search  /" autocomplete="off">
    <button type="submit">find</button>
  </form>
</header>

<main class="ledger">
  <div class="head srow">
    <div>folio</div><div>lex</div><div>vec</div><div>author</div><div>kind</div><div>entry</div>
  </div>
  <div id="ledger-body">{{ROWS}}</div>
</main>

<footer>
  <div class="balance">
    <span>hits <b>{{N}}</b></span>
    {{LANES}}
    <span>new matches arrive <b>live</b></span>
  </div>
</footer>
` + themeScript + liveScript + `
</body>
</html>`

// artifactPage frames rendered markdown. It is a standalone document served
// under a strict CSP, not an inclusion into the room, so a rendering surprise
// cannot reach the room's DOM.
func artifactPage(hash string, body []byte) string {
	return `<!doctype html>
<html lang="en" data-theme="dark">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>artifact ` + hash[:12] + ` · comms</title>
<style>` + baseCSS + `
body { display:block; padding:0; }
.art { max-width: 52rem; margin: 0 auto; padding: 1.5rem 1.25rem 4rem; }
.art h1,.art h2,.art h3 { color: var(--ink-strong); margin: 1.6rem 0 .5rem; line-height:1.25; }
.art h1 { font-size:1.35rem; } .art h2 { font-size:1.1rem; } .art h3 { font-size:.95rem; }
.art p, .art li { line-height:1.6; }
.art table { border-collapse:collapse; width:100%; margin:.8rem 0; }
.art th, .art td { border:1px solid var(--rule); padding:.3rem .55rem; text-align:left; }
.art th { background: var(--band); color: var(--ink-mute); font-weight:500; }
.art pre { background: var(--panel); border:1px solid var(--rule); padding:.6rem .75rem; overflow-x:auto; }
.art code { background: var(--panel); padding:.05rem .3rem; }
.art pre code { background:none; padding:0; }
.art blockquote { border-left:2px solid var(--rule-strong); margin:.8rem 0; padding-left:.9rem; color: var(--ink-mute); }
.art hr { border:0; border-top:1px solid var(--rule); margin:1.5rem 0; }
.artfoot { border-top:1px solid var(--rule); margin-top:2rem; padding-top:.6rem;
           color: var(--ink-faint); font-size:.72rem; }
</style>
</head>
<body>
<div class="art">` + string(body) + `
<div class="artfoot">artifact ` + hash + ` · stored as GitHub-Flavored Markdown, rendered sanitized</div>
</div>
</body>
</html>`
}
