const state = { items: [], filter: "all", themes: [], checks: [], gitDirty: false, commentsDirty: false, tokenTheme: "", tokenSHA: "" };
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
  showToast.timer = window.setTimeout(() => toast.classList.remove("visible"), 4200);
}

async function getJSON(url, options) {
  const response = await fetch(url, { cache: "no-store", ...(options || {}) });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data.error || response.statusText);
  return data;
}

function renderSite(data) {
  const site = data.site || {};
  const checks = data.last_build?.checks || [];
  state.checks = checks;
  $("#site-title").textContent = site.title || "Your site is a folder of files.";
  $("#site-description").textContent = site.description || "HTML-first static publishing without a database.";
  $("#posts-count").textContent = data.posts ?? 0;
  $("#pages-count").textContent = data.pages ?? 0;
  $("#media-count").textContent = data.media_count ?? 0;
  $("#published-count").textContent = data.last_build?.published ?? 0;
  $("#last-built").textContent = data.last_build?.generated_at
    ? `Built ${new Date(data.last_build.generated_at).toLocaleString()}`
    : "Not built yet";
  $("#check-count").textContent = checks.length ? `${checks.length} check${checks.length === 1 ? "" : "s"}` : "No check issues";
  renderThemes(data.themes || []);
  renderGit(data.git || {});
  renderComments(site);
}

function renderThemes(themes) {
  state.themes = themes;
  const list = $("#theme-list");
  if (!themes.length) { list.innerHTML = '<p class="empty-cell">No themes found.</p>'; return; }
  list.innerHTML = themes.map((theme) => `<article class="theme-card ${theme.active ? "active" : ""}">
    <div class="theme-swatch theme-${escapeHTML(theme.name)}"><span>✦</span></div>
    <div class="theme-card-copy"><div class="theme-card-title"><h3>${escapeHTML(theme.title)}</h3>${theme.active ? '<span class="active-label">Active</span>' : ""}</div><p>${escapeHTML(theme.description || "A Fileloom theme.")}</p><small>${escapeHTML(theme.license || "Local")}</small></div>
    <div class="theme-card-actions">${theme.active ? '<span class="theme-current">In use</span>' : `<button class="button button-quiet button-small use-theme" data-theme="${escapeHTML(theme.name)}">Use theme</button>`}<button class="button button-quiet button-small token-theme" data-theme="${escapeHTML(theme.name)}">Style tokens</button><a href="/_cms/editor?theme=${encodeURIComponent(theme.name)}">Edit visually ↗</a></div>
  </article>`).join("");
  list.querySelectorAll(".use-theme").forEach((button) => button.addEventListener("click", async () => {
    button.disabled = true;
    try {
      await getJSON("/_cms/api/themes/activate", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: `name=${encodeURIComponent(button.dataset.theme)}` });
      showToast(`Activated ${button.dataset.theme}.`);
      await loadDashboard();
    } catch (error) { showToast(error.message, true); button.disabled = false; }
  }));
  list.querySelectorAll(".token-theme").forEach((button) => button.addEventListener("click", () => openThemeTokens(button.dataset.theme)));
}

function syncGitDependencies() {
  const enabled = $("#git-enabled").checked;
  const autoCommit = $("#git-auto-commit");
  const autoPush = $("#git-auto-push");
  autoCommit.disabled = !enabled;
  autoPush.disabled = !enabled || !autoCommit.checked;
  if (!enabled) autoPush.checked = false;
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
  $("#git-init").hidden = !!status.available;
  $("#git-enabled").checked = !!config.enabled;
  $("#git-auto-commit").checked = !!config.auto_commit;
  $("#git-auto-push").checked = !!config.auto_push;
  $("#git-commit-on").value = config.commit_on || "build";
  $("#git-remote-name").value = config.remote || status.remote_name || "origin";
  $("#git-branch-name").value = config.branch || status.branch || "";
  $("#git-remote-url").value = config.remote_url || status.remote || "";
  state.gitDirty = false;
  syncGitDependencies();
}

function syncCommentsDependencies() {
  const enabled = $("#comments-enabled").checked;
  $("#comments-server").required = enabled;
  $("#comments-site").required = enabled;
}

function renderComments(site) {
  const config = site?.comments || site || {};
  const enabled = !!config.enabled;
  const pill = $("#comments-status-pill");
  pill.textContent = enabled ? "Enabled" : "Off";
  pill.className = `status ${enabled ? "" : "draft"}`;
  $("#comments-provider").textContent = config.provider || "Artalk";
  $("#comments-site-key").textContent = config.site || "—";
  $("#comments-summary").textContent = enabled
    ? "Artalk is enabled for this site. Pages and posts can opt out individually from the editor Details panel."
    : "Comments are disabled. No comments widget or comment data is part of this site.";
  $("#comments-enabled").checked = enabled;
  $("#comments-server").value = config.server || "";
  $("#comments-site").value = config.site || "";
  $("#comments-origin").textContent = config.base_url || window.location.origin;
  state.commentsDirty = false;
  syncCommentsDependencies();
}

function filteredItems() {
  return state.items.filter((item) => {
    if (state.filter === "all") return true;
    if (state.filter === "draft") return item.status === "draft";
    return item.type === state.filter;
  });
}

function statusClass(status) {
  return status === "draft" || status === "private" || status === "scheduled" ? "draft" : "";
}

function renderItems() {
  const list = $("#content-list");
  const items = filteredItems();
  if (!items.length) { list.innerHTML = '<tr><td colspan="5" class="empty-cell">Nothing here yet. Create something beautiful.</td></tr>'; return; }
  list.innerHTML = items.map((item) => {
    const icon = item.type === "post" ? "✎" : "□";
    const status = escapeHTML(item.status || "published");
    const publish = item.status === "draft" || item.status === "private" ? `<a href="#" data-publish="${escapeHTML(item.path)}">Publish</a>` : "";
    const schedule = item.status === "scheduled" ? `<small class="item-subtitle">${escapeHTML(item.publish_at ? new Date(item.publish_at).toLocaleString() : "time missing")}</small>` : "";
    const scheduleAction = item.status === "scheduled" ? `<a href="#" data-now="${escapeHTML(item.path)}">Publish now</a>` : `<a href="#" data-schedule="${escapeHTML(item.path)}">Schedule</a>`;
    return `<tr>
      <td><a class="item-title" href="${escapeHTML(item.editor_url)}"><span class="item-icon">${icon}</span><span>${escapeHTML(item.title)}${schedule}<small class="item-subtitle">${escapeHTML(item.path)}</small></span></a></td>
      <td><span class="type-label">${escapeHTML(item.type)}</span></td>
      <td>${escapeHTML(item.date)}</td>
      <td><span class="status ${statusClass(item.status)}">${status}</span></td>
      <td><div class="row-actions">${publish}${scheduleAction}<a href="#" data-history="${escapeHTML(item.path)}" data-title="${escapeHTML(item.title)}">History</a><a href="${escapeHTML(item.editor_url)}">Edit</a><a href="${escapeHTML(item.url)}" target="_blank" rel="noreferrer">View ↗</a></div></td>
    </tr>`;
  }).join("");
  list.querySelectorAll("[data-publish]").forEach((link) => link.addEventListener("click", async (event) => {
    event.preventDefault();
    try { await changeStatus(link.dataset.publish, "published", ""); showToast("Published."); await loadDashboard(); }
    catch (error) { showToast(error.message, true); }
  }));
  list.querySelectorAll("[data-schedule]").forEach((link) => link.addEventListener("click", async (event) => {
    event.preventDefault();
    const value = prompt("Schedule for local date/time, e.g. 2026-09-12T09:00:");
    if (!value) return;
    try { await changeStatus(link.dataset.schedule, "scheduled", new Date(value).toISOString()); showToast("Scheduled."); await loadDashboard(); }
    catch (error) { showToast(error.message, true); }
  }));
  list.querySelectorAll("[data-now]").forEach((link) => link.addEventListener("click", async (event) => {
    event.preventDefault();
    try { await changeStatus(link.dataset.now, "published", ""); showToast("Published now."); await loadDashboard(); }
    catch (error) { showToast(error.message, true); }
  }));
  list.querySelectorAll("[data-history]").forEach((link) => link.addEventListener("click", (event) => { event.preventDefault(); openRevisions(link.dataset.history, link.dataset.title); }));
}

async function changeStatus(path, status, publishAt) {
  const payload = new URLSearchParams({ path, status });
  if (publishAt) payload.set("publish_at", publishAt);
  return getJSON("/_cms/api/items/status", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: payload.toString() });
}

async function loadDashboard() {
  try {
    const [site, items] = await Promise.all([getJSON("/_cms/api/site"), getJSON("/_cms/api/items")]);
    state.items = items.items || [];
    renderSite(site);
    renderItems();
  } catch (error) { showToast(error.message, true); }
}

function openCheckReport() {
  const list = $("#check-list");
  if (!state.checks.length) list.innerHTML = '<p class="empty-cell">No issues found in the latest build.</p>';
  else list.innerHTML = state.checks.map((issue) => `<article class="revision-row"><div><strong>${escapeHTML(issue.code)} · ${escapeHTML(issue.severity)}</strong><small>${escapeHTML(issue.path)}${issue.line ? `:${issue.line}` : ""} · ${escapeHTML(issue.message)}</small></div></article>`).join("");
  $("#check-dialog")?.showModal();
}
async function openRevisions(path, title) {
  const dialog = $("#revision-dialog");
  $("#revision-title").textContent = `History · ${title}`;
  const list = $("#revision-list");
  list.innerHTML = '<p class="empty-cell">Loading history…</p>';
  dialog.showModal();
  try {
    const data = await getJSON(`/_cms/api/revisions?path=${encodeURIComponent(path)}`);
    if (!data.revisions?.length) { list.innerHTML = '<p class="empty-cell">No revisions yet. A snapshot is created before the next change.</p>'; return; }
    list.innerHTML = data.revisions.map((revision) => `<article class="revision-row"><div><strong>${escapeHTML(new Date(revision.created_at).toLocaleString())}</strong><small>${escapeHTML(revision.reason || "Source change")} · ${escapeHTML(String(revision.size))} bytes</small></div><button class="button button-quiet button-small restore-revision" data-path="${escapeHTML(path)}" data-id="${escapeHTML(revision.id)}">Restore</button></article>`).join("");
    list.querySelectorAll(".restore-revision").forEach((button) => button.addEventListener("click", async () => {
      if (!confirm("Restore this revision? The current source will be snapshotted first.")) return;
      button.disabled = true;
      try {
        await getJSON("/_cms/api/revisions/restore", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: `path=${encodeURIComponent(button.dataset.path)}&id=${encodeURIComponent(button.dataset.id)}` });
        dialog.close(); showToast("Revision restored."); await loadDashboard();
      } catch (error) { showToast(error.message, true); button.disabled = false; }
    }));
  } catch (error) { list.innerHTML = `<p class="empty-cell">${escapeHTML(error.message)}</p>`; }
}

async function openThemeTokens(theme) {
  const dialog = $("#theme-token-dialog");
  const fields = $("#theme-token-fields");
  fields.innerHTML = '<p class="empty-cell">Loading tokens…</p>';
  $("#theme-token-title").textContent = `${theme} tokens`;
  dialog.showModal();
  try {
    const data = await getJSON(`/_cms/api/theme-tokens?theme=${encodeURIComponent(theme)}`);
    state.tokenTheme = theme; state.tokenSHA = data.sha256;
    fields.innerHTML = (data.tokens || []).map((token) => `<label class="token-field"><span>${escapeHTML(token.name)}</span><input name="${escapeHTML(token.name)}" data-token-name="${escapeHTML(token.name)}" value="${escapeHTML(token.value)}" required></label>`).join("") || '<p class="empty-cell">No custom properties found.</p>';
  } catch (error) { fields.innerHTML = `<p class="empty-cell">${escapeHTML(error.message)}</p>`; }
}

$("#build-button")?.addEventListener("click", async () => {
  const button = $("#build-button"); button.disabled = true; button.innerHTML = '<span class="button-icon">…</span> Building';
  try { const data = await getJSON("/_cms/api/build", { method: "POST" }); const count = data.build.checks?.length || 0; showToast(`Built ${data.build.files} files${count ? `; ${count} check issue${count === 1 ? "" : "s"}` : "."}`); await loadDashboard(); }
  catch (error) { showToast(error.message, true); }
  finally { button.disabled = false; button.innerHTML = '<span class="button-icon">↻</span> Build site'; }
});

$("#export-button")?.addEventListener("click", async () => {
  const button = $("#export-button"); button.disabled = true;
  try {
    const response = await fetch("/_cms/api/export", { method: "POST", cache: "no-store" });
    if (!response.ok) { const data = await response.json().catch(() => ({})); throw new Error(data.error || response.statusText); }
    const blob = await response.blob();
    const link = document.createElement("a"); link.href = URL.createObjectURL(blob); link.download = "fileloom-site.zip"; link.click(); URL.revokeObjectURL(link.href);
    showToast("Export downloaded.");
  } catch (error) { showToast(error.message, true); }
  finally { button.disabled = false; }
});

function openGitDialog() { state.gitDirty = false; $("#git-dialog")?.showModal(); syncGitDependencies(); }
function openCommentsDialog() {
  state.commentsDirty = false;
  $("#comments-dialog")?.showModal();
  syncCommentsDependencies();
}
function requestCommentsClose() {
  if (!state.commentsDirty || window.confirm("Discard unsaved comments settings?")) {
    state.commentsDirty = false;
    $("#comments-dialog")?.close();
  }
}
function requestGitClose() {
  if (!state.gitDirty) { $("#git-dialog")?.close(); return; }
  $("#discard-dialog")?.showModal();
}

$("#comments-configure")?.addEventListener("click", openCommentsDialog);
$("#comments-close")?.addEventListener("click", requestCommentsClose);
$("#comments-cancel")?.addEventListener("click", requestCommentsClose);
$("#comments-dialog")?.addEventListener("cancel", (event) => { event.preventDefault(); requestCommentsClose(); });
$("#comments-config-form")?.addEventListener("input", () => { state.commentsDirty = true; });
$("#comments-config-form")?.addEventListener("change", () => { state.commentsDirty = true; syncCommentsDependencies(); });
$("#comments-config-form")?.addEventListener("submit", async (event) => {
  event.preventDefault();
  const form = event.currentTarget;
  if (!form.reportValidity()) return;
  const server = form.elements.server.value.trim();
  if (form.elements.enabled.checked) {
    try {
      const parsed = new URL(server);
      if (!["http:", "https:"].includes(parsed.protocol) || parsed.username || parsed.password || parsed.search || parsed.hash) throw new Error();
    } catch {
      form.elements.server.setCustomValidity("Use an absolute http:// or https:// Artalk server URL without credentials, query, or fragment.");
      form.elements.server.reportValidity();
      form.elements.server.setCustomValidity("");
      return;
    }
  }
  const payload = new URLSearchParams({
    enabled: form.elements.enabled.checked ? "true" : "false",
    provider: "artalk",
    server,
    site: form.elements.site.value.trim(),
  });
  const button = $("#comments-save"); button.disabled = true;
  try {
    await getJSON("/_cms/api/comments/config", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: payload.toString() });
    state.commentsDirty = false;
    $("#comments-dialog").close();
    showToast(form.elements.enabled.checked ? "Comments enabled." : "Comments disabled.");
    await loadDashboard();
  } catch (error) { showToast(error.message, true); }
  finally { button.disabled = false; }
});

$("#git-close")?.addEventListener("click", requestGitClose);
$("#git-cancel")?.addEventListener("click", requestGitClose);
$("#git-dialog")?.addEventListener("cancel", (event) => { event.preventDefault(); requestGitClose(); });
$("#discard-dialog")?.addEventListener("close", (event) => { if (event.target.returnValue === "discard") { state.gitDirty = false; $("#git-dialog")?.close(); } });
$("#git-config-form")?.addEventListener("input", () => { state.gitDirty = true; });
$("#git-config-form")?.addEventListener("change", () => { state.gitDirty = true; syncGitDependencies(); });
$("#git-enabled")?.addEventListener("change", syncGitDependencies);
$("#git-auto-commit")?.addEventListener("change", syncGitDependencies);
$("#git-config-form")?.addEventListener("submit", async (event) => {
  event.preventDefault();
  const form = event.currentTarget;
  if (!form.reportValidity()) return;
  const remoteURL = form.elements.remote_url.value.trim();
  if (remoteURL && !(/^git@[^\s/:]+:[^\s]+$/.test(remoteURL) || /^(https?|ssh|git|file):\/\/[^\s]+$/.test(remoteURL))) {
    form.elements.remote_url.setCustomValidity("Use an https://, ssh://, git://, file://, or git@host:path remote URL."); form.elements.remote_url.reportValidity(); form.elements.remote_url.setCustomValidity(""); return;
  }
  if (form.elements.auto_push.checked && (!form.elements.enabled.checked || !form.elements.auto_commit.checked)) { showToast("Auto-push requires Git automation and auto-commit.", true); return; }
  const payload = new URLSearchParams();
  ["enabled", "auto_commit", "auto_push"].forEach((name) => payload.set(name, form.elements[name].checked ? "true" : "false"));
  ["commit_on", "remote", "branch", "remote_url"].forEach((name) => payload.set(name, form.elements[name].value));
  const button = $("#git-save"); button.disabled = true;
  try {
    await getJSON("/_cms/api/git/config", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: payload.toString() });
    state.gitDirty = false; $("#git-dialog").close(); showToast("Git settings saved."); await loadDashboard();
  } catch (error) { showToast(error.message, true); }
  finally { button.disabled = false; }
});

$("#git-init")?.addEventListener("click", async () => {
  if (!confirm("Initialize a separate Git repository inside site/?")) return;
  try { await getJSON("/_cms/api/git/init", { method: "POST" }); showToast("Initialized the site repository."); await loadDashboard(); }
  catch (error) { showToast(error.message, true); }
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

$("#check-count")?.addEventListener("click", openCheckReport);
$("#check-close")?.addEventListener("click", () => $("#check-dialog")?.close());
$("#revision-close")?.addEventListener("click", () => $("#revision-dialog")?.close());
$("#theme-token-close")?.addEventListener("click", () => $("#theme-token-dialog")?.close());
$("#theme-token-cancel")?.addEventListener("click", () => $("#theme-token-dialog")?.close());
$("#theme-token-form")?.addEventListener("submit", async (event) => {
  event.preventDefault();
  const tokens = {};
  event.currentTarget.querySelectorAll("[data-token-name]").forEach((input) => { tokens[input.dataset.tokenName] = input.value; });
  const button = event.currentTarget.querySelector("button[type=submit]"); button.disabled = true;
  try {
    await getJSON(`/_cms/api/theme-tokens?theme=${encodeURIComponent(state.tokenTheme)}`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ theme: state.tokenTheme, sha256: state.tokenSHA, tokens }) });
    $("#theme-token-dialog").close(); showToast("Theme tokens saved."); await loadDashboard();
  } catch (error) { showToast(error.message, true); }
  finally { button.disabled = false; }
});

document.querySelectorAll(".filter-pill").forEach((button) => button.addEventListener("click", () => {
  document.querySelectorAll(".filter-pill").forEach((pill) => pill.classList.remove("active"));
  button.classList.add("active"); state.filter = button.dataset.filter; renderItems();
}));

loadDashboard();
