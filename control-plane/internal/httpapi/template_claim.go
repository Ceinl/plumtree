package httpapi

import "html/template"

var claimTmpl = template.Must(template.New("claim").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Claim Plumtree Deploy</title>
  <script src="https://shoo.dev/shoo.js"></script>
  <style>
    :root { --ink: #1d2522; --muted: #66736e; --paper: #f6f3ec; --line: #d8d1c4; --panel: #fffdf7; --hot: #c7523a; }
    * { box-sizing: border-box; }
    html, body { margin: 0; min-height: 100%; background: var(--paper); color: var(--ink); }
    body { font-family: Charter, "Iowan Old Style", "Palatino Linotype", Georgia, serif; letter-spacing: 0; display: grid; place-items: center; padding: 24px; }
    main { width: min(520px, 100%); border: 1px solid var(--line); background: var(--panel); padding: 24px; }
    h1 { margin: 0 0 12px; font-size: 30px; line-height: 1.05; }
    p { margin: 0 0 16px; color: var(--muted); }
    button, input, a.button { font: inherit; letter-spacing: 0; }
    button, a.button {
      display: inline-block;
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
    form { display: flex; gap: 10px; align-items: flex-start; margin-top: 12px; }
    input {
      width: 100%;
      min-height: 40px;
      border: 1px solid var(--ink);
      border-radius: 6px;
      background: #fffaf0;
      color: var(--ink);
      padding: 8px 10px;
    }
    .error { color: var(--hot); }
    [hidden] { display: none !important; }
    @media (max-width: 560px) { form { flex-direction: column; } form button { width: 100%; } }
  </style>
</head>
<body>
  <main>
    <h1>Claim deploy</h1>
    <p id="status">Checking sign-in...</p>
    <a id="login" class="button" href="#" hidden>Sign in with Shoo</a>
    <form id="handleForm" hidden>
      <input id="handleInput" name="handle" autocomplete="username" autocapitalize="none" spellcheck="false" placeholder="alice" pattern="[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?" required>
      <button type="submit">Save handle</button>
    </form>
  </main>
  <script>
    const deployID = {{.DeployID}};
    const claimToken = {{.ClaimToken}};
    const statusLabel = document.getElementById("status");
    const login = document.getElementById("login");
    const handleForm = document.getElementById("handleForm");
    const handleInput = document.getElementById("handleInput");
    let activeToken = "";

    login.href = "https://shoo.dev/authorize?redirect_uri=" + encodeURIComponent(window.location.href);
    login.addEventListener("click", (event) => {
      if (window.Shoo && window.Shoo.startSignIn) {
        event.preventDefault();
        window.Shoo.startSignIn({ returnTo: window.location.pathname });
      }
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

    async function claim() {
      statusLabel.textContent = "Claiming deploy...";
      await api("/api/claims/" + encodeURIComponent(deployID) + "/" + encodeURIComponent(claimToken), activeToken, { method: "POST" });
      window.location.href = "/dashboard";
    }

    async function boot() {
      const auth = await identity();
      if (!auth) {
        statusLabel.textContent = "Sign in to claim this deploy. Claim links expire after 30 seconds.";
        login.hidden = false;
        return;
      }
      activeToken = auth.token;
      login.hidden = true;
      try {
        const me = await api("/api/me", activeToken);
        if (me.owner.needsHandle) {
          statusLabel.textContent = "Choose a handle to claim this deploy.";
          handleForm.hidden = false;
          handleInput.focus();
          return;
        }
        await claim();
      } catch (error) {
        statusLabel.textContent = error.message;
        statusLabel.className = "error";
      }
    }

    handleForm.addEventListener("submit", async (event) => {
      event.preventDefault();
      statusLabel.className = "";
      statusLabel.textContent = "Saving handle...";
      try {
        await api("/api/me/handle", activeToken, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ handle: handleInput.value.trim() })
        });
        handleForm.hidden = true;
        await claim();
      } catch (error) {
        statusLabel.textContent = error.message;
        statusLabel.className = "error";
      }
    });

    boot();
  </script>
</body>
</html>`))
