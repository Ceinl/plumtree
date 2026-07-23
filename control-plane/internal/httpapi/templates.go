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
    button, table, input, textarea { font: inherit; letter-spacing: 0; }
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
    .section { margin-top: 36px; }
    .section h2 { margin: 0 0 14px; font-size: 26px; }
    .key-form {
      display: grid;
      grid-template-columns: minmax(140px, 220px) 1fr auto;
      gap: 10px;
      align-items: start;
      margin-bottom: 14px;
    }
    .key-form input, .key-form textarea {
      width: 100%;
      min-height: 40px;
      border: 1px solid var(--ink);
      border-radius: 6px;
      background: #fffaf0;
      color: var(--ink);
      padding: 8px 10px;
    }
    .key-form textarea { min-height: 74px; resize: vertical; font-family: ui-monospace, monospace; font-size: 13px; }
    .hint { margin: 0; color: var(--muted); font-size: 13px; }
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
      .claim-row { flex-direction: column; }
      .claim-row button { width: 100%; }
      .key-form { grid-template-columns: 1fr; }
      .key-form button { width: 100%; }
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
        <a href="#appsSection">Apps</a>
        <a href="#sshKeysSection">SSH Keys</a>
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
      <section id="appsSection">
        <div id="empty" class="empty" hidden>No apps in this namespace yet.</div>
        <div id="apps" class="table-wrap" hidden>
          <table>
            <thead><tr><th>Handle</th><th>Active deploy</th><th>Connections / day</th><th>Created</th></tr></thead>
            <tbody id="rows"></tbody>
          </table>
        </div>
      </section>
      <section id="sshKeysSection" class="section" hidden>
        <h2>SSH Keys</h2>
        <form id="sshKeyForm" class="key-form">
          <input id="sshKeyName" name="name" placeholder="laptop" pattern="[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?" required>
          <textarea id="sshPublicKey" name="publicKey" placeholder="ssh-ed25519 AAAA..." required></textarea>
          <button type="submit">Add key</button>
        </form>
        <p class="hint">Paste an OpenSSH public key. Private keys never leave your device.</p>
        <div id="sshKeysEmpty" class="empty">No SSH keys registered.</div>
        <div id="sshKeys" class="table-wrap" hidden>
          <table>
            <thead><tr><th>Name</th><th>Fingerprint</th><th>Added</th><th></th></tr></thead>
            <tbody id="sshKeyRows"></tbody>
          </table>
        </div>
      </section>
    </main>
  </div>
  <script nonce="{{.CSPNonce}}">
    const login = document.getElementById("login");
    const signout = document.getElementById("signout");
    const owner = document.getElementById("owner");
    const identityLabel = document.getElementById("identity");
    const apps = document.getElementById("apps");
    const rows = document.getElementById("rows");
    const empty = document.getElementById("empty");
    const errorBox = document.getElementById("error");
    const handleSetup = document.getElementById("handleSetup");
    const handleForm = document.getElementById("handleForm");
    const handleInput = document.getElementById("handleInput");
    const sshKeysSection = document.getElementById("sshKeysSection");
    const sshKeyForm = document.getElementById("sshKeyForm");
    const sshKeyName = document.getElementById("sshKeyName");
    const sshPublicKey = document.getElementById("sshPublicKey");
    const sshKeys = document.getElementById("sshKeys");
    const sshKeysEmpty = document.getElementById("sshKeysEmpty");
    const sshKeyRows = document.getElementById("sshKeyRows");
    let activeToken = "";
    let appsStream = null;

    // openAppsStream subscribes to live app updates over SSE so per-app
    // connection counts refresh on change without re-fetching. fetch is used
    // instead of EventSource because it keeps the bearer out of URLs.
    function openAppsStream(token) {
      closeAppsStream();
      const stream = { controller: new AbortController(), closed: false };
      appsStream = stream;
      consumeAppsStream(token, stream);
    }

    async function consumeAppsStream(token, stream) {
      while (!stream.closed) {
        try {
          const response = await fetch("/api/apps/stream", {
            headers: { Authorization: "Bearer " + token, Accept: "text/event-stream" },
            cache: "no-store",
            signal: stream.controller.signal
          });
          if (!response.ok || !response.body) {
            throw new Error("live updates unavailable (" + response.status + ")");
          }
          const reader = response.body.getReader();
          const decoder = new TextDecoder();
          let buffered = "";
          while (!stream.closed) {
            const chunk = await reader.read();
            if (chunk.done) break;
            buffered += decoder.decode(chunk.value, { stream: true });
            let boundary;
            while ((boundary = buffered.indexOf("\n\n")) !== -1) {
              const event = buffered.slice(0, boundary);
              buffered = buffered.slice(boundary + 2);
              const data = event.split("\n")
                .map(line => line.replace(/\r$/, ""))
                .filter(line => line.startsWith("data:"))
                .map(line => line.slice(5).trimStart())
                .join("\n");
              if (!data) continue;
              try {
                renderApps(JSON.parse(data).apps || []);
              } catch (e) { /* ignore malformed frame */ }
            }
          }
        } catch (error) {
          if (!stream.closed && error.name !== "AbortError") {
            // Keep the dashboard live through transient network interruptions.
          }
        }
        if (!stream.closed) await new Promise(resolve => setTimeout(resolve, 1000));
      }
    }

    function closeAppsStream() {
      if (appsStream) {
        appsStream.closed = true;
        appsStream.controller.abort();
        appsStream = null;
      }
    }

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
        tr.innerHTML = "<td><code></code></td><td></td><td></td><td></td>";
        tr.children[0].firstChild.textContent = app.handle;
        tr.children[1].textContent = app.activeDeployId || "-";
        tr.children[2].textContent = app.connectionsPerDay > 0
          ? app.connectionsToday + " / " + app.connectionsPerDay
          : app.connectionsToday + " / ∞";
        tr.children[3].textContent = app.createdAt ? new Date(app.createdAt).toLocaleString() : "-";
        rows.appendChild(tr);
      }
      apps.hidden = items.length === 0;
      empty.hidden = items.length !== 0;
    }

    function renderSSHKeys(items) {
      sshKeyRows.textContent = "";
      for (const key of items) {
        const tr = document.createElement("tr");
        tr.innerHTML = "<td></td><td><code></code></td><td></td><td><button class=\"secondary\" type=\"button\">Revoke</button></td>";
        tr.children[0].textContent = key.name;
        tr.children[1].firstChild.textContent = key.fingerprint;
        tr.children[2].textContent = key.createdAt ? new Date(key.createdAt).toLocaleString() : "-";
        tr.children[3].firstChild.addEventListener("click", () => revokeSSHKey(key.id));
        sshKeyRows.appendChild(tr);
      }
      sshKeys.hidden = items.length === 0;
      sshKeysEmpty.hidden = items.length !== 0;
    }

    async function loadSSHKeys(token) {
      const listing = await api("/api/me/ssh-keys", token);
      renderSSHKeys(listing.sshKeys || []);
      sshKeysSection.hidden = false;
    }

    async function revokeSSHKey(id) {
      clearError();
      try {
        await api("/api/me/ssh-keys/" + encodeURIComponent(id), activeToken, { method: "DELETE" });
        await loadSSHKeys(activeToken);
      } catch (error) {
        showError(error.message);
      }
    }

    function showHandleSetup() {
      closeAppsStream();
      apps.hidden = true;
      empty.hidden = true;
      handleSetup.hidden = false;
      owner.textContent = "-";
      identityLabel.textContent = "Shoo verified";
      handleInput.focus();
    }

    async function loadApps(token, me) {
      handleSetup.hidden = true;
      owner.textContent = me.owner.handle;
      identityLabel.textContent = me.auth.provider === "shoo" ? "Shoo verified" : "Signed in";
      const listing = await api("/api/apps", token);
      renderApps(listing.apps || []);
      openAppsStream(token);
    }

    async function boot() {
      const auth = await identity();
      if (!auth) {
        closeAppsStream();
        login.hidden = false;
        signout.hidden = true;
        handleSetup.hidden = true;
        sshKeysSection.hidden = true;
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
        await loadSSHKeys(auth.token);
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
    sshKeyForm.addEventListener("submit", async (event) => {
      event.preventDefault();
      clearError();
      try {
        await api("/api/me/ssh-keys", activeToken, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ name: sshKeyName.value.trim(), publicKey: sshPublicKey.value.trim() })
        });
        sshKeyForm.reset();
        await loadSSHKeys(activeToken);
      } catch (error) {
        showError(error.message);
      }
    });
    boot();
  </script>
</body>
</html>`))
