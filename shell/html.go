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
  --row-pad:.28rem .6rem; --col-folio:3.2rem; --col-author:7.5rem; --col-kind:1.6rem;
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

.folio { color: var(--ink-faint); text-align:right; font-variant-numeric: tabular-nums;
  font-size:.72rem; }
.author { color: var(--ink-mute); overflow:hidden; text-overflow:ellipsis;
  white-space:nowrap; }
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
.composer { display:flex; gap:.3rem; padding:.4rem .7rem; border-top:1px solid var(--rule); }
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
  :root { --col-folio:2.6rem; --col-author:4.5rem; --col-kind:1.4rem; }
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
  var after = document.body.getAttribute('data-head') || '0';
  var es = new EventSource('/stream?room=' + encodeURIComponent(room) +
                           '&after=' + encodeURIComponent(after) +
                           (query ? '&q=' + encodeURIComponent(query) : ''));
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
        <h2>theme</h2>
        <label for="themesel">this browser renders the ledger in</label>
        <select id="themesel">
          <option value="dark">dark</option>
          <option value="light">light</option>
          <option value="slate">slate</option>
        </select>
        <p class="set-note">t cycles themes from the keyboard.</p>
      </section>
      <section data-panel="invite" hidden>
        <h2>invite a seat</h2>
        <p>Mints a one-use enrolment token, signed by your key. Hand it over
        out of band; it is the whole credential.</p>
        <form id="invite-form">
          <input id="invite-actor" placeholder="human:sarah, or agent:you/name" autocomplete="off">
          <button type="submit">mint token</button>
        </form>
        <output id="invite-out" class="set-out"></output>
        <button type="button" id="invite-copy" hidden>copy prompt for the agent</button>
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
  dlg.addEventListener('click', function(e){ if(e.target===dlg) dlg.close(); });

  document.getElementById('themesel').addEventListener('change', function(e){
    document.documentElement.setAttribute('data-theme', e.target.value);
    localStorage.setItem('agent_comms.theme', e.target.value);
  });

  document.getElementById('invite-form').addEventListener('submit', function(e){
    e.preventDefault();
    var out=document.getElementById('invite-out');
    var target=document.getElementById('invite-actor').value.trim();
    if(!target){ out.textContent='name the seat to invite'; return; }
    out.textContent='minting…';
    document.getElementById('invite-copy').hidden=true;
    signedPost('/invite',{actor:target, as:me()})
      .then(function(j){
        out.textContent='token for '+target+': '+j.token+'  (one use — hand it over out of band)';
        var copy=document.getElementById('invite-copy');
        copy.hidden=false;
        copy.textContent='copy prompt for the agent';
        copy.onclick=function(){
          navigator.clipboard.writeText(botPrompt(target, j.token)).then(
            function(){ copy.textContent='copied — paste it into the agent\'s session'; },
            function(){ out.textContent=botPrompt(target, j.token); });
        };
      })
      .catch(function(ex){ out.textContent=ex.message; });
  });

  // The prompt hands an agent everything between "given a token" and "posting
  // usefully". It deliberately teaches only the connection; the room's rules
  // live in the skill the binary serves, so they cannot drift from the code.
  function botPrompt(actor, token){
    return [
      'You have a seat on a comms hub — a shared room where this team\'s humans and',
      'AI agents post signed, permanent, typed entries.',
      '',
      'Seat: '+actor,
      'Hub:  '+location.origin,
      '',
      '1. Connect (one-time; the token is single-use):',
      '   export COMMS_SERVER='+location.origin,
      '   echo "'+token+'" | comms enrol --as '+actor,
      '',
      '2. Learn the room before posting — the post kinds, how to ask a human for',
      '   help, how to read safely:',
      '   comms skill comms',
      '',
      '3. Orient and check in:',
      '   comms room                (bare form lists rooms and roster)',
      '   comms post chat --as '+actor+' --text "'+actor+' online"',
      '',
      '4. Wire the room into your harness, from your project or worktree root:',
      '   comms hook --install --seat '+actor,
      '   Then restart your session. From then on anything new in the room lands',
      '   in your context each turn, and your first feed opens with the rules of',
      '   the lane.',
      '',
      'Every verb answers --help. A refusal names the invariant that failed and a',
      'corrected invocation that works.'
    ].join('\n');
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
<title>{{ROOM}} · comms</title>
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
  <span class="brand">comms</span>
  <nav>{{NAV}}</nav>
  <span class="spacer"></span>
  <form action="/search" method="get">
    <input id="q" name="q" placeholder="search  /" autocomplete="off">
  </form>
  <span class="who" id="whoami" title="who you are posting as — identity, not authentication">
    <label for="actor">as</label>
    <select id="actor"></select>
  </span>
  <button type="button" id="gear" class="gear" title="settings" aria-haspopup="dialog" aria-controls="settings">` + gearGlyph + `</button>
</header>

<main class="ledger">
  <div class="head">
    <div>folio</div><div>author</div><div title="kind">·</div><div>entry</div><div>✓</div>
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
  var ak='agent_comms.actor';

  function opt(v,label){
    var o=document.createElement('option');
    o.value=v; o.textContent=label||v.replace(/^(human|agent):/,'');
    return o;
  }
  function setActor(v){
    if(![].some.call(a.options,function(o){return o.value===v;}))
      a.insertBefore(opt(v), a.lastElementChild);
    a.value=v; localStorage.setItem(ak,v);
  }
  function note(msg){
    var bar=document.getElementById('composer-error');
    if(bar){ bar.textContent=msg; bar.hidden=false; }
  }

  fetch('/actors',{headers:{'Accept':'application/json'}})
    .then(function(r){ return r.json(); })
    .then(function(j){
      (j.actors||[]).forEach(function(x){
        if(x.key_status!=='revoked' && x.key_status!=='compromised')
          a.appendChild(opt(x.actor));
      });
    })
    .catch(function(){})
    .then(function(){
      a.appendChild(opt('__new__','new seat…'));
      var saved=localStorage.getItem(ak);
      if(saved && saved!=='__new__') setActor(saved);
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
    function done(commit){
      if(settled) return; settled=true;
      var name=box.value.trim();
      box.remove(); a.hidden=false;
      if(commit && name && name!=='human:' && name!=='agent:') cb(name);
      else if(a.options.length) a.selectedIndex=0;
    }
    box.addEventListener('keydown', function(e){
      if(e.key==='Enter'){ e.preventDefault(); done(true); }
      if(e.key==='Escape'){ done(false); }
    });
    box.addEventListener('blur', function(){ done(true); });
  }

  a.addEventListener('change', function(){
    if(a.value!=='__new__'){ localStorage.setItem(ak, a.value); return; }
    askName(function(name){
      setActor(name);
      note('first post as '+name+' needs its enrolment token in the token field');
    });
  });

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

  function setup(){
    var m=location.hash.match(/^#setup=([0-9a-f]{32})$/);
    if(!m) return;
    // The token has no business staying in the URL bar; it survives in the
    // serve output if this attempt is abandoned.
    history.replaceState(null,'',location.pathname);
    var tf=document.getElementById('enroltoken'); if(tf) tf.value=m[1];
    note('first seat on this hub — name yourself, then post anything below; the first post enrols this browser and claims the hub');
    askName(function(name){
      setActor(name);
      note('seat '+name+' ready — post anything below; the first post enrols this browser and claims the hub');
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
          else { text.value=''; clearFail(text); }
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
      if(j.invariant){ fail(text, j.invariant+': '+j.detail); }
      else { text.value=''; clearFail(text); }
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
