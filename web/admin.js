const state = { items: [], filter: "all" };
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
  const response = await fetch(url, options);
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
  if (!items.length) {
    list.innerHTML = '<tr><td colspan="5" class="empty-cell">Nothing here yet. Create something beautiful.</td></tr>';
    return;
  }
  list.innerHTML = items.map((item) => {
    const icon = item.type === "post" ? "✎" : "□";
    const status = escapeHTML(item.status || "published");
    return `<tr>
      <td><a class="item-title" href="${escapeHTML(item.editor_url)}"><span class="item-icon">${icon}</span><span>${escapeHTML(item.title)}<small class="item-subtitle">${escapeHTML(item.path)}</small></span></a></td>
      <td><span class="type-label">${escapeHTML(item.type)}</span></td>
      <td>${escapeHTML(item.date)}</td>
      <td><span class="status ${status === "draft" ? "draft" : ""}">${status}</span></td>
      <td><div class="row-actions"><a href="${escapeHTML(item.editor_url)}">Edit</a><a href="${escapeHTML(item.url)}" target="_blank" rel="noreferrer">View ↗</a></div></td>
    </tr>`;
  }).join("");
}

async function loadDashboard() {
  try {
    const [site, items] = await Promise.all([getJSON("/_cms/api/site"), getJSON("/_cms/api/items")]);
    state.items = items.items || [];
    renderSite(site);
    renderItems();
  } catch (error) {
    showToast(error.message, true);
  }
}

$("#build-button")?.addEventListener("click", async () => {
  const button = $("#build-button");
  button.disabled = true;
  button.innerHTML = '<span class="button-icon">…</span> Building';
  try {
    const data = await getJSON("/_cms/api/build", { method: "POST" });
    showToast(`Built ${data.build.files} files.`);
    await loadDashboard();
  } catch (error) {
    showToast(error.message, true);
  } finally {
    button.disabled = false;
    button.innerHTML = '<span class="button-icon">↻</span> Build site';
  }
});

document.querySelectorAll(".filter-pill").forEach((button) => {
  button.addEventListener("click", () => {
    document.querySelectorAll(".filter-pill").forEach((pill) => pill.classList.remove("active"));
    button.classList.add("active");
    state.filter = button.dataset.filter;
    renderItems();
  });
});

loadDashboard();
