import { esc } from "../ui.js";

export async function render(root, _ctx, email, retry) {
  root.innerHTML = `
    <div class="screen auth blocked">
      <img class="logo" src="img/logo.png" alt="" width="200" height="178">
      <h1>Not allowed</h1>
      <p><span class="email">${esc(email)}</span> isn't on the list for Mandamás.</p>
      <button class="google-btn" id="retry"><span>Use another account</span></button>
    </div>`;
  root.querySelector("#retry").addEventListener("click", retry);
}
