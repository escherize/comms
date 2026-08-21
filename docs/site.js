// comms docs — theme cycling (same three themes as the room), copy buttons,
// and the hero ledger's one authored moment. No dependencies, ~1KB.
(function () {
  var order = ["dark", "light", "slate"];
  var root = document.documentElement;
  try {
    var saved = localStorage.getItem("comms-docs-theme");
    if (saved) root.setAttribute("data-theme", saved);
  } catch (e) {}
  var btn = document.querySelector("button.theme");
  if (btn) {
    var label = function () {
      btn.textContent = "theme: " + (root.getAttribute("data-theme") || "auto");
    };
    label();
    btn.addEventListener("click", function () {
      var cur = root.getAttribute("data-theme");
      var next = order[(order.indexOf(cur) + 1) % order.length];
      root.setAttribute("data-theme", next);
      try { localStorage.setItem("comms-docs-theme", next); } catch (e) {}
      label();
    });
  }

  document.querySelectorAll(".term").forEach(function (t) {
    var pre = t.querySelector("pre");
    if (!pre) return;
    var b = document.createElement("button");
    b.className = "copy";
    b.type = "button";
    b.textContent = "copy";
    b.addEventListener("click", function () {
      // Strip prompts and drop output lines: what lands on the clipboard runs.
      var lines = [];
      pre.querySelectorAll("code > span.line, code").forEach(function () {});
      pre.textContent.split("\n").forEach(function (l) {
        if (/^\s*[#$]\s/.test(l)) { lines.push(l.replace(/^\s*[#$]\s/, "")); }
        else if (!pre.querySelector(".p")) { lines.push(l); }
      });
      var text = (lines.length ? lines : pre.textContent.split("\n")).join("\n").trim();
      navigator.clipboard.writeText(text).then(function () {
        b.textContent = "copied";
        b.classList.add("done");
        setTimeout(function () { b.textContent = "copy"; b.classList.remove("done"); }, 1400);
      });
    });
    t.appendChild(b);
  });

  // The hero ledger posts its own rows once it is on screen. Arm first (rows
  // hide), then play; without JS the figure stays fully visible.
  var live = document.querySelector(".ledger.live");
  if (live) {
    var rows = live.querySelectorAll(".lrow");
    var delays = [0, 0.15, 0.9, 1.7, 2.6, 3.6];
    rows.forEach(function (r, i) {
      r.style.setProperty("--d", (delays[i] != null ? delays[i] : 4) + "s");
    });
    live.classList.add("armed");
    var play = function () { live.classList.add("play"); };
    if ("IntersectionObserver" in window) {
      var io = new IntersectionObserver(function (es) {
        es.forEach(function (e) {
          if (e.isIntersecting) { play(); io.disconnect(); }
        });
      }, { threshold: 0.3 });
      io.observe(live);
    } else {
      play();
    }
  }
})();
