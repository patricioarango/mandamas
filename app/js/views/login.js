import { icons } from "../ui.js";

export async function render(root, { api }) {
  root.innerHTML = `
    <div class="screen auth">
      <img class="logo" src="img/logo.png" alt="Mandamás logo" width="240" height="214">
      <h1>Mandamás</h1>
      <p>Write the task. Claude opens the PR.</p>
      <button class="google-btn" id="signin">${icons.google}<span>Continue with Google</span></button>
      <p class="auth-error" id="signin-error" role="alert" hidden></p>
      <div class="fine">Only invited accounts can get in.</div>
    </div>`;
  const btn = root.querySelector("#signin");
  const err = root.querySelector("#signin-error");
  const fail = (e) => { console.error(e); err.textContent = e.message || "Sign-in failed. Try again."; err.hidden = false; };
  btn.addEventListener("click", async () => {
    btn.disabled = true;
    err.hidden = true;
    try { await api.signIn(); } catch (e) { fail(e); } finally { btn.disabled = false; }
  });
  api.redirectError?.then((e) => e && fail(e));
}
