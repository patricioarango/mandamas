import { icons, toast } from "../ui.js";

export async function render(root, { api }) {
  root.innerHTML = `
    <div class="screen auth">
      <img class="logo" src="img/logo.png" alt="Mandamás logo" width="240" height="214">
      <h1>Mandamás</h1>
      <p>Write the task. Claude opens the PR.</p>
      <button class="google-btn" id="signin">${icons.google}<span>Continue with Google</span></button>
      <div class="fine">Only invited accounts can get in.</div>
    </div>`;
  root.querySelector("#signin").addEventListener("click", async (e) => {
    e.currentTarget.disabled = true;
    try { await api.signIn(); } catch (err) { console.error(err); toast("Sign-in failed. Try again."); }
    finally { e.currentTarget.disabled = false; }
  });
}
