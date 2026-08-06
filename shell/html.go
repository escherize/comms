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
  --row-pad:.28rem .6rem; --col-folio:5.5rem; --col-author:8rem; --col-kind:3.4rem;
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

.folio { color: var(--ink-faint); text-align:right; font-variant-numeric: tabular-nums; }
.author { color: var(--ink-mute); overflow:hidden; text-overflow:ellipsis; }
.kind { color: var(--ink-faint); font-size:.72rem; letter-spacing:.05em; }
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

.sev { font-size:.68rem; letter-spacing:.06em; }
.sev-p0, .sev-p1 { color: var(--sev-hi); }
.sev-p2 { color: var(--sev-lo); }
.sev-p3 { color: var(--ink-faint); }

.row.struck .body { color: var(--ink-faint); text-decoration: line-through; }
.row.struck .erased { text-decoration:none; font-style:italic; }

.carried {
  grid-template-columns: var(--col-folio) 1fr;
  border-bottom:1px solid var(--rule);
  background: var(--band); color: var(--ink-faint); font-style:italic; cursor:pointer;
}
.carried > div { padding: var(--row-pad); }
.carried:hover { color: var(--ink-mute); }
.carried .cf::after { content:" ▸"; }

/* ---- foot ---- */
footer {
  border-top:1px solid var(--rule-strong); background: var(--panel);
}
.balance {
  display:flex; gap:1.4rem; flex-wrap:wrap;
  padding:.3rem .7rem; color: var(--ink-faint); font-size:.72rem;
}
.balance b { color: var(--ink); font-weight:500; }
.composer { display:flex; gap:.3rem; padding:.4rem .7rem; border-top:1px solid var(--rule); }
.composer input[name=text] { flex:1; }

/* ---- search ---- */
.srow { grid-template-columns: var(--col-folio) 3rem 3rem var(--col-author) var(--col-kind) 1fr; }
.rank { color: var(--ink-faint); text-align:right; font-variant-numeric: tabular-nums; }
.rank.vec { color: var(--ink-faint); opacity:.5; }
.empty { padding:2rem .7rem; color: var(--ink-faint); }

@media (prefers-reduced-motion: reduce) { * { transition:none !important; animation:none !important; } }
@media (max-width: 640px) {
  :root { --col-folio:3.6rem; --col-author:5rem; --col-kind:2.6rem; }
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
  // Resume after the last folio the server already rendered. Without this the
  // stream replays the whole backlog and appends duplicates of every row that
  // was collapsed into a carried-forward line (those have no DOM node to
  // dedupe against).
  var after = document.body.getAttribute('data-head') || '0';
  var es = new EventSource('/stream?room=' + encodeURIComponent(room) +
                           '&after=' + encodeURIComponent(after));
  es.addEventListener('datastar-patch-elements', function(e){
    var mode='append', selector='#ledger-body', html=[];
    e.data.split('\n').forEach(function(line){
      if (line.indexOf('mode ')===0) mode = line.slice(5).trim();
      else if (line.indexOf('selector ')===0) selector = line.slice(9).trim();
      else if (line.indexOf('elements ')===0) html.push(line.slice(9));
    });
    var target = document.querySelector(selector);
    if (!target) return;
    var seq = e.lastEventId;
    if (seq && target.querySelector('[data-seq="'+seq+'"]')) return; // resume overlap
    if (mode==='append') target.insertAdjacentHTML('beforeend', html.join('\n'));
    else target.innerHTML = html.join('\n');
    var main = document.querySelector('main.ledger');
    if (main) main.scrollTop = main.scrollHeight;
  });
})();
</script>`

const themeScript = `
<script>
(function(){
  var k='agent_comms.theme', s=localStorage.getItem(k);
  if(s) document.documentElement.setAttribute('data-theme', s);
  window.cycleTheme=function(){
    var order=['dark','light','slate'],
        cur=document.documentElement.getAttribute('data-theme')||'dark',
        next=order[(order.indexOf(cur)+1)%order.length];
    document.documentElement.setAttribute('data-theme', next);
    localStorage.setItem(k, next);
  };
  document.addEventListener('keydown', function(e){
    if(e.target.tagName==='INPUT'||e.target.tagName==='SELECT') return;
    if(e.key==='/'){ e.preventDefault(); var q=document.getElementById('q'); if(q) q.focus(); }
    if(e.key==='t'){ cycleTheme(); }
    if(e.key==='c'){ var c=document.getElementById('ctext'); if(c){ e.preventDefault(); c.focus(); } }
  });
})();
</script>`

const roomHTML = `<!doctype html>
<html lang="en" data-theme="dark">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{ROOM}} · agent_comms</title>
<style>` + baseCSS + `</style>
</head>
<body data-room="{{ROOM}}" data-head="{{HEAD}}">
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
  <span class="brand">agent_comms</span>
  <nav>{{NAV}}</nav>
  <span class="spacer"></span>
  <form action="/search" method="get">
    <input id="q" name="q" placeholder="search  /" autocomplete="off">
  </form>
  <button type="button" onclick="cycleTheme()" title="cycle theme (t)">theme</button>
</header>

<main class="ledger">
  <div class="head">
    <div>folio</div><div>author</div><div>kind</div><div>entry</div><div>✓</div>
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
  </div>
  <form class="composer" id="composer">
    <select name="kind" id="ckind">
      <option value="chat">chat</option>
      <option value="finding">finding</option>
      <option value="til">til</option>
      <option value="status">status</option>
    </select>
    <input id="ctext" name="text" placeholder="entry  (c to focus)" autocomplete="off">
    <button type="submit">post</button>
  </form>
</footer>
` + liveScript + composeScript + themeScript + `
</body>
</html>`

// composeScript turns the composer into a command submission. It exists because
// the command surface takes JSON, and the browser is just another client of it.
const composeScript = `
<script>
(function(){
  var f=document.getElementById('composer');
  if(!f) return;
  f.addEventListener('submit', function(e){
    e.preventDefault();
    var text=document.getElementById('ctext'), kind=document.getElementById('ckind');
    if(!text.value.trim()) return;
    var body={text:text.value};
    if(kind.value==='finding') body.severity='p2';
    fetch('/commands',{method:'POST',headers:{'Content-Type':'application/json'},
      body:JSON.stringify({room:document.body.getAttribute('data-room'),
        author:'bcm', kind:kind.value, body:body,
        idem:(crypto.randomUUID?crypto.randomUUID():String(Date.now()+Math.random()))})
    }).then(function(r){return r.json()}).then(function(j){
      if(j.invariant){ text.setAttribute('title', j.invariant+': '+j.detail); text.style.borderColor='var(--sev-hi)'; }
      else { text.value=''; text.style.borderColor=''; text.removeAttribute('title'); }
    });
  });
})();
</script>`

const searchHTML = `<!doctype html>
<html lang="en" data-theme="dark">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>search · agent_comms</title>
<style>` + baseCSS + `</style>
</head>
<body>
<header>
  <span class="brand">agent_comms</span>
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
    <span>lexical <b>current</b></span>
    <span>vector <b>ships M2</b></span>
  </div>
</footer>
` + themeScript + `
</body>
</html>`
