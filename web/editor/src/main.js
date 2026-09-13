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
  baseHTML: "",
  baseDocument: null,
  metadata: null,
  dirty: false,
  saving: false,
  changeVersion: 0,
  hostUndo: [],
  hostRedo: [],
  conflict: null,
  selected: null,
  sheet: null,
};

const metadataFields = ["title", "date", "tags", "category", "excerpt", "status", "publish_at"];

function metadataFromDocument(document) {
  return {
    title: String(document?.title || ""),
    date: String(document?.date || ""),
    tags: [...(document?.tags || [])],
    category: String(document?.category || ""),
    excerpt: String(document?.excerpt || ""),
    status: String(document?.status || "published"),
    publish_at: String(document?.publish_at || ""),
  };
}

function cloneMetadata(metadata) {
  return metadataFromDocument(metadata || {});
}

function metadataValue(metadata, field) {
  const value = metadata?.[field];
  return Array.isArray(value) ? value.join("\u001f") : String(value ?? "");
}

function metadataPayload() {
  const metadata = cloneMetadata(state.metadata || state.record?.document || {});
  metadata.tags = metadata.tags.map((tag) => String(tag).trim()).filter(Boolean);
  return metadata;
}

function bodyHTMLFromEditorDocument(html) {
  try {
    const parsed = new DOMParser().parseFromString(String(html || ""), "text/html");
    return parsed.body?.innerHTML || "";
  } catch {
    return String(html || "");
  }
}

function normalizedBodyHTML(html) {
  return bodyHTMLFromEditorDocument(html).replace(/\s+/g, " ").trim();
}

function textSummary(html, fallback = "(empty)") {
  try {
    const parsed = new DOMParser().parseFromString(String(html || ""), "text/html");
    const text = (parsed.body?.textContent || "").replace(/\s+/g, " ").trim();
    return text ? `${text.slice(0, 180)}${text.length > 180 ? "…" : ""}` : fallback;
  } catch {
    return fallback;
  }
}

function localDateTimeValue(value) {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const pad = (number) => String(number).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function updateDocumentHeading() {
  const heading = document.querySelector(".document-heading strong");
  if (!heading) return;
  const title = state.record?.document?.title || state.record?.path || "";
  heading.textContent = title;
  heading.title = title;
  const summary = document.querySelector("[data-document-summary]");
  if (summary) summary.textContent = `${state.record?.theme || "default"} theme · ${state.record?.document?.status || "published"} · HTML-first`;
}

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
  if (state.dirty && !state.saving) setStatus("Unsaved changes", "dirty");
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
  return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/_cms/assets/fileloom-code.css"><style>${css}</style><style>html{background:#fff}body{min-height:100vh;margin:0}</style></head><body>${record.html || ""}</body></html>`;
}

function insertIntoBody(source, fragment) {
  const lower = source.toLowerCase();
  const end = lower.lastIndexOf("</body>");
  if (end >= 0) return `${source.slice(0, end)}\n${fragment}\n${source.slice(end)}`;
  return `${source}\n${fragment}\n`;
}

function cleanSelectedHTML(element) {
  if (!element) return "";
  const clone = element.cloneNode(true);
  clone.querySelectorAll?.("[data-local-editor-selected], [data-local-editor-drag-ready]").forEach((node) => {
    node.removeAttribute("data-local-editor-selected");
    node.removeAttribute("data-local-editor-drag-ready");
  });
  clone.removeAttribute?.("data-local-editor-selected");
  clone.removeAttribute?.("data-local-editor-drag-ready");
  return clone.outerHTML || "";
}

function normalizedSelectionText(value) {
  return String(value || "").replace(/\u00a0/g, " ").replace(/\s+/g, " ").trim();
}

function selectionMatchesFingerprint(element, target) {
  return typeof target?.originalText !== "string"
    || normalizedSelectionText(element?.textContent) === normalizedSelectionText(target.originalText);
}

function sourceSelectionInfo(source, selection, closestSelector = "") {
  if (!selection?.target) return null;
  const parsed = new DOMParser().parseFromString(String(source || ""), "text/html");
  const target = selection.target;
  let resolved = null;
  let candidates = [];
  if (target.selector) {
    try {
      candidates = [...parsed.querySelectorAll(target.selector)];
      const candidate = candidates[target.selectorIndex ?? 0] || null;
      if (candidate && selectionMatchesFingerprint(candidate, target)) resolved = candidate;
    } catch {
      // Fall through to the stable path and fingerprint fallbacks.
    }
  }
  if (!resolved && Array.isArray(target.domPath)) {
    resolved = parsed.body;
    for (const index of target.domPath) resolved = resolved?.children?.[index];
    if (!selectionMatchesFingerprint(resolved, target)) resolved = null;
  }
  if (!resolved && target.id) {
    const candidate = parsed.getElementById(target.id);
    if (candidate && selectionMatchesFingerprint(candidate, target)) resolved = candidate;
  }
  if (!resolved && /^[a-z][a-z0-9-]*$/i.test(target.tagName || "") && typeof target.originalText === "string") {
    const matches = [...parsed.querySelectorAll(target.tagName)].filter((candidate) => selectionMatchesFingerprint(candidate, target));
    if (matches.length === 1) resolved = matches[0];
  }
  const element = closestSelector ? resolved?.closest?.(closestSelector) : resolved;
  if (!element || ["BODY", "HTML"].includes(element.tagName)) return null;
  const selectedHTML = cleanSelectedHTML(element);
  if (!selectedHTML) return null;
  if (closestSelector) candidates = [...parsed.querySelectorAll(closestSelector)];
  const selectedIndex = candidates.indexOf(element);
  const occurrence = selectedIndex < 0
    ? Math.max(0, Number(target.selectorIndex || 0))
    : candidates.slice(0, selectedIndex).filter((candidate) => cleanSelectedHTML(candidate) === selectedHTML).length;
  return { parsed, target, element, selectedHTML, occurrence, selectorIndex: selectedIndex };
}

function sourceOpeningTagRange(source, info) {
  if (!info?.element) return null;
  const tagName = String(info.element.tagName || "").toLowerCase();
  if (!tagName) return null;
  const exactStart = Number.isInteger(info.start) ? info.start : -1;
  if (exactStart >= 0 && new RegExp(`^<${tagName}(?:\\s|>)`, "i").test(source.slice(exactStart))) {
    const end = scanOpeningTagEnd(source, exactStart);
    if (end >= 0) return { start: exactStart, end };
  }
  const ranges = rawOpeningTagRanges(source, tagName);
  const index = Number.isInteger(info.selectorIndex) && info.selectorIndex >= 0 ? info.selectorIndex : 0;
  return ranges[index] || null;
}

function scanOpeningTagEnd(source, start) {
  let quote = "";
  for (let index = start + 1; index < source.length; index += 1) {
    const character = source[index];
    if (quote) {
      if (character === quote) quote = "";
      continue;
    }
    if (character === '"' || character === "'") {
      quote = character;
      continue;
    }
    if (character === ">") return index + 1;
  }
  return -1;
}

function rawOpeningTagRanges(source, tagName) {
  const ranges = [];
  const tagPattern = new RegExp(`<${tagName}(?=\\s|/?>)`, "ig");
  let match;
  while ((match = tagPattern.exec(source))) {
    const start = match.index;
    if (source.slice(Math.max(0, start - 4), start) === "<!--") continue;
    const end = scanOpeningTagEnd(source, start);
    if (end >= 0) ranges.push({ start, end });
    tagPattern.lastIndex = Math.max(tagPattern.lastIndex, end);
  }
  return ranges;
}

function parseOpeningTagAttributes(tag) {
  const attributes = [];
  let index = 1;
  while (index < tag.length && /\s/.test(tag[index])) index += 1;
  while (index < tag.length && !/[\s/>]/.test(tag[index])) index += 1;
  while (index < tag.length) {
    while (index < tag.length && /\s/.test(tag[index])) index += 1;
    if (index >= tag.length || tag[index] === ">" || tag[index] === "/") break;
    const start = index;
    while (index < tag.length && !/[\s=/>]/.test(tag[index])) index += 1;
    const name = tag.slice(start, index);
    if (!name) break;
    while (index < tag.length && /\s/.test(tag[index])) index += 1;
    let valueStart = index;
    let valueEnd = index;
    let quote = "";
    if (tag[index] === "=") {
      index += 1;
      while (index < tag.length && /\s/.test(tag[index])) index += 1;
      valueStart = index;
      if (tag[index] === '"' || tag[index] === "'") {
        quote = tag[index];
        valueStart = ++index;
        while (index < tag.length && tag[index] !== quote) index += 1;
        valueEnd = index;
        if (index < tag.length) index += 1;
      } else {
        while (index < tag.length && !/[\s>]/.test(tag[index])) index += 1;
        valueEnd = index;
      }
    }
    attributes.push({ name, normalized: name.toLowerCase(), start, valueStart, valueEnd, end: index, quote });
  }
  return attributes;
}

function patchOpeningTagAttributes(tag, values = {}, remove = new Set()) {
  const attributes = parseOpeningTagAttributes(tag);
  const replacements = [];
  for (const [name, value] of Object.entries(values)) {
    const normalized = name.toLowerCase();
    const existing = attributes.find((attribute) => attribute.normalized === normalized);
    if (existing) {
      if (remove.has(normalized)) {
        let start = existing.start;
        while (start > 0 && /\s/.test(tag[start - 1])) start -= 1;
        replacements.push({ start, end: existing.end, value: "" });
      } else {
        const escaped = escapeHTML(value);
        if (existing.quote || !/[\s"'`=<>]/.test(String(value))) {
          replacements.push({ start: existing.valueStart, end: existing.valueEnd, value: escaped });
        } else {
          replacements.push({ start: existing.start, end: existing.end, value: `${existing.name}="${escaped}"` });
        }
      }
      continue;
    }
    if (remove.has(normalized)) continue;
    const closeStart = tag.endsWith("/>") ? tag.length - 2 : tag.length - 1;
    replacements.push({ start: closeStart, end: closeStart, value: ` ${name}="${escapeHTML(value)}"` });
  }
  replacements.sort((left, right) => right.start - left.start || right.end - left.end);
  let result = tag;
  for (const replacement of replacements) result = `${result.slice(0, replacement.start)}${replacement.value}${result.slice(replacement.end)}`;
  return result;
}
function sourceRangeFromHTML(source, selectedHTML, occurrence) {
  let cursor = 0;
  let index = -1;
  for (let count = 0; count <= occurrence; count += 1) {
    index = source.indexOf(selectedHTML, cursor);
    if (index < 0) return null;
    cursor = index + selectedHTML.length;
  }
  return { start: index, end: index + selectedHTML.length, html: selectedHTML };
}

function sourceElementRange(source, parsed, element) {
  if (!parsed || !element) return null;
  const selectedHTML = cleanSelectedHTML(element);
  if (!selectedHTML || ["BODY", "HTML"].includes(element.tagName)) return null;
  const candidates = [...parsed.querySelectorAll("*")].filter((candidate) => cleanSelectedHTML(candidate) === selectedHTML);
  const occurrence = candidates.indexOf(element);
  if (occurrence < 0) return null;
  return sourceRangeFromHTML(source, selectedHTML, occurrence);
}

function sourceSelectionRange(source, selection, closestSelector = "") {
  const info = sourceSelectionInfo(source, selection, closestSelector);
  if (!info) return null;
  const range = sourceRangeFromHTML(source, info.selectedHTML, info.occurrence);
  return range ? { ...info, ...range } : null;
}

function sourceSelectionElement(source, selection) {
  return sourceSelectionInfo(source, selection)?.element || null;
}

function insertAfterSelection(source, fragment, selection) {
  const info = sourceSelectionRange(source, selection);
  if (!info) return insertIntoBody(source, fragment);
  const end = info.end;
  return `${source.slice(0, end)}\n${fragment}\n${source.slice(end)}`;
}

function replaceSelection(source, replacement, selection, closestSelector = "") {
  const info = sourceSelectionRange(source, selection, closestSelector);
  if (!info) return source;
  return `${source.slice(0, info.start)}${replacement}${source.slice(info.end)}`;
}

async function applyEditorHTML(next, { recordHistory = true } = {}) {
  if (!state.editor) return;
  const previous = state.editor.getHtml();
  if (next === previous) return;
  if (recordHistory) {
    state.hostUndo.push(previous);
    if (state.hostUndo.length > 50) state.hostUndo.shift();
    state.hostRedo = [];
  }
  state.changeVersion += 1;
  state.selected = null;
  updateSelection(null);
  await state.editor.setHtml(next);
  state.currentHTML = state.editor.getHtml();
  setDirty(true);
}

async function undoEditorChange() {
  if (state.hostUndo.length) {
    const current = state.editor.getHtml();
    const previous = state.hostUndo.pop();
    state.hostRedo.push(current);
    await applyEditorHTML(previous, { recordHistory: false });
    setStatus("Undid insertion", "dirty");
    return;
  }
  if (state.editor?.canUndo) {
    await state.editor.undo();
    state.changeVersion += 1;
    setDirty(true);
  }
}

async function redoEditorChange() {
  if (state.hostRedo.length) {
    const current = state.editor.getHtml();
    const next = state.hostRedo.pop();
    state.hostUndo.push(current);
    await applyEditorHTML(next, { recordHistory: false });
    setStatus("Redid insertion", "dirty");
    return;
  }
  if (state.editor?.canRedo) {
    await state.editor.redo();
    state.changeVersion += 1;
    setDirty(true);
  }
}

const codeLanguages = [
  ["plaintext", "Plain text"],
  ["javascript", "JavaScript"],
  ["typescript", "TypeScript"],
  ["html", "HTML"],
  ["css", "CSS"],
  ["json", "JSON"],
  ["go", "Go"],
  ["python", "Python"],
  ["bash", "Bash"],
  ["sql", "SQL"],
];

function languageClass(value) {
  return `language-${String(value || "plaintext").toLowerCase().replace(/[^a-z0-9_-]/g, "-")}`;
}

function codeBlockFragment(language = "plaintext", code = "Paste code here") {
  const normalized = String(language || "plaintext").toLowerCase();
  const className = languageClass(normalized);
  return `<pre class="fileloom-code-block" data-fileloom-code data-language="${escapeHTML(normalized)}"><code class="${className}" data-language="${escapeHTML(normalized)}">${escapeHTML(code)}</code></pre>`;
}

function selectedCodeBlock() {
  const element = state.selected?.element;
  return element?.closest?.("pre.fileloom-code-block, pre[data-fileloom-code]") || null;
}

function selectedImageBlock() {
  const element = state.selected?.element;
  const candidate = element?.closest?.("figure.fileloom-image, img");
  if (!candidate) return null;
  if (candidate.tagName === "FIGURE" && !candidate.querySelector("img")) return null;
  return candidate;
}

function selectedStructuralElement() {
  return selectedCodeBlock() || selectedImageBlock() || state.selected?.element || null;
}

function structuralSelectionInfo(source) {
  const selector = selectedCodeBlock()
    ? "pre.fileloom-code-block, pre[data-fileloom-code]"
    : selectedImageBlock()
      ? "figure.fileloom-image, img"
      : "";
  return sourceSelectionRange(source, state.selected, selector);
}

function elementInspectorTarget() {
  const element = state.selected?.element;
  if (!element) return null;
  const link = element.closest?.("a");
  if (link) return link;
  const image = element.closest?.("img");
  if (image) return image;
  return element;
}

function elementInspectorSelector(element) {
  if (!element) return "";
  if (element.tagName === "A") return "a";
  if (element.tagName === "IMG") return "img";
  return "";
}

function elementInspectorInfo(source) {
  const element = elementInspectorTarget();
  if (!element) return null;
  const selector = elementInspectorSelector(element);
  return sourceSelectionInfo(source, state.selected, selector);
}

function inspectorURLIsSafe(value) {
  const normalized = String(value || "").trim();
  if (!normalized || /[\u0000-\u001f\u007f]/.test(normalized)) return false;
  if (/^(?:javascript|vbscript|data):/i.test(normalized)) return false;
  if (normalized.startsWith("//")) return false;
  if (/^(?:https?:|mailto:|tel:|\/|#|\?|\.{0,2}\/)/i.test(normalized)) return true;
  return !/^[a-z][a-z0-9+.-]*:/i.test(normalized);
}

function inspectorClassIsSafe(value) {
  return String(value || "").trim().split(/\s+/).filter(Boolean).every((token) => /^[A-Za-z_][A-Za-z0-9_-]*$/.test(token));
}

function inspectorIDIsSafe(value) {
  return !value || /^[A-Za-z][A-Za-z0-9_.:-]*$/.test(value);
}

function inspectorTextValue(element, name) {
  return element?.getAttribute?.(name) || "";
}

function elementInspectorHTML() {
  const element = elementInspectorTarget();
  if (!element || ["BODY", "HTML"].includes(element.tagName)) return "";
  const tag = element.tagName.toLowerCase();
  const isLink = tag === "a";
  const isImage = tag === "img";
  const linkFields = isLink ? `<div class="details-grid"><label class="form-field"><span>Link URL</span><input name="href" value="${escapeHTML(inspectorTextValue(element, "href"))}" required></label><label class="form-field"><span>Target</span><select name="target"><option value="">Same tab</option><option value="_blank" ${element.getAttribute("target") === "_blank" ? "selected" : ""}>New tab</option><option value="_self" ${element.getAttribute("target") === "_self" ? "selected" : ""}>This frame</option></select></label></div><div class="details-grid"><label class="form-field"><span>Rel</span><input name="rel" value="${escapeHTML(inspectorTextValue(element, "rel"))}" placeholder="nofollow"></label><label class="form-field"><span>Title</span><input name="title" value="${escapeHTML(inspectorTextValue(element, "title"))}"></label></div>` : "";
  const imageFields = isImage ? `<div class="details-grid"><label class="form-field"><span>Image source</span><input name="src" value="${escapeHTML(inspectorTextValue(element, "src"))}" required></label><label class="form-field"><span>Alt text</span><input name="alt" value="${escapeHTML(inspectorTextValue(element, "alt"))}"></label></div><div class="details-grid"><label class="form-field"><span>Loading</span><select name="loading"><option value="">Browser default</option><option value="lazy" ${element.getAttribute("loading") === "lazy" ? "selected" : ""}>Lazy</option><option value="eager" ${element.getAttribute("loading") === "eager" ? "selected" : ""}>Eager</option></select></label><label class="form-field"><span>Decoding</span><select name="decoding"><option value="">Browser default</option><option value="async" ${element.getAttribute("decoding") === "async" ? "selected" : ""}>Async</option><option value="sync" ${element.getAttribute("decoding") === "sync" ? "selected" : ""}>Sync</option></select></label></div><label class="form-field"><span>Image title</span><input name="title" value="${escapeHTML(inspectorTextValue(element, "title"))}"></label>` : "";
  return `<div class="sheet-heading"><div><span class="eyebrow">Element inspector</span><h2>&lt;${escapeHTML(tag)}&gt; properties</h2></div><button class="icon-button" data-action="close-sheet" aria-label="Close">×</button></div><form id="element-inspector-form" class="details-form" data-inspector-tag="${escapeHTML(tag)}"><div class="details-grid"><label class="form-field"><span>Element</span><input value="${escapeHTML(tag)}" readonly></label><label class="form-field"><span>ID</span><input name="id" value="${escapeHTML(element.id || "")}" placeholder="optional"></label></div><label class="form-field"><span>Classes</span><input name="class" value="${escapeHTML(element.className || "")}" placeholder="optional"></label>${linkFields}${imageFields}<p class="code-help">Apply changes to the selected source element without reserializing the rest of the document.</p><div class="details-actions"><button type="button" class="secondary-button" data-action="close-sheet">Cancel</button><button type="submit" class="primary-button">Apply properties</button></div></form>`;
}

function inspectorAttributeChanges(form) {
  const tag = form.dataset.inspectorTag;
  const values = { id: form.elements.id.value.trim(), class: form.elements.class.value.trim() };
  const remove = new Set();
  if (!values.id) remove.add("id");
  if (!values.class) remove.add("class");
  if (tag === "a") {
    values.href = form.elements.href.value.trim();
    values.target = form.elements.target.value;
    values.rel = form.elements.rel.value.trim();
    values.title = form.elements.title.value.trim();
    if (!values.target) remove.add("target");
    if (!values.rel) remove.add("rel");
    if (!values.title) remove.add("title");
  }
  if (tag === "img") {
    values.src = form.elements.src.value.trim();
    values.alt = form.elements.alt.value;
    values.loading = form.elements.loading.value;
    values.decoding = form.elements.decoding.value;
    values.title = form.elements.title.value.trim();
    if (!values.loading) remove.add("loading");
    if (!values.decoding) remove.add("decoding");
    if (!values.title) remove.add("title");
  }
  return { values, remove };
}

async function submitElementInspector(form) {
  const { values, remove } = inspectorAttributeChanges(form);
  if (!inspectorIDIsSafe(values.id)) {
    setStatus("ID contains unsupported characters", "error");
    return;
  }
  if (!inspectorClassIsSafe(values.class)) {
    setStatus("Classes must be space-separated CSS names", "error");
    return;
  }
  if (form.dataset.inspectorTag === "a" && !inspectorURLIsSafe(values.href)) {
    setStatus("Link URL is unsafe or invalid", "error");
    return;
  }
  if (form.dataset.inspectorTag === "img" && !inspectorURLIsSafe(values.src)) {
    setStatus("Image source is unsafe or invalid", "error");
    return;
  }
  const source = state.editor?.getHtml() || "";
  const info = elementInspectorInfo(source);
  const range = sourceOpeningTagRange(source, info);
  if (!info || !range) {
    setStatus("Could not resolve the selected element", "error");
    return;
  }
  const openingTag = source.slice(range.start, range.end);
  const nextOpeningTag = patchOpeningTagAttributes(openingTag, values, remove);
  if (nextOpeningTag === openingTag) {
    closeSheet();
    setStatus("No property changes", "neutral");
    return;
  }
  await applyEditorHTML(`${source.slice(0, range.start)}${nextOpeningTag}${source.slice(range.end)}`);
  closeSheet();
  setStatus("Element properties updated", "dirty");
}

function selectionOperationsHTML() {
  const element = selectedStructuralElement();
  if (!element || ["BODY", "HTML"].includes(element.tagName)) return "";
  const previous = element.previousElementSibling;
  const next = element.nextElementSibling;
  const imageAction = selectedImageBlock() ? '<button class="secondary-button" data-action="media">Replace image</button>' : "";
  return `<div class="selection-actions"><span class="eyebrow">Element actions</span><div class="selection-action-grid"><button class="secondary-button" data-action="properties">Edit properties</button>${imageAction}<button class="secondary-button" data-action="duplicate">Duplicate</button><button class="secondary-button" data-action="move-up" ${previous ? "" : "disabled"}>Move up</button><button class="secondary-button" data-action="move-down" ${next ? "" : "disabled"}>Move down</button><button class="secondary-button danger-button" data-action="delete-selection">Delete</button></div></div>`;
}

function duplicateWithoutIDs(element) {
  const clone = element.cloneNode(true);
  clone.removeAttribute?.("id");
  clone.querySelectorAll?.("[id]").forEach((node) => node.removeAttribute("id"));
  return cleanSelectedHTML(clone);
}

async function duplicateSelection() {
  const source = state.editor?.getHtml() || "";
  const info = structuralSelectionInfo(source);
  if (!info) return;
  const duplicate = duplicateWithoutIDs(info.element);
  if (!duplicate) return;
  const next = `${source.slice(0, info.end)}\n${duplicate}\n${source.slice(info.end)}`;
  await applyEditorHTML(next);
  closeSheet();
  setStatus("Element duplicated", "dirty");
}

async function deleteSelection() {
  const source = state.editor?.getHtml() || "";
  const info = structuralSelectionInfo(source);
  if (!info) return;
  await applyEditorHTML(`${source.slice(0, info.start)}${source.slice(info.end)}`);
  closeSheet();
  setStatus("Element deleted", "dirty");
}

async function moveSelection(direction) {
  const source = state.editor?.getHtml() || "";
  const info = structuralSelectionInfo(source);
  const sibling = direction < 0 ? info?.element?.previousElementSibling : info?.element?.nextElementSibling;
  if (!info || !sibling) {
    setStatus(direction < 0 ? "Already at the top" : "Already at the bottom", "neutral");
    return;
  }
  const siblingRange = sourceElementRange(source, info.parsed, sibling);
  if (!siblingRange) {
    setStatus("Could not resolve the neighboring element", "error");
    return;
  }
  let next;
  if (direction < 0 && siblingRange.start < info.start) {
    next = `${source.slice(0, siblingRange.start)}${info.html}${source.slice(siblingRange.end, info.start)}${siblingRange.html}${source.slice(info.end)}`;
  } else if (direction > 0 && info.start < siblingRange.start) {
    next = `${source.slice(0, info.start)}${siblingRange.html}${source.slice(info.end, siblingRange.start)}${info.html}${source.slice(siblingRange.end)}`;
  } else {
    setStatus("Could not move the selected element", "error");
    return;
  }
  await applyEditorHTML(next);
  closeSheet();
  setStatus(direction < 0 ? "Element moved up" : "Element moved down", "dirty");
}
function codeBlockValues() {
  const block = selectedCodeBlock();
  const code = block?.querySelector("code");
  const classLanguage = code?.className?.match(/language-([\w-]+)/)?.[1];
  return {
    block,
    language: block?.dataset.language || code?.dataset.language || classLanguage || "plaintext",
    code: code?.textContent || "",
  };
}

function codeSheetHTML(mode = "insert") {
  const values = mode === "update" ? codeBlockValues() : { language: "plaintext", code: "" };
  const options = codeLanguages.map(([value, label]) => `<option value="${value}" ${value === values.language ? "selected" : ""}>${label}</option>`).join("");
  return `<div class="sheet-heading"><div><span class="eyebrow">${mode === "update" ? "Edit element" : "Insert"}</span><h2>Code block</h2></div><button class="icon-button" data-action="close-sheet" aria-label="Close">×</button></div>
    <form id="code-form" class="code-form" data-code-mode="${mode}">
      <label class="form-field"><span>Language</span><select name="language">${options}</select></label>
      <label class="form-field"><span>Code</span><textarea name="code" rows="13" spellcheck="false" autocapitalize="off" autocomplete="off">${escapeHTML(values.code)}</textarea></label>
      <p class="code-help">Code stays plain in the HTML source. Fileloom applies syntax highlighting in the public/theme preview.</p>
      <div class="details-actions"><button type="button" class="secondary-button" data-action="close-sheet">Cancel</button><button type="submit" class="primary-button">${mode === "update" ? "Update code" : "Insert code"}</button></div>
    </form>`;
}

async function openCodeSheet(mode = "insert") {
  openSheet("code");
  document.querySelector("#sheet-content").innerHTML = codeSheetHTML(mode);
}

async function submitCodeForm(form) {
  const language = form.elements.language.value || "plaintext";
  const code = form.elements.code.value;
  const mode = form.dataset.codeMode;
  if (mode === "update") {
    const next = replaceSelection(state.editor.getHtml(), codeBlockFragment(language, code), state.selected, "pre.fileloom-code-block, pre[data-fileloom-code]");
    if (next === state.editor.getHtml()) {
      setStatus("Could not resolve the selected code block", "error");
      return;
    }
    await applyEditorHTML(next);
    closeSheet();
    setStatus("Code block updated", "dirty");
    return;
  }
  const hadSelection = Boolean(state.selected);
  const next = insertAfterSelection(state.editor.getHtml(), codeBlockFragment(language, code), state.selected);
  await applyEditorHTML(next);
  closeSheet();
  setStatus(hadSelection ? "Code block added after selection" : "Code block added", "dirty");
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
      return codeBlockFragment("javascript", "// Write code here");
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
  const hadSelection = Boolean(state.selected);
  const next = insertAfterSelection(state.editor.getHtml(), blockFragment(type), state.selected);
  await applyEditorHTML(next);
  closeSheet();
  setStatus(hadSelection ? "Block added after selection" : "Block added", "dirty");
}

function flattenMedia(node, result = []) {
  if (!node) return result;
  if (node.type === "file") result.push(node);
  for (const child of node.items || []) flattenMedia(child, result);
  return result;
}

function mediaURL(file) {
  if (file.url) return String(file.url);
  const value = String(file.path || "");
  return `/media${value.startsWith("/") ? value : `/${value.split("/").map(encodeURIComponent).join("/")}`}`;
}

async function uploadMediaFiles(files) {
  const selected = [...(files || [])];
  if (!selected.length) return;
  setStatus(`Uploading ${selected.length} file${selected.length === 1 ? "" : "s"}…`, "busy");
  for (const file of selected) {
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
  const replacingImage = Boolean(selectedImageBlock());
  openSheet("media");
  const content = document.querySelector("#sheet-content");
  content.innerHTML = '<div class="sheet-loading">Loading media…</div>';
  try {
    const tree = await fetchJSON("/_cms/api/media");
    const files = flattenMedia(tree).filter((file) => /\.(avif|gif|jpe?g|png|svg|webp)$/i.test(file.name || ""));
    content.innerHTML = `
      <div class="sheet-heading"><div><span class="eyebrow">Media library</span><h2>${replacingImage ? "Replace selected image" : "Choose an image"}</h2></div><label class="upload-button">Upload<input id="media-upload" type="file" accept="image/*" multiple></label></div>
      <div id="media-dropzone" class="media-dropzone" tabindex="0"><strong>Drop images here</strong><span>or tap to choose files</span></div>
      <div class="media-grid">${files.length ? files.map((file) => `<button class="media-card" data-media-url="${escapeHTML(mediaURL(file))}" data-media-name="${escapeHTML(file.name)}"><img src="${escapeHTML(mediaURL(file))}" alt=""><span>${escapeHTML(file.name)}</span></button>`).join("") : '<p class="empty-state">No images yet.</p>'}</div>`;
    const input = content.querySelector("#media-upload");
    const zone = content.querySelector("#media-dropzone");
    const chooseFiles = async (filesToUpload) => {
      try { await uploadMediaFiles(filesToUpload); } catch (error) { setStatus(error.message, "error"); }
    };
    input?.addEventListener("change", async (event) => chooseFiles(event.target.files));
    zone?.addEventListener("click", () => input?.click());
    zone?.addEventListener("keydown", (event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); input?.click(); } });
    ["dragenter", "dragover"].forEach((type) => zone?.addEventListener(type, (event) => { event.preventDefault(); zone.classList.add("is-dragging"); }));
    ["dragleave", "drop"].forEach((type) => zone?.addEventListener(type, (event) => { event.preventDefault(); zone.classList.remove("is-dragging"); }));
    zone?.addEventListener("drop", async (event) => chooseFiles(event.dataTransfer?.files));
  } catch (error) {
    content.innerHTML = `<p class="error-card">${escapeHTML(error.message)}</p>`;
  }
}

async function insertImage(url, name) {
  const replacingImage = Boolean(selectedImageBlock());
  const hadSelection = Boolean(state.selected);
  const fragment = `<figure class="fileloom-image"><img src="${escapeHTML(url)}" alt="${escapeHTML(name)}"><figcaption>${escapeHTML(name)}</figcaption></figure>`;
  const source = state.editor.getHtml();
  const next = replacingImage
    ? replaceSelection(source, fragment, state.selected, "figure.fileloom-image, img")
    : insertAfterSelection(source, fragment, state.selected);
  if (next === source && replacingImage) {
    setStatus("Could not resolve the selected image", "error");
    return;
  }
  await applyEditorHTML(next);
  closeSheet();
  setStatus(replacingImage ? "Image replaced" : hadSelection ? "Image added after selection" : "Image added", "dirty");
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
  } else if (kind === "details") {
    document.querySelector("#sheet-content").innerHTML = detailsSheetHTML();
    syncPublishAtField();
  } else if (kind === "properties") {
    document.querySelector("#sheet-content").innerHTML = elementInspectorHTML();
  } else if (kind === "history") {
    document.querySelector("#sheet-content").innerHTML = '<div class="sheet-heading"><div><span class="eyebrow">Source history</span><h2>Revision history</h2></div><button class="icon-button" data-action="close-sheet" aria-label="Close">×</button></div><div class="sheet-loading">Loading history…</div>';
  } else if (kind === "conflict") {
    document.querySelector("#sheet-content").innerHTML = conflictSheetHTML();
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

function detailsSheetHTML() {
  const metadata = metadataPayload();
  const scheduled = metadata.status === "scheduled";
  const codeAction = selectedCodeBlock() ? '<button type="button" class="secondary-button" data-action="edit-code">Edit code</button>' : "";
  return `<div class="sheet-heading"><div><span class="eyebrow">Page details</span><h2>Metadata & status</h2></div><button class="icon-button" data-action="close-sheet" aria-label="Close">×</button></div>${selectionOperationsHTML()}
    <form id="details-form" class="details-form">
      <label class="form-field"><span>Title</span><input data-metadata="title" value="${escapeHTML(metadata.title)}" required></label>
      <div class="details-grid"><label class="form-field"><span>Date</span><input data-metadata="date" type="date" value="${escapeHTML(metadata.date)}"></label><label class="form-field"><span>Status</span><select data-metadata="status"><option value="draft" ${metadata.status === "draft" ? "selected" : ""}>Draft</option><option value="private" ${metadata.status === "private" ? "selected" : ""}>Private</option><option value="published" ${metadata.status === "published" ? "selected" : ""}>Published</option><option value="scheduled" ${metadata.status === "scheduled" ? "selected" : ""}>Scheduled</option></select></label></div>
      <label class="form-field" data-publish-at-field ${scheduled ? "" : "hidden"}><span>Publish at</span><input data-metadata="publish_at" type="datetime-local" value="${escapeHTML(localDateTimeValue(metadata.publish_at))}"><small>Stored in UTC after saving.</small></label>
      <div class="details-grid"><label class="form-field"><span>Tags</span><input data-metadata="tags" value="${escapeHTML(metadata.tags.join(", "))}" placeholder="design, notes"></label><label class="form-field"><span>Category</span><input data-metadata="category" value="${escapeHTML(metadata.category)}"></label></div>
      <label class="form-field"><span>Excerpt</span><textarea data-metadata="excerpt" rows="3">${escapeHTML(metadata.excerpt)}</textarea></label>
      <div class="details-actions">${codeAction}<button type="button" class="secondary-button" data-action="history">History</button><button type="button" class="primary-button" data-action="save">Save details</button></div>
    </form>
    <div class="info-card details-source"><span class="eyebrow">Source</span><strong>${escapeHTML(state.record?.path || "")}</strong><small>${escapeHTML(state.record?.theme || "default")} theme · slug ${escapeHTML(state.record?.document?.slug || "")}</small></div>`;
}

function syncPublishAtField() {
  const field = document.querySelector("[data-publish-at-field]");
  const status = document.querySelector('[data-metadata="status"]');
  const input = document.querySelector('[data-metadata="publish_at"]');
  if (field) field.hidden = status?.value !== "scheduled";
  if (input) input.required = status?.value === "scheduled";
}

function parseTagsInput(value) {
  const seen = new Set();
  return String(value || "").split(",").map((tag) => tag.trim()).filter((tag) => {
    const key = tag.toLowerCase();
    if (!tag || seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

function updateMetadataDraft(field) {
  if (!state.metadata) state.metadata = metadataFromDocument(state.record?.document || {});
  const name = field.dataset.metadata;
  if (!metadataFields.includes(name)) return;
  state.metadata[name] = name === "tags" ? parseTagsInput(field.value) : field.value;
  state.changeVersion += 1;
  if (name === "status") syncPublishAtField();
  setDirty(true);
}

function metadataMerge(remoteDocument) {
  const base = state.baseDocument || metadataFromDocument(state.record?.document || {});
  const local = state.metadata || base;
  const remote = metadataFromDocument(remoteDocument);
  const merged = {};
  const conflicts = [];
  for (const field of metadataFields) {
    const localValue = metadataValue(local, field);
    const baseValue = metadataValue(base, field);
    const remoteValue = metadataValue(remote, field);
    if (localValue === baseValue) merged[field] = Array.isArray(remote[field]) ? [...remote[field]] : remote[field];
    else if (remoteValue === baseValue || localValue === remoteValue) merged[field] = Array.isArray(local[field]) ? [...local[field]] : local[field];
    else {
      merged[field] = Array.isArray(local[field]) ? [...local[field]] : local[field];
      conflicts.push(field);
    }
  }
  return { merged, conflicts };
}

function conflictSheetHTML() {
  const conflict = state.conflict || {};
  const remote = conflict.remote;
  if (!remote) {
    return `<div class="sheet-heading"><div><span class="eyebrow">Save conflict</span><h2>Someone changed this page</h2></div><button class="icon-button" data-action="close-sheet" aria-label="Close">×</button></div><div class="info-card conflict-card"><p>Your local edits are safe in this canvas. Loading the current source…</p></div>`;
  }
  const merge = conflict.merge || metadataMerge(remote.document);
  const baseBody = state.baseHTML || state.record?.html || "";
  const localBody = bodyHTMLFromEditorDocument(state.editor?.getHtml() || state.currentHTML);
  const localChanged = normalizedBodyHTML(localBody) !== normalizedBodyHTML(baseBody);
  const remoteChanged = normalizedBodyHTML(remote.html) !== normalizedBodyHTML(baseBody);
  const bodyConflict = localChanged && remoteChanged && normalizedBodyHTML(localBody) !== normalizedBodyHTML(remote.html);
  const fields = merge.conflicts.length ? `<p class="conflict-fields"><strong>Metadata conflicts:</strong> ${escapeHTML(merge.conflicts.join(", "))}</p>` : `<p class="conflict-fields">Metadata changes can be merged safely.</p>`;
  const mergeDisabled = bodyConflict || merge.conflicts.length > 0;
  return `<div class="sheet-heading"><div><span class="eyebrow">Save conflict</span><h2>Review newer source</h2></div><button class="icon-button" data-action="close-sheet" aria-label="Close">×</button></div>
    <div class="info-card conflict-card"><p>The source changed after this editor opened. Choose deliberately; nothing has been overwritten yet.</p><small>Remote source: ${escapeHTML(String(remote.source_sha256 || conflict.current_sha256 || "").slice(0, 12))}…</small>${fields}</div>
    <div class="conflict-diff"><article><span class="eyebrow">Your local body</span><p>${escapeHTML(textSummary(localBody))}</p><small>${localChanged ? "Changed locally" : "Unchanged locally"}</small></article><article><span class="eyebrow">Remote body</span><p>${escapeHTML(textSummary(remote.html))}</p><small>${remoteChanged ? "Changed remotely" : "Unchanged remotely"}</small></article></div>
    <div class="conflict-actions conflict-actions-stack"><button class="secondary-button" data-action="reload-conflict">Use remote version</button><button class="secondary-button" data-action="merge-conflict" ${mergeDisabled ? "disabled" : ""}>Merge safe changes, keep local body</button><button class="primary-button" data-action="overwrite-conflict">Keep all local edits</button></div>`;
}

function historySheetHTML(revisions) {
  const rows = revisions.length ? revisions.map((revision) => `<article class="revision-row"><div><strong>${escapeHTML(new Date(revision.created_at).toLocaleString())}</strong><small>${escapeHTML(revision.reason || "Source change")} · ${escapeHTML(String(revision.size || 0))} bytes</small></div><button class="secondary-button button-small" data-revision-id="${escapeHTML(revision.id)}">Restore</button></article>`).join("") : '<p class="empty-state">No revisions yet. A snapshot is created before the next change.</p>';
  return `<div class="sheet-heading"><div><span class="eyebrow">Source history</span><h2>Revision history</h2></div><button class="icon-button" data-action="close-sheet" aria-label="Close">×</button></div><div class="revision-list">${rows}</div>`;
}

async function openHistorySheet() {
  openSheet("history");
  const content = document.querySelector("#sheet-content");
  try {
    const data = await fetchJSON(`/_cms/api/revisions?path=${encodeURIComponent(state.record.path)}`);
    if (state.sheet !== "history") return;
    content.innerHTML = historySheetHTML(data.revisions || []);
  } catch (error) {
    content.innerHTML = `<p class="error-card">${escapeHTML(error.message)}</p>`;
  }
}

async function restoreRevision(id, button) {
  if (state.dirty && !window.confirm("Restore this revision and discard your unsaved editor changes?")) return;
  if (button) button.disabled = true;
  try {
    const body = new URLSearchParams({ path: state.record.path, id, base_sha256: state.record.source_sha256 });
    await fetchJSON("/_cms/api/revisions/restore", {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: body.toString(),
    });
    closeSheet();
    await refreshDocument(true);
    setStatus("Revision restored", "success");
  } catch (error) {
    if (error.status === 409) {
      await showConflict(error.payload);
    } else {
      setStatus(error.message, "error");
    }
    if (button) button.disabled = false;
  }
}

async function showConflict(payload) {
  state.conflict = payload || {};
  openSheet("conflict");
  const content = document.querySelector("#sheet-content");
  try {
    const url = state.conflict.reload_url || `/_cms/api/editor?path=${encodeURIComponent(state.record.path)}&engine=deckflow`;
    state.conflict.remote = await fetchJSON(url);
    state.conflict.merge = metadataMerge(state.conflict.remote.document);
    if (state.sheet === "conflict") content.innerHTML = conflictSheetHTML();
  } catch (error) {
    content.innerHTML = `<div class="sheet-heading"><div><span class="eyebrow">Save conflict</span><h2>Review newer source</h2></div><button class="icon-button" data-action="close-sheet" aria-label="Close">×</button></div><p class="error-card">Could not load the newer source: ${escapeHTML(error.message)}</p>`;
  }
}

async function useRemoteConflict() {
  closeSheet();
  await refreshDocument(true);
}

async function overwriteConflict() {
  const remote = state.conflict?.remote;
  if (!remote) return;
  state.record = remote;
  state.baseDocument = metadataFromDocument(remote.document);
  state.baseHTML = remote.html || "";
  state.record.source_sha256 = remote.source_sha256;
  state.conflict = null;
  closeSheet();
  setStatus("Retrying with local edits…", "busy");
  await saveDocument();
}

async function mergeConflict() {
  const remote = state.conflict?.remote;
  const merge = state.conflict?.merge;
  if (!remote || !merge || merge.conflicts.length) return;
  const baseBody = state.baseHTML || state.record?.html || "";
  const localBody = bodyHTMLFromEditorDocument(state.editor?.getHtml() || state.currentHTML);
  const localChanged = normalizedBodyHTML(localBody) !== normalizedBodyHTML(baseBody);
  const remoteChanged = normalizedBodyHTML(remote.html) !== normalizedBodyHTML(baseBody);
  if (localChanged && remoteChanged && normalizedBodyHTML(localBody) !== normalizedBodyHTML(remote.html)) return;
  if (!localChanged && remoteChanged) {
    await state.editor.setHtml(editorDocument(remote));
    state.currentHTML = state.editor.getHtml();
  }
  state.record = remote;
  state.baseDocument = metadataFromDocument(remote.document);
  state.baseHTML = remote.html || "";
  state.record.source_sha256 = remote.source_sha256;
  state.metadata = merge.merged;
  state.conflict = null;
  state.changeVersion += 1;
  closeSheet();
  setDirty(true);
  setStatus("Merged safe changes; saving…", "busy");
  await saveDocument();
}

function styleSheetHTML() {
  return `<div class="sheet-heading"><div><span class="eyebrow">Design</span><h2>${escapeHTML(state.record?.theme || "Theme")}</h2></div><button class="icon-button" data-action="close-sheet" aria-label="Close">×</button></div><div class="quick-actions"><button class="secondary-button" data-action="undo">↶ Undo</button><button class="secondary-button" data-action="redo">↷ Redo</button></div><div class="info-card"><strong>Theme controls stay in Fileloom</strong><p>Use the CMS Style tokens panel for site-wide colors, spacing, typography, and other theme variables. Element-level styling is available from the canvas when supported.</p><button class="secondary-button" data-action="cms">Open CMS</button></div>`;
}

function renderShell(record) {
  const title = escapeHTML(record.document?.title || record.path);
  app.innerHTML = `
    <div class="editor-shell">
      <header class="editor-topbar">
        <div class="topbar-leading"><button class="back-button" data-action="back" aria-label="Back to CMS">←<span class="desktop-only"> CMS</span></button><div class="brand-lockup"><span class="brand-mark">F</span><span class="desktop-only">FILELOOM</span></div></div>
        <div class="document-heading"><span class="eyebrow">Visual editor</span><strong title="${title}">${title}</strong><span class="dirty-dot" data-dirty-indicator hidden></span></div>
        <div class="topbar-actions"><span id="editor-status" data-kind="neutral">Ready</span><button class="icon-button desktop-only" data-action="undo" aria-label="Undo">↶</button><button class="icon-button desktop-only" data-action="redo" aria-label="Redo">↷</button><button class="secondary-button desktop-only" data-action="preview">Preview</button><button class="primary-button" data-action="save" disabled>Save</button><button class="icon-button mobile-only" data-action="more" aria-label="More">•••</button></div>
      </header>
      <main class="editor-workspace">
        <aside class="desktop-panel desktop-blocks"><div class="panel-heading"><div><span class="eyebrow">Add</span><h2>Blocks</h2></div></div>${blockCatalogHTML()}</aside>
        <section class="canvas-region"><div id="deckflow-canvas" aria-label="Editable page canvas"></div><div class="canvas-hint">Tap text to edit · Select an element for controls</div></section>
        <aside class="desktop-panel desktop-inspector"><div class="panel-heading"><div><span class="eyebrow">Inspect</span><h2>Selection</h2></div></div><div id="selection-info" class="selection-info"><strong>Nothing selected</strong><p>Choose an element in the canvas to edit it.</p></div><div class="inspector-divider"></div><div class="quick-actions inspector-actions"><button class="secondary-button" data-action="details">Details</button><button class="secondary-button" data-action="history">History</button></div><div class="info-card"><span class="eyebrow">Source</span><strong>${escapeHTML(record.path)}</strong><small data-document-summary>${escapeHTML(record.theme || "default")} theme · ${escapeHTML(record.document?.status || "published")} · HTML-first</small></div></aside>
      </main>
      <nav class="mobile-nav" aria-label="Editor tools"><button data-action="blocks"><span>＋</span><small>Blocks</small></button><button data-action="media"><span>▧</span><small>Media</small></button><button data-action="details"><span>≡</span><small>Details</small></button><button data-action="style"><span>◌</span><small>Style</small></button><button data-action="preview"><span>◉</span><small>Preview</small></button><button data-action="save"><span>↑</span><small>Save</small></button></nav>
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
  const codeAction = selectedCodeBlock() ? '<button class="secondary-button selection-code-action" data-action="edit-code">Edit code block</button>' : "";
  info.innerHTML = `<span class="eyebrow">Selected element</span><strong>&lt;${escapeHTML(label)}&gt;</strong><p>${escapeHTML(selection.textContent || "Empty element")}</p>${codeAction}${selectionOperationsHTML()}`;
}

async function saveDocument() {
  if (!state.editor || !state.dirty || state.saving) return;
  state.saving = true;
  setDirty(true);
  setStatus("Saving…", "busy");
  try {
    const html = await state.editor.flush();
    const versionAtStart = state.changeVersion;
    const metadata = metadataPayload();
    const payload = await fetchJSON("/_cms/api/editor-save", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ path: state.record.path, html, metadata, base_sha256: state.record.source_sha256, engine: "deckflow" }),
    });
    state.record.source_sha256 = payload.source_sha256;
    state.record.document = payload.document || state.record.document;
    state.record.html = payload.document?.html || state.record.html;
    state.baseDocument = metadataFromDocument(state.record.document);
    state.baseHTML = state.record.html || "";
    state.currentHTML = html;
    state.conflict = null;
    updateDocumentHeading();
    if (state.changeVersion === versionAtStart) {
      state.metadata = metadataFromDocument(state.record.document);
      setDirty(false);
      setStatus("Saved", "success");
    } else {
      setDirty(true);
      setStatus("Saved; newer edits still need saving", "dirty");
    }
  } catch (error) {
    if (error.status === 409) {
      await showConflict(error.payload);
      setStatus("Conflict — review the newer source", "error");
    } else {
      setStatus(error.message, "error");
    }
  } finally {
    state.saving = false;
    setDirty(state.dirty);
    if (state.conflict && !state.sheet) setStatus("Conflict — review the newer source", "error");
  }
}

async function refreshDocument(force = false) {
  if (!force && state.dirty && !window.confirm("Discard your unsaved changes and reload this page?")) return;
  const fresh = await fetchJSON(`/_cms/api/editor?path=${encodeURIComponent(state.path)}&engine=deckflow`);
  state.record = fresh;
  state.currentHTML = editorDocument(fresh);
  state.baseHTML = fresh.html || "";
  state.baseDocument = metadataFromDocument(fresh.document);
  state.metadata = metadataFromDocument(fresh.document);
  state.hostUndo = [];
  state.hostRedo = [];
  state.conflict = null;
  state.changeVersion += 1;
  await state.editor.setHtml(state.currentHTML);
  updateDocumentHeading();
  setDirty(false);
  setStatus("Reloaded", "success");
}

async function openPreview() {
  const previewWindow = window.open("about:blank", "_blank");
  if (!previewWindow) {
    setStatus("Preview was blocked by the browser", "error");
    return;
  }
  setStatus("Rendering preview…", "busy");
  try {
    const html = await state.editor.flush();
    const response = await fetch("/_cms/api/editor-preview", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ path: state.record.path, html, metadata: metadataPayload() }),
      cache: "no-store",
    });
    if (!response.ok) {
      let message = `${response.status} ${response.statusText}`;
      try { message = (await response.json()).error || message; } catch {}
      throw new Error(message);
    }
    const preview = await response.text();
    previewWindow.document.open();
    previewWindow.document.write(preview);
    previewWindow.document.close();
    setStatus("Preview opened", "success");
  } catch (error) {
    previewWindow.close();
    setStatus(error.message, "error");
  }
}

async function handleAction(action) {
  switch (action) {
    case "back":
      if (!state.dirty || window.confirm("Leave without saving your changes?")) window.location.href = "/_cms/";
      break;
    case "save":
      if (document.querySelector("#details-form") && !document.querySelector("#details-form").reportValidity()) break;
      await saveDocument();
      break;
    case "preview":
      await openPreview();
      break;
    case "blocks":
      openSheet("blocks");
      break;
    case "media":
      await openMediaSheet();
      break;
    case "edit-code":
      await openCodeSheet("update");
      break;
    case "properties":
      openSheet("properties");
      break;
    case "duplicate":
      await duplicateSelection();
      break;
    case "move-up":
      await moveSelection(-1);
      break;
    case "move-down":
      await moveSelection(1);
      break;
    case "delete-selection":
      await deleteSelection();
      break;
    case "style":
      openSheet("style");
      break;
    case "more":
      openSheet("details");
      break;
    case "details":
      openSheet("details");
      break;
    case "history":
      await openHistorySheet();
      break;
    case "undo":
      await undoEditorChange();
      break;
    case "redo":
      await redoEditorChange();
      break;
    case "reload-conflict":
      await useRemoteConflict();
      break;
    case "merge-conflict":
      await mergeConflict();
      break;
    case "overwrite-conflict":
      await overwriteConflict();
      break;
    case "keep-conflict":
      closeSheet();
      setStatus("Conflict kept — your edits are still local", "error");
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
    else if (block.dataset.block === "code") await openCodeSheet("insert");
    else await insertBlock(block.dataset.block);
    return;
  }
  const revision = event.target.closest("[data-revision-id]");
  if (revision) {
    await restoreRevision(revision.dataset.revisionId, revision);
    return;
  }
  const action = event.target.closest("[data-action]")?.dataset.action;
  if (action) await handleAction(action);
});

app.addEventListener("input", (event) => {
  const field = event.target.closest("#details-form [data-metadata]");
  if (field) updateMetadataDraft(field);
});

app.addEventListener("change", (event) => {
  const field = event.target.closest("#details-form [data-metadata]");
  if (field) updateMetadataDraft(field);
});

app.addEventListener("submit", async (event) => {
  const codeForm = event.target.closest("#code-form");
  const inspectorForm = event.target.closest("#element-inspector-form");
  if (!codeForm && !inspectorForm) return;
  event.preventDefault();
  if (codeForm) await submitCodeForm(codeForm);
  else await submitElementInspector(inspectorForm);
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
  state.baseHTML = state.record.html || "";
  state.baseDocument = metadataFromDocument(state.record.document);
  state.metadata = metadataFromDocument(state.record.document);
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
      state.changeVersion += 1;
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
