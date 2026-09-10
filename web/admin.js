const state = { items: [], filter: "all", themes: [] };
const $ = (selector) => document.querySelector(selector);

function escapeHTML(value) {
  return String(value ?? "").replace(/[&<>\"']/g, (char) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#039;"
  }[char]));
}

function showToast(message, error = false) {
  const toast = $("#toast");
  toast.textContent = message;
  toast.classList.toggle("error", error);
  toast.classList.add("visible");
  window.clearTimeout(showToast.timer);
  showToast.timer = window.setTimeout(() => toast.classList.remove("visible"), 3200);
}

async function getJSON(url, options) {
  const response = await fetch(url, { cache: "no-store", ...(options || {}) });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data.error || response.statusText);
  return data;
}

function renderSite(data) {
  const site = data.site || {};
  $("#site-title").textContent = site.title || "Your site is a folder of files.";
  $("#site-description").textContent = site.description || "HTML-first static publishing without a database.";
  $("#posts-count").textContent = data.posts ?? 0;
  $("#pages-count").textContent = data.pages ?? 0;
  $("#media-count").textContent = data.media_count ?? 0;
  $("#published-count").textContent = data.last_build?.published ?? 0;
  $("#last-built").textContent = data.last_build?.generated_at
    ? `Built ${new Date(data.last_build.generated_at).toLocaleString()}`
    : "Not built yet";
  renderThemes(data.themes || []);
  renderGit(data.git || {});
}

function renderThemes(themes) {
  state.themes = themes;
  const list = $("#theme-list");
  if (!themes.length) { list.innerHTML = '<p class="empty-cell">No themes found.</p>'; return; }
  list.innerHTML = themes.map((theme) => `<article class="theme-card ${theme.active ? "active" : ""}">
    <div class="theme-swatch theme-${escapeHTML(theme.name)}"><span>✦</span></div>
    <div class="theme-card-copy"><div class="theme-card-title"><h3>${escapeHTML(theme.title)}</h3>${theme.active ? '<span class="active-label">Active</span>' : ""}</div><p>${escapeHTML(theme.description || "A Fileloom theme.")}</p><small>${escapeHTML(theme.license || "Local")}</small></div>
    <div class="theme-card-actions">${theme.active ? '<span class="theme-current">In use</span>' : `<button class="button button-quiet button-small use-theme" data-theme="${escapeHTML(theme.name)}">Use theme</button>`}<a href="/_cms/editor?theme=${encodeURIComponent(theme.name)}">Edit visually ↗</a></div>
  </article>`).join("");
  list.querySelectorAll(".use-theme").forEach((button) => button.addEventListener("click", async () => {
    button.disabled = true;
    try {
      await getJSON("/_cms/api/themes/activate", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: `name=${encodeURIComponent(button.dataset.theme)}` });
      showToast(`Activated ${button.dataset.theme}.`);
      await loadDashboard();
    } catch (error) { showToast(error.message, true); button.disabled = false; }
  }));
}

function renderGit(data) {
  const status = data.status || data || {};
  const config = data.config || {};
  const pill = $("#git-status-pill");
  if (!status.available) {
    pill.textContent = "Unavailable"; pill.className = "status draft";
    $("#git-summary").textContent = status.error || "This site is not inside a Git repository.";
  } else {
    pill.textContent = status.clean ? "Clean" : `${status.changes} change${status.changes === 1 ? "" : "s"}`;
    pill.className = `status ${status.clean ? "" : "draft"}`;
    $("#git-summary").textContent = config.auto_commit ? `Auto-commit is enabled on ${config.commit_on || "build"}.` : "Manual commits are available; auto-commit and push are opt-in.";
  }
  $("#git-branch").textContent = status.branch || "—";
  $("#git-changes").textContent = status.available ? String(status.changes ?? 0) : "—";
  $("#git-remote").textContent = status.remote || "none";
  $("#git-commit").disabled = !status.available;
  $("#git-push").disabled = !status.available || !status.remote;
  $("#git-configure").onclick = () => $("#git-dialog")?.showModal();
  $("#git-auto-commit").checked = !!config.auto_commit;
  $("#git-auto-push").checked = !!config.auto_push;
  $("#git-commit-on").value = config.commit_on || "build";
  $("#git-remote-name").value = config.remote || status.remote_name || "origin";
  $("#git-branch-name").value = config.branch || status.branch || "";
  $("#git-remote-url").value = config.remote_url || status.remote || "";
}

function filteredItems() {
  return state.items.filter((item) => {
    if (state.filter === "all") return true;
    if (state.filter === "draft") return item.status === "draft";
    return item.type === state.filter;
  });
}

function renderItems() {
  const list = $("#content-list");
  const items = filteredItems();
  if (!items.length) { list.innerHTML = '<tr><td colspan="5" class="empty-cell">Nothing here yet. Create something beautiful.</td></tr>'; return; }
  list.innerHTML = items.map((item) => {
    const icon = item.type === "post" ? "✎" : "□";
    const status = escapeHTML(item.status || "published");
    const publish = item.status === "draft" ? `<a href="#" data-publish="${escapeHTML(item.path)}">Publish</a>` : "";
    return `<tr>
      <td><a class="item-title" href="${escapeHTML(item.editor_url)}"><span class="item-icon">${icon}</span><span>${escapeHTML(item.title)}<small class="item-subtitle">${escapeHTML(item.path)}</small></span></a></td>
      <td><span class="type-label">${escapeHTML(item.type)}</span></td>
      <td>${escapeHTML(item.date)}</td>
      <td><span class="status ${status === "draft" ? "draft" : ""}">${status}</span></td>
      <td><div class="row-actions">${publish}<a href="${escapeHTML(item.editor_url)}">Edit</a><a href="${escapeHTML(item.url)}" target="_blank" rel="noreferrer">View ↗</a></div></td>
    </tr>`;
  }).join("");
  list.querySelectorAll("[data-publish]").forEach((link) => link.addEventListener("click", async (event) => {
    event.preventDefault();
    try { await getJSON("/_cms/api/items/status", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: `path=${encodeURIComponent(link.dataset.publish)}&status=published` }); showToast("Published."); await loadDashboard(); }
    catch (error) { showToast(error.message, true); }
  }));
}

async function loadDashboard() {
  try {
    const [site, items] = await Promise.all([getJSON("/_cms/api/site"), getJSON("/_cms/api/items")]);
    state.items = items.items || [];
    renderSite(site);
    renderItems();
  } catch (error) { showToast(error.message, true); }
}

$("#build-button")?.addEventListener("click", async () => {
  const button = $("#build-button"); button.disabled = true; button.innerHTML = '<span class="button-icon">…</span> Building';
  try { const data = await getJSON("/_cms/api/build", { method: "POST" }); showToast(`Built ${data.build.files} files.`); await loadDashboard(); }
  catch (error) { showToast(error.message, true); }
  finally { button.disabled = false; button.innerHTML = '<span class="button-icon">↻</span> Build site'; }
});

$("#git-configure")?.addEventListener("click", () => $("#git-dialog")?.showModal());
$("#git-cancel")?.addEventListener("click", () => $("#git-dialog")?.close());
$("#git-config-form")?.addEventListener("submit", async (event) => {
  event.preventDefault();
  const form = event.currentTarget;
  const payload = new URLSearchParams();
  ["enabled", "auto_commit", "auto_push"].forEach((name) => payload.set(name, form.elements[name].checked ? "true" : "false"));
  ["commit_on", "remote", "branch", "remote_url"].forEach((name) => payload.set(name, form.elements[name].value));
  const button = $("#git-save"); button.disabled = true;
  try {
    await getJSON("/_cms/api/git/config", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: payload.toString() });
    $("#git-dialog").close(); showToast("Git settings saved."); await loadDashboard();
  } catch (error) { showToast(error.message, true); }
  finally { button.disabled = false; }
});

$("#git-commit")?.addEventListener("click", async () => {
  try { const data = await getJSON("/_cms/api/git/commit", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: "message=Manual+Fileloom+update" }); showToast(data.committed ? "Committed source changes." : "Nothing to commit."); await loadDashboard(); }
  catch (error) { showToast(error.message, true); }
});

$("#git-push")?.addEventListener("click", async () => {
  if (!confirm("Push the current branch to its configured remote?")) return;
  try { await getJSON("/_cms/api/git/push", { method: "POST" }); showToast("Pushed."); await loadDashboard(); }
  catch (error) { showToast(error.message, true); }
});

document.querySelectorAll(".filter-pill").forEach((button) => button.addEventListener("click", () => {
  document.querySelectorAll(".filter-pill").forEach((pill) => pill.classList.remove("active"));
  button.classList.add("active"); state.filter = button.dataset.filter; renderItems();
}));

loadDashboard();
