package httpapi

import "html/template"

var dashboardTmpl = template.Must(template.New("dashboard").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Plumtree Dashboard</title>
  <script src="https://shoo.dev/shoo.js"></script>
  <style>
    :root {
      --ink: #1d2522;
      --muted: #66736e;
      --paper: #f6f3ec;
      --line: #d8d1c4;
      --panel: #fffdf7;
      --accent: #126b57;
      --hot: #c7523a;
      --code: #263b36;
    }
    * { box-sizing: border-box; }
    html, body { margin: 0; min-height: 100%; background: var(--paper); color: var(--ink); }
    body {
      font-family: Charter, "Iowan Old Style", "Palatino Linotype", Georgia, serif;
      letter-spacing: 0;
    }
    button, table, input { font: inherit; letter-spacing: 0; }
    .shell { min-height: 100vh; display: grid; grid-template-columns: 260px 1fr; }
    aside {
      border-right: 1px solid var(--line);
      background: #e9e1d2;
      padding: 28px 22px;
      display: flex;
      flex-direction: column;
      gap: 28px;
    }
    .brand { font-size: 28px; line-height: 1; font-weight: 700; }
    .status { display: grid; gap: 8px; color: var(--muted); font-size: 14px; }
    .status strong { color: var(--ink); font-size: 16px; overflow-wrap: anywhere; }
    nav { display: grid; gap: 6px; }
    nav a {
      color: var(--ink);
      text-decoration: none;
      padding: 8px 0;
      border-bottom: 1px solid rgba(29,37,34,.16);
    }
    main { padding: 34px clamp(20px, 5vw, 68px); }
    .topbar { display: flex; align-items: flex-start; justify-content: space-between; gap: 18px; margin-bottom: 34px; }
    h1 { margin: 0; font-size: clamp(28px, 4vw, 46px); line-height: 1.02; max-width: 720px; }
    .actions { display: flex; gap: 10px; align-items: center; min-height: 40px; }
    button, .login {
      border: 1px solid var(--ink);
      border-radius: 6px;
      background: var(--ink);
      color: var(--paper);
      padding: 9px 13px;
      min-height: 40px;
      cursor: pointer;
      text-decoration: none;
      white-space: nowrap;
    }
    button.secondary { background: transparent; color: var(--ink); }
    .summary {
      display: grid;
      grid-template-columns: repeat(3, minmax(120px, 1fr));
      border: 1px solid var(--line);
      background: var(--panel);
      margin-bottom: 28px;
    }
    .claim {
      border: 1px solid var(--line);
      background: var(--panel);
      margin-bottom: 28px;
      padding: 22px;
      max-width: 620px;
    }
    .claim h2 { margin: 0 0 14px; font-size: 22px; line-height: 1.1; }
    .claim form { display: grid; gap: 12px; }
    .claim-row { display: flex; gap: 10px; align-items: flex-start; }
    .claim input {
      width: 100%;
      min-height: 40px;
      border: 1px solid var(--ink);
      border-radius: 6px;
      background: #fffaf0;
      color: var(--ink);
      padding: 8px 10px;
    }
    .hint { margin: 0; color: var(--muted); font-size: 13px; }
    .metric { padding: 18px; border-right: 1px solid var(--line); min-height: 92px; }
    .metric:last-child { border-right: 0; }
    .metric span { color: var(--muted); font-size: 13px; text-transform: uppercase; }
    .metric strong { display: block; margin-top: 8px; font-size: 28px; }
    .section-title { margin: 0 0 14px; font-size: 22px; line-height: 1.1; }
    .limits .metric strong { font-size: 18px; overflow-wrap: anywhere; }
    .table-wrap { border: 1px solid var(--line); background: var(--panel); overflow-x: auto; }
    table { width: 100%; border-collapse: collapse; min-width: 720px; }
    th, td { text-align: left; padding: 13px 16px; border-bottom: 1px solid var(--line); vertical-align: top; }
    th { color: var(--muted); font-size: 12px; text-transform: uppercase; background: #ede5d6; }
    tr:last-child td { border-bottom: 0; }
    code { color: var(--code); background: #ece6d9; border: 1px solid #dbd2c2; border-radius: 4px; padding: 2px 5px; }
    .pill { display: inline-block; border: 1px solid var(--accent); color: var(--accent); border-radius: 999px; padding: 2px 8px; font-size: 13px; }
    .empty, .error { padding: 26px; border: 1px solid var(--line); background: var(--panel); color: var(--muted); }
    .error { border-color: var(--hot); color: var(--hot); }
    [hidden] { display: none !important; }
    @media (max-width: 760px) {
      .shell { grid-template-columns: 1fr; }
      aside { border-right: 0; border-bottom: 1px solid var(--line); padding: 18px 20px; }
      .topbar { flex-direction: column; }
      .summary { grid-template-columns: 1fr; }
      .claim-row { flex-direction: column; }
      .claim-row button { width: 100%; }
      .metric { border-right: 0; border-bottom: 1px solid var(--line); }
      .metric:last-child { border-bottom: 0; }
    }
  </style>
</head>
<body>
  <div class="shell">
    <aside>
      <div class="brand">Plumtree</div>
      <div class="status">
        <span>Signed in</span>
        <strong id="owner">-</strong>
        <span id="identity">Waiting for Shoo</span>
      </div>
      <nav aria-label="Primary">
        <a href="/dashboard">Apps</a>
      </nav>
    </aside>
    <main>
      <div class="topbar">
        <h1>Apps</h1>
        <div class="actions">
          <a id="login" class="login" href="#">Sign in</a>
          <button id="signout" class="secondary" hidden>Sign out</button>
        </div>
      </div>
      <section class="summary" aria-label="Account summary">
        <div class="metric"><span>Total apps</span><strong id="total">-</strong></div>
        <div class="metric"><span>Active deploys</span><strong id="active">-</strong></div>
      </section>
      {{if .Limits}}
      <h2 class="section-title">Platform limits</h2>
      <section class="summary limits" aria-label="Platform limits">
        {{range .Limits}}<div class="metric"><span>{{.Label}}</span><strong>{{.Value}}</strong></div>
        {{end}}
      </section>
      {{end}}
      <section id="handleSetup" class="claim" hidden>
        <h2>Choose your handle</h2>
        <form id="handleForm">
          <div class="claim-row">
            <input id="handleInput" name="handle" autocomplete="username" autocapitalize="none" spellcheck="false" placeholder="alice" pattern="[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?" required>
            <button type="submit">Save handle</button>
          </div>
          <p class="hint">Lowercase letters, digits, and single dashes. This becomes your app namespace.</p>
        </form>
      </section>
      <div id="error" class="error" hidden></div>
      <div id="empty" class="empty" hidden>No apps in this namespace yet.</div>
      <div id="apps" class="table-wrap" hidden>
        <table>
          <thead><tr><th>Handle</th><th>Active deploy</th><th>Created</th></tr></thead>
          <tbody id="rows"></tbody>
        </table>
      </div>
    </main>
  </div>
  <script>
    const login = document.getElementById("login");
    const signout = document.getElementById("signout");
    const owner = document.getElementById("owner");
    const identityLabel = document.getElementById("identity");
    const total = document.getElementById("total");
    const active = document.getElementById("active");
    const apps = document.getElementById("apps");
    const rows = document.getElementById("rows");
    const empty = document.getElementById("empty");
    const errorBox = document.getElementById("error");
    const summary = document.querySelector(".summary");
    const handleSetup = document.getElementById("handleSetup");
    const handleForm = document.getElementById("handleForm");
    const handleInput = document.getElementById("handleInput");
    let activeToken = "";

    login.href = "https://shoo.dev/authorize?redirect_uri=" + encodeURIComponent(new URL("/shoo/callback", window.location.origin).href);
    login.addEventListener("click", (event) => {
      if (window.Shoo && window.Shoo.startSignIn) {
        event.preventDefault();
        window.Shoo.startSignIn({ returnTo: "/dashboard" });
      }
    });
    signout.addEventListener("click", () => {
      window.Shoo && window.Shoo.clearIdentity && window.Shoo.clearIdentity();
      window.location.href = "/dashboard";
    });

    async function identity() {
      for (let i = 0; i < 12; i++) {
        const value = window.Shoo && window.Shoo.getIdentity && window.Shoo.getIdentity();
        if (value && value.token) return value;
        await new Promise(resolve => setTimeout(resolve, 150));
      }
      return null;
    }

    async function api(path, token, options = {}) {
      const headers = Object.assign({ Authorization: "Bearer " + token }, options.headers || {});
      const response = await fetch(path, Object.assign({}, options, { headers }));
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error || response.statusText);
      return body;
    }

    function showError(message) {
      errorBox.textContent = message;
      errorBox.hidden = false;
    }

    function clearError() {
      errorBox.textContent = "";
      errorBox.hidden = true;
    }

    function renderApps(items) {
      rows.textContent = "";
      for (const app of items) {
        const tr = document.createElement("tr");
        tr.innerHTML = "<td><code></code></td><td></td><td></td>";
        tr.children[0].firstChild.textContent = app.handle;
        tr.children[1].textContent = app.activeDeployId || "-";
        tr.children[2].textContent = app.createdAt ? new Date(app.createdAt).toLocaleString() : "-";
        rows.appendChild(tr);
      }
      apps.hidden = items.length === 0;
      empty.hidden = items.length !== 0;
      total.textContent = String(items.length);
      active.textContent = String(items.filter(app => app.activeDeployId).length);
    }

    function showHandleSetup() {
      summary.hidden = true;
      apps.hidden = true;
      empty.hidden = true;
      handleSetup.hidden = false;
      owner.textContent = "-";
      identityLabel.textContent = "Shoo verified";
      total.textContent = "-";
      publicCount.textContent = "-";
      active.textContent = "-";
      handleInput.focus();
    }

    async function loadApps(token, me) {
      handleSetup.hidden = true;
      summary.hidden = false;
      owner.textContent = me.owner.handle;
      identityLabel.textContent = me.auth.provider === "shoo" ? "Shoo verified" : "Signed in";
      const listing = await api("/api/apps", token);
      renderApps(listing.apps || []);
    }

    async function boot() {
      const auth = await identity();
      if (!auth) {
        login.hidden = false;
        signout.hidden = true;
        handleSetup.hidden = true;
        summary.hidden = false;
        owner.textContent = "-";
        identityLabel.textContent = "Not signed in";
        renderApps([]);
        return;
      }
      activeToken = auth.token;
      login.hidden = true;
      signout.hidden = false;
      if (window.location.pathname === "/shoo/callback") {
        window.history.replaceState({}, "", "/dashboard");
      }
      try {
        clearError();
        const me = await api("/api/me", auth.token);
        if (me.owner.needsHandle) {
          showHandleSetup();
          return;
        }
        await loadApps(auth.token, me);
      } catch (error) {
        showError(error.message);
        renderApps([]);
      }
    }
    handleForm.addEventListener("submit", async (event) => {
      event.preventDefault();
      clearError();
      const handle = handleInput.value.trim();
      try {
        const me = await api("/api/me/handle", activeToken, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ handle })
        });
        await loadApps(activeToken, Object.assign({ auth: { provider: "shoo" } }, me));
      } catch (error) {
        showError(error.message);
      }
    });
    boot();
  </script>
</body>
</html>`))
