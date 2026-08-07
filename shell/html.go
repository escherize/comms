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
.composer input[name=text], .composer #ctext { flex:1; }
.composer .tok { width:16rem; }
body[data-signing="false"] .composer .tok { display:none; }

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
  var ak='agent_comms.actor', a=document.getElementById('actor');
  if(a){
    var saved=localStorage.getItem(ak);
    if(saved){ a.value=saved; }
    a.addEventListener('change', function(){ localStorage.setItem(ak, a.value); });
  }
  var k='agent_comms.theme', s=localStorage.getItem(k);
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
    if(e.target.tagName==='INPUT'||e.target.tagName==='SELECT') return;
    if(e.key==='/'){ e.preventDefault(); var q=document.getElementById('q'); if(q) q.focus(); }
    if(e.key==='t'){ cycleTheme(); }
    if(e.key==='c'){ var c=document.getElementById('ctext'); if(c){ e.preventDefault(); c.focus(); } }
    // Room switching: [ and ] move through the nav without a mouse.
    if(e.key==='[' || e.key===']'){
      var links=[].slice.call(document.querySelectorAll('header nav a'));
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
<title>{{ROOM}} · agent_comms</title>
<style>` + baseCSS + `</style>
</head>
<body data-room="{{ROOM}}" data-head="{{HEAD}}" data-signing="{{SIGNING}}">
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
  <span class="who" id="whoami" title="who you are posting as — identity, not authentication">
    <label for="actor">as</label>
    <select id="actor">
      <option value="bcm">bcm</option>
      <option value="sarah">sarah</option>
      <option value="agent:claude-1">claude-1</option>
      <option value="agent:codex-3">codex-3</option>
    </select>
  </span>
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
    {{PROGRESS}}
  </div>
  <form class="composer" id="composer">
    <select name="kind" id="ckind">
      <option value="chat">chat</option>
      <option value="finding">finding</option>
      <option value="til">til</option>
      <option value="status">status</option>
    </select>
    <input id="ctext" name="text" placeholder="entry, or /finding /til /status /ask /answer /handoff /pr  (c to focus)" autocomplete="off">
    <input id="enroltoken" class="tok" placeholder="enrolment token (first post only)" autocomplete="off">
    <button type="submit">post</button>
  </form>
</footer>
` + liveScript + composeScript + themeScript + `
</body>
</html>`

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

  var DB='agent_comms.keys', STORE='keys';
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
    return idbGet(actor).then(function(pair){
      if(pair) return pair;
      var tf=document.getElementById('enroltoken');
      var token=tf && tf.value.trim();
      if(!token) return Promise.reject(new Error(
        'first post as '+actor+' needs an enrolment token — run: agent_comms -invite '+actor+
        ' and paste it in the token field'));
      return crypto.subtle.generateKey({name:'Ed25519'}, false, ['sign','verify'])
        .then(function(kp){
          return crypto.subtle.exportKey('raw', kp.publicKey).then(function(raw){
            return fetch('/keys',{method:'POST',
              headers:{'Content-Type':'application/json'},
              body:JSON.stringify({actor:actor, public_key:hex(raw), token:token.trim()})
            }).then(function(r){
              if(!r.ok) return r.json().then(function(j){
                throw new Error(j.detail||'enrolment refused'); });
              return idbPut(actor, kp).then(function(){ return kp; });
            });
          });
        });
    });
  }

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

  function fail(text, msg){
    text.setAttribute('title', msg);
    text.style.borderColor='var(--sev-hi)';
  }

  f.addEventListener('submit', function(e){
    e.preventDefault();
    var text=document.getElementById('ctext'), kind=document.getElementById('ckind');
    if(!text.value.trim()) return;
    var actor=(document.getElementById('actor')||{value:'bcm'}).value;

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
    var payload=JSON.stringify(cmdObj);

    // When the server accepts unsigned commands (-insecure, localhost demos),
    // post without enrolling. The client must not demand a key the server is
    // not going to check.
    if(document.body.getAttribute('data-signing') !== 'true'){
      fetch('/commands',{method:'POST',headers:{'Content-Type':'application/json'},body:payload})
        .then(function(r){ return r.json(); }).then(function(j){
          if(j.invariant){ fail(text, j.invariant+': '+j.detail); }
          else { text.value=''; text.style.borderColor=''; text.removeAttribute('title'); }
        }).catch(function(err){ fail(text, String(err && err.message || err)); });
      return;
    }

    if(!crypto.subtle){ fail(text,'this browser cannot sign; serve over https or localhost'); return; }

    keyFor(actor).then(function(kp){
      return crypto.subtle.sign({name:'Ed25519'}, kp.privateKey,
        new TextEncoder().encode(payload));
    }).then(function(sig){
      return fetch('/commands',{method:'POST',
        headers:{'Content-Type':'application/json','X-Signature':hex(sig)},
        body:payload});
    }).then(function(r){ return r.json(); }).then(function(j){
      if(j.invariant){ fail(text, j.invariant+': '+j.detail); }
      else { text.value=''; text.style.borderColor=''; text.removeAttribute('title'); }
    }).catch(function(err){ fail(text, String(err && err.message || err)); });
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
    <span>lanes searched <b>lexical</b></span>
    <span>vector <b>unbuilt — these results are lexical only</b></span>
  </div>
</footer>
` + themeScript + `
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
<title>artifact ` + hash[:12] + ` · agent_comms</title>
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
