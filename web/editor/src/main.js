import { mountHtmlEditor } from "@deckflow/html-editor/ui";
import "./style.css";

const app = document.querySelector("#app");
const params = new URLSearchParams(window.location.search);
const path = params.get("path") || "";
const state = {
  path,
  record: null,
  editor: null,
  currentHTML: "",
  dirty: false,
  saving: false,
  selected: null,
  sheet: null,
};

const escapeHTML = (value) => String(value ?? "")
  .replaceAll("&", "&amp;")
  .replaceAll("<", "&lt;")
  .replaceAll(">", "&gt;")
  .replaceAll('"', "&quot;")
  .replaceAll("'", "&#39;");

function setViewportHeight() {
  const height = window.visualViewport?.height || window.innerHeight;
  document.documentElement.style.setProperty("--fileloom-vh", `${height}px`);
}

function setStatus(message, kind = "neutral") {
  const node = document.querySelector("#editor-status");
  if (!node) return;
  node.textContent = message;
  node.dataset.kind = kind;
}

function setDirty(dirty) {
  state.dirty = Boolean(dirty);
  document.querySelectorAll("[data-action=save]").forEach((button) => {
    button.disabled = state.saving || !state.dirty;
    button.classList.toggle("is-dirty", state.dirty);
  });
  document.querySelectorAll("[data-dirty-indicator]").forEach((node) => {
    node.hidden = !state.dirty;
  });
  if (state.dirty) setStatus("Unsaved changes", "dirty");
}

async function fetchJSON(url, options = {}) {
  const response = await fetch(url, { cache: "no-store", ...options });
  let payload = null;
  try {
    payload = await response.json();
  } catch {
    payload = { ok: false, error: `${response.status} ${response.statusText}` };
  }
  if (!response.ok) {
    const error = new Error(payload?.error || `${response.status} ${response.statusText}`);
    error.status = response.status;
    error.payload = payload;
    throw error;
  }
  return payload;
}

function editorDocument(record) {
  const css = String(record.stylesheet_css || "").replace(/<\/style/gi, "<\\/style");
  return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><style>${css}</style><style>html{background:#fff}body{min-height:100vh;margin:0}</style></head><body>${record.html || ""}</body></html>`;
}

function insertIntoBody(source, fragment) {
  const lower = source.toLowerCase();
  const end = lower.lastIndexOf("</body>");
  if (end >= 0) return `${source.slice(0, end)}\n${fragment}\n${source.slice(end)}`;
  return `${source}\n${fragment}\n`;
}

function blockFragment(type) {
  switch (type) {
    case "heading":
      return "<h2>New section</h2>";
    case "paragraph":
      return "<p>Write something here.</p>";
    case "quote":
      return "<blockquote>Share a thoughtful quote or important detail.</blockquote>";
    case "list":
      return "<ul><li>First item</li><li>Second item</li></ul>";
    case "code":
      return '<pre class="fileloom-code-block" data-fileloom-code data-language="javascript"><code class="language-javascript" data-language="javascript">// Write code here</code></pre>';
    case "divider":
      return "<hr>";
    case "link":
      return '<p><a href="/">Add a link</a></p>';
    default:
      return "<p>New content</p>";
  }
}

async function insertBlock(type) {
  if (!state.editor) return;
  const next = insertIntoBody(state.editor.getHtml(), blockFragment(type));
  await state.editor.setHtml(next);
  state.currentHTML = state.editor.getHtml();
  setDirty(true);
  closeSheet();
  setStatus("Block added", "dirty");
}

function flattenMedia(node, result = []) {
  if (!node) return result;
  if (node.type === "file") result.push(node);
  for (const child of node.items || []) flattenMedia(child, result);
  return result;
}

function mediaURL(file) {
  const value = String(file.path || "");
  return `/media${value.startsWith("/") ? value : `/${value}`}`;
}

async function uploadMedia(input) {
  const files = [...(input.files || [])];
  if (!files.length) return;
  setStatus(`Uploading ${files.length} file${files.length === 1 ? "" : "s"}…`, "busy");
  for (const file of files) {
    const form = new FormData();
    form.append("file", file, file.name);
    const response = await fetch("/_cms/api/media", { method: "POST", body: form });
    if (!response.ok) {
      let message = "Upload failed";
      try { message = (await response.json()).error || message; } catch {}
      throw new Error(message);
    }
  }
  setStatus("Media uploaded", "success");
  await openMediaSheet();
}

async function openMediaSheet() {
  openSheet("media");
  const content = document.querySelector("#sheet-content");
  content.innerHTML = '<div class="sheet-loading">Loading media…</div>';
  try {
    const tree = await fetchJSON("/_cms/api/media");
    const files = flattenMedia(tree).filter((file) => /\.(avif|gif|jpe?g|png|svg|webp)$/i.test(file.name || ""));
    content.innerHTML = `
      <div class="sheet-heading"><div><span class="eyebrow">Media library</span><h2>Choose an image</h2></div><label class="upload-button">Upload<input id="media-upload" type="file" accept="image/*" multiple></label></div>
      <div class="media-grid">${files.length ? files.map((file) => `<button class="media-card" data-media-url="${escapeHTML(mediaURL(file))}" data-media-name="${escapeHTML(file.name)}"><img src="${escapeHTML(mediaURL(file))}" alt=""><span>${escapeHTML(file.name)}</span></button>`).join("") : '<p class="empty-state">No images yet.</p>'}</div>`;
    content.querySelector("#media-upload")?.addEventListener("change", async (event) => {
      try { await uploadMedia(event.target); } catch (error) { setStatus(error.message, "error"); }
    });
  } catch (error) {
    content.innerHTML = `<p class="error-card">${escapeHTML(error.message)}</p>`;
  }
}

async function insertImage(url, name) {
  const fragment = `<figure class="fileloom-image"><img src="${escapeHTML(url)}" alt="${escapeHTML(name)}"><figcaption>${escapeHTML(name)}</figcaption></figure>`;
  const next = insertIntoBody(state.editor.getHtml(), fragment);
  await state.editor.setHtml(next);
  state.currentHTML = state.editor.getHtml();
  setDirty(true);
  closeSheet();
  setStatus("Image added", "dirty");
}

function openSheet(kind) {
  state.sheet = kind;
  const backdrop = document.querySelector("#sheet-backdrop");
  const sheet = document.querySelector("#editor-sheet");
  if (!backdrop || !sheet) return;
  backdrop.hidden = false;
  sheet.hidden = false;
  sheet.dataset.sheet = kind;
  if (kind === "blocks") {
    document.querySelector("#sheet-content").innerHTML = blockCatalogHTML();
  } else if (kind === "style") {
    document.querySelector("#sheet-content").innerHTML = styleSheetHTML();
  }
}

function closeSheet() {
  state.sheet = null;
  const backdrop = document.querySelector("#sheet-backdrop");
  const sheet = document.querySelector("#editor-sheet");
  if (backdrop) backdrop.hidden = true;
  if (sheet) sheet.hidden = true;
}

function blockCatalogHTML() {
  return `<div class="sheet-heading"><div><span class="eyebrow">Insert</span><h2>Choose a block</h2></div><button class="icon-button" data-action="close-sheet" aria-label="Close">×</button></div><div class="block-grid">${[
    ["heading", "Heading", "Aa"], ["paragraph", "Paragraph", "¶"], ["quote", "Quote", "❞"], ["list", "List", "☷"], ["code", "Code", "<>"], ["divider", "Divider", "—"], ["link", "Link", "↗"], ["media", "Image", "▧"],
  ].map(([type, label, icon]) => `<button class="block-card" data-block="${type}"><span>${icon}</span><strong>${label}</strong><small>Tap to add</small></button>`).join("")}</div>`;
}

function styleSheetHTML() {
  return `<div class="sheet-heading"><div><span class="eyebrow">Design</span><h2>${escapeHTML(state.record?.theme || "Theme")}</h2></div><button class="icon-button" data-action="close-sheet" aria-label="Close">×</button></div><div class="info-card"><strong>Theme controls stay in Fileloom</strong><p>Use the CMS Style tokens panel for site-wide colors, spacing, typography, and other theme variables. Element-level styling is available from the canvas when supported.</p><button class="secondary-button" data-action="cms">Open CMS</button></div>`;
}

function renderShell(record) {
  const title = escapeHTML(record.document?.title || record.path);
  app.innerHTML = `
    <div class="editor-shell">
      <header class="editor-topbar">
        <div class="topbar-leading"><button class="back-button" data-action="back" aria-label="Back to CMS">←<span class="desktop-only"> CMS</span></button><div class="brand-lockup"><span class="brand-mark">F</span><span class="desktop-only">FILELOOM</span></div></div>
        <div class="document-heading"><span class="eyebrow">Visual editor</span><strong title="${title}">${title}</strong><span class="dirty-dot" data-dirty-indicator hidden></span></div>
        <div class="topbar-actions"><span id="editor-status" data-kind="neutral">Ready</span><button class="secondary-button desktop-only" data-action="preview">Preview</button><button class="primary-button" data-action="save" disabled>Save</button><button class="icon-button mobile-only" data-action="more" aria-label="More">•••</button></div>
      </header>
      <main class="editor-workspace">
        <aside class="desktop-panel desktop-blocks"><div class="panel-heading"><div><span class="eyebrow">Add</span><h2>Blocks</h2></div></div>${blockCatalogHTML()}</aside>
        <section class="canvas-region"><div id="deckflow-canvas" aria-label="Editable page canvas"></div><div class="canvas-hint">Tap text to edit · Select an element for controls</div></section>
        <aside class="desktop-panel desktop-inspector"><div class="panel-heading"><div><span class="eyebrow">Inspect</span><h2>Selection</h2></div></div><div id="selection-info" class="selection-info"><strong>Nothing selected</strong><p>Choose an element in the canvas to edit it.</p></div><div class="inspector-divider"></div><div class="info-card"><span class="eyebrow">Source</span><strong>${escapeHTML(record.path)}</strong><small>${escapeHTML(record.theme || "default")} theme · HTML-first</small></div></aside>
      </main>
      <nav class="mobile-nav" aria-label="Editor tools"><button data-action="blocks"><span>＋</span><small>Blocks</small></button><button data-action="media"><span>▧</span><small>Media</small></button><button data-action="style"><span>◌</span><small>Style</small></button><button data-action="preview"><span>◉</span><small>Preview</small></button><button data-action="save"><span>↑</span><small>Save</small></button></nav>
      <div id="sheet-backdrop" class="sheet-backdrop" hidden data-action="close-sheet"></div><section id="editor-sheet" class="editor-sheet" hidden aria-label="Editor panel"><div id="sheet-grabber"></div><div id="sheet-content"></div></section>
    </div>`;
}

function updateSelection(selection) {
  state.selected = selection;
  const info = document.querySelector("#selection-info");
  if (!info) return;
  if (!selection) {
    info.innerHTML = "<strong>Nothing selected</strong><p>Choose an element in the canvas to edit it.</p>";
    return;
  }
  const label = selection.element?.tagName?.toLowerCase() || "element";
  info.innerHTML = `<span class="eyebrow">Selected element</span><strong>&lt;${escapeHTML(label)}&gt;</strong><p>${escapeHTML(selection.textContent || "Empty element")}</p>`;
}

async function saveDocument() {
  if (!state.editor || !state.dirty || state.saving) return;
  state.saving = true;
  setStatus("Saving…", "busy");
  setDirty(true);
  try {
    const html = await state.editor.flush();
    const payload = await fetchJSON("/_cms/api/editor-save", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ path: state.record.path, html, base_sha256: state.record.source_sha256, engine: "deckflow" }),
    });
    state.record.source_sha256 = payload.source_sha256;
    state.currentHTML = html;
    setDirty(false);
    setStatus("Saved", "success");
  } catch (error) {
    if (error.status === 409) {
      setStatus("Conflict — your changes are still here", "error");
    } else {
      setStatus(error.message, "error");
    }
  } finally {
    state.saving = false;
    setDirty(state.dirty);
  }
}

async function refreshDocument() {
  if (state.dirty && !window.confirm("Discard your unsaved changes and reload this page?")) return;
  const fresh = await fetchJSON(`/_cms/api/editor?path=${encodeURIComponent(state.path)}&engine=deckflow`);
  state.record = fresh;
  state.currentHTML = editorDocument(fresh);
  await state.editor.setHtml(state.currentHTML);
  setDirty(false);
  setStatus("Reloaded", "success");
}

async function handleAction(action) {
  switch (action) {
    case "back":
      if (!state.dirty || window.confirm("Leave without saving your changes?")) window.location.href = "/_cms/";
      break;
    case "save":
      await saveDocument();
      break;
    case "preview":
      window.open(state.record.preview_url, "_blank", "noopener,noreferrer");
      break;
    case "blocks":
      openSheet("blocks");
      break;
    case "media":
      await openMediaSheet();
      break;
    case "style":
      openSheet("style");
      break;
    case "more":
      openSheet("style");
      break;
    case "close-sheet":
      closeSheet();
      break;
    case "cms":
      window.location.href = "/_cms/";
      break;
    default:
      break;
  }
}

app.addEventListener("click", async (event) => {
  const media = event.target.closest("[data-media-url]");
  if (media) {
    await insertImage(media.dataset.mediaUrl, media.dataset.mediaName || "image");
    return;
  }
  const block = event.target.closest("[data-block]");
  if (block) {
    if (block.dataset.block === "media") await openMediaSheet();
    else await insertBlock(block.dataset.block);
    return;
  }
  const action = event.target.closest("[data-action]")?.dataset.action;
  if (action) await handleAction(action);
});

document.addEventListener("keydown", (event) => {
  if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "s") {
    event.preventDefault();
    saveDocument();
  }
});

window.addEventListener("beforeunload", (event) => {
  if (state.dirty) {
    event.preventDefault();
    event.returnValue = "";
  }
});
window.visualViewport?.addEventListener("resize", setViewportHeight);
window.addEventListener("resize", setViewportHeight);
setViewportHeight();

async function start() {
  if (!path) throw new Error("No content path was provided");
  state.record = await fetchJSON(`/_cms/api/editor?path=${encodeURIComponent(path)}&engine=deckflow`);
  if (!state.record.editor?.engines?.some((engine) => engine.name === "deckflow" && engine.available)) {
    throw new Error("The Deckflow editor bundle is not installed yet");
  }
  renderShell(state.record);
  const canvas = document.querySelector("#deckflow-canvas");
  state.currentHTML = editorDocument(state.record);
  state.editor = mountHtmlEditor({
    container: canvas,
    html: state.currentHTML,
    baseUrl: `${window.location.origin}/`,
    allowScripts: false,
    fit: "none",
    showScaleToggle: false,
    title: `${state.record.document?.title || state.record.path} editor canvas`,
    onChange: ({ html }) => {
      state.currentHTML = html;
      setDirty(true);
    },
    onSelectionChange: updateSelection,
    onError: (error) => setStatus(error.message, "error"),
  });
  await state.editor.ready;
  setStatus("Ready", "success");
}

start().catch((error) => {
  app.innerHTML = `<main class="fatal-error"><span class="eyebrow">Fileloom editor</span><h1>Editor could not load</h1><p>${escapeHTML(error.message)}</p><button class="primary-button" data-action="back">Back to CMS</button></main>`;
});
