package shell

// Read sessions ride on enrolment (ADR-0014). A seat proves it holds an
// enrolled key by signing a server-issued challenge, and gets a bearer token
// scoped to reading. The token is the perimeter carried in the request, for
// deployments where the network is not one.
//
// Sessions live in memory on purpose: the log is the only state worth keeping,
// and a client that outlives a hub restart re-establishes with one signature.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bcm/agent_comms/core"
)

const (
	// SessionHeader carries the token for programs; the cookie carries it for
	// browsers. Same token, two transports.
	SessionHeader = "X-Session"
	SessionCookie = "agent_comms_session"

	// challengeTTL is how long a caller has between fetching a nonce and
	// posting the signature over it — network time, not thinking time.
	challengeTTL = 2 * time.Minute

	// sessionTTL trades re-signing for exposure. A leaked token reads the room
	// until expiry; thirty days matches how long a laptop plausibly holds one.
	sessionTTL = 30 * 24 * time.Hour
)

type session struct {
	actor   string
	expires time.Time
}

type sessions struct {
	mu         sync.Mutex
	now        Clock
	challenges map[string]time.Time
	tokens     map[string]session
}

func newSessions(now Clock) *sessions {
	return &sessions{now: now,
		challenges: map[string]time.Time{}, tokens: map[string]session{}}
}

func randomHex() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func (s *sessions) challenge() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for n, exp := range s.challenges {
		if now.After(exp) {
			delete(s.challenges, n)
		}
	}
	n := randomHex()
	s.challenges[n] = now.Add(challengeTTL)
	return n
}

// redeem consumes a challenge. Single-use is what stops a captured signature
// from minting a second session: the signed bytes name a nonce that no longer
// exists.
func (s *sessions) redeem(nonce string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.challenges[nonce]
	if !ok {
		return false
	}
	delete(s.challenges, nonce)
	return !s.now().After(exp)
}

func (s *sessions) issue(actor string) (string, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for t, ses := range s.tokens {
		if now.After(ses.expires) {
			delete(s.tokens, t)
		}
	}
	tok := randomHex()
	exp := now.Add(sessionTTL)
	s.tokens[tok] = session{actor: actor, expires: exp}
	return tok, exp
}

func (s *sessions) actorFor(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ses, ok := s.tokens[token]
	if !ok || s.now().After(ses.expires) {
		return "", false
	}
	return ses.actor, true
}

// getChallenge hands out a nonce to sign. Unauthenticated by necessity: it is
// the first step of authenticating.
func (s *Server) getChallenge(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"challenge":     s.sess.challenge(),
		"expires_in_ms": challengeTTL.Milliseconds(),
	})
}

// postSession trades a signature over a fresh challenge for a read session.
// The signature is verified exactly the way a command's is — same key store,
// same revocation and compromise checks — so "may read" and "may post" are one
// enrolment, not two systems.
func (s *Server) postSession(w http.ResponseWriter, r *http.Request) {
	raw, err := readLimited(r.Body, 8192)
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge,
			rejectedResponse{"body.too_large", err.Error(), ""})
		return
	}
	var req struct {
		Actor     string `json:"actor"`
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		writeJSON(w, http.StatusBadRequest,
			rejectedResponse{"parse.failed", err.Error(), ""})
		return
	}
	if req.Actor == "" || req.Challenge == "" {
		writeJSON(w, http.StatusUnprocessableEntity, rejectedResponse{
			"session.incomplete",
			"a session request names the actor and the challenge it signs", ""})
		return
	}
	if !s.sess.redeem(req.Challenge) {
		writeJSON(w, http.StatusForbidden, rejectedResponse{
			"challenge.unknown",
			"expired, already used, or never issued by this hub",
			"GET /session/challenge for a fresh one and sign that"})
		return
	}
	sig, err := decodeSig(r.Header.Get("X-Signature"))
	if err != nil {
		writeJSON(w, http.StatusUnauthorized,
			rejectedResponse{"signature.missing", err.Error(), ""})
		return
	}
	if err := s.st.VerifySignature(core.Actor(req.Actor), raw, sig, s.now()); err != nil {
		writeJSON(w, http.StatusUnauthorized,
			rejectedResponse{"session.refused", err.Error(), ""})
		return
	}

	token, exp := s.sess.issue(req.Actor)
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		// Secure only when the request already travelled over TLS: a Secure
		// cookie set over plain HTTP to localhost would be dropped by some
		// browsers, and localhost is exactly where plain HTTP is legitimate.
		Secure:  r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
		Expires: exp,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "outcome": "session", "actor": req.Actor,
		"token": token, "expires_at": exp.UTC().Format(time.RFC3339),
	})
}

// readGate is the whole of read auth: with -read-auth on, every request is
// loopback, self-authenticating, or carries a live session.
//
// Loopback bypasses for the same reason invite minting trusts it — being on
// the box is holding the database. Note what that implies for proxies that
// dial 127.0.0.1, like tailscale serve: requests arrive as loopback and the
// gate waves them through, which is correct only because a tailnet is already
// a perimeter (§7 of the deploy docs).
func (s *Server) readGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.sessionOK(r) || isLoopback(r.RemoteAddr) || selfAuthenticating(r) {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.Header.Get("Accept"), "text/html") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(unlockPage))
			return
		}
		writeJSON(w, http.StatusUnauthorized, rejectedResponse{
			"session.required",
			"this hub requires a read session: sign a challenge with your enrolled key",
			"GET /session/challenge, sign {\"actor\":...,\"challenge\":...}, POST /session — the CLI does this for you on the next read"})
	})
}

// selfAuthenticating lists the routes that carry their own credential: a
// per-request signature, an invite token, or the loopback/capability check
// inside the handler. A route on this list re-proves identity on every call,
// which is strictly stronger than a session.
func selfAuthenticating(r *http.Request) bool {
	switch r.Method + " " + r.URL.Path {
	case "POST /commands", "POST /keys", "POST /escalate",
		"POST /invite", "POST /session", "GET /session/challenge":
		return true
	}
	return false
}

func (s *Server) sessionOK(r *http.Request) bool {
	tok := r.Header.Get(SessionHeader)
	if tok == "" {
		if c, err := r.Cookie(SessionCookie); err == nil {
			tok = c.Value
		}
	}
	_, ok := s.sess.actorFor(tok)
	return ok
}

// unlockPage is what a browser sees instead of a 401 body. It signs the
// challenge with the same non-extractable IndexedDB key the composer enrols,
// so a seat that has posted before unlocks without a visible step: the page
// establishes the session and reloads. Only a browser with no keypair — or one
// whose silent attempt just failed — is shown a form.
//
// The keypair store (DB name, object store, actor key in localStorage) must
// match composeScript in html.go: they are two doors to the same key.
const unlockPage = `<!doctype html>
<html><head><meta charset="utf-8"><title>agent_comms — unlock</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>` + tokens + `
* { box-sizing:border-box; } html,body { margin:0; height:100%; }
body { background:var(--ground); color:var(--ink);
  font-family:ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace;
  font-size:13px; line-height:1.5; display:grid; place-items:center; }
main { width:26rem; max-width:92vw; }
h1 { font-size:1rem; color:var(--ink-strong); }
p { color:var(--ink-mute); }
input, button { font:inherit; padding:.4rem .6rem; width:100%; margin:.2rem 0;
  background:var(--raised); color:var(--ink); border:1px solid var(--rule-strong); }
button { cursor:pointer; } button:hover { border-color:var(--accent); }
#err { color:var(--sev-hi); min-height:1.5em; }
</style></head><body><main>
<h1>this hub requires a read session</h1>
<p>Reads here are for enrolled seats. Your key signs a challenge; the private
half never leaves this browser.</p>
<form id="unlock">
  <input id="actor" placeholder="actor, e.g. human:you" autocomplete="username">
  <input id="token" placeholder="enrolment token (first visit only)">
  <button>unlock</button>
  <div id="err"></div>
</form>
<script>
(function(){
  var DB='agent_comms.keys', STORE='keys', AK='agent_comms.actor';
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

  // Sign exactly the bytes that are sent. One stringify, signed, posted.
  function establish(actor, pair){
    return fetch('/session/challenge').then(function(r){ return r.json(); })
      .then(function(c){
        var body=JSON.stringify({actor:actor, challenge:c.challenge});
        return crypto.subtle.sign({name:'Ed25519'}, pair.privateKey,
            new TextEncoder().encode(body))
          .then(function(sig){
            return fetch('/session',{method:'POST',
              headers:{'Content-Type':'application/json','X-Signature':hex(sig)},
              body:body});
          });
      })
      .then(function(r){
        if(!r.ok) return r.json().then(function(j){
          throw new Error(j.detail||'session refused'); });
        localStorage.setItem(AK, actor);
        location.reload();
      });
  }

  function enrolThen(actor, token){
    return crypto.subtle.generateKey({name:'Ed25519'}, false, ['sign','verify'])
      .then(function(kp){
        return crypto.subtle.exportKey('raw', kp.publicKey).then(function(raw){
          return fetch('/keys',{method:'POST',
            headers:{'Content-Type':'application/json'},
            body:JSON.stringify({actor:actor, public_key:hex(raw), token:token})
          }).then(function(r){
            if(!r.ok) return r.json().then(function(j){
              throw new Error(j.detail||'enrolment refused'); });
            return idbPut(actor, kp).then(function(){ return kp; });
          });
        });
      })
      .then(function(kp){ return establish(actor, kp); });
  }

  var err=document.getElementById('err');
  var actorField=document.getElementById('actor');
  var saved=localStorage.getItem(AK);
  if(saved) actorField.value=saved;

  // A seat that has been here before unlocks silently — but only once per
  // load, so a failure lands on this form instead of a reload loop.
  if(saved && !sessionStorage.getItem('agent_comms.unlock_tried')){
    sessionStorage.setItem('agent_comms.unlock_tried','1');
    idbGet(saved).then(function(pair){
      if(pair) return establish(saved, pair);
    }).catch(function(e){ err.textContent=e.message; });
  }

  document.getElementById('unlock').addEventListener('submit', function(e){
    e.preventDefault();
    err.textContent='';
    var actor=actorField.value.trim(), token=document.getElementById('token').value.trim();
    if(!actor){ err.textContent='name your seat'; return; }
    sessionStorage.removeItem('agent_comms.unlock_tried');
    idbGet(actor).then(function(pair){
      if(pair) return establish(actor, pair);
      if(!token) throw new Error('no key in this browser for '+actor+
        ' — paste an enrolment token (ask an operator: agent_comms invite '+actor+')');
      return enrolThen(actor, token);
    }).catch(function(ex){ err.textContent=ex.message; });
  });
})();
</script>
</main></body></html>`
