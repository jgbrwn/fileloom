import { createElementTarget } from "@deckflow/html-editor/core";
import { patchElementInHtml, patchElementsInHtml } from "@deckflow/html-editor/html-patch";
import { mountHtmlEditor } from "@deckflow/html-editor/ui";
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import { html as htmlLanguage } from "@codemirror/lang-html";
import { EditorState, Compartment } from "@codemirror/state";
import { oneDark } from "@codemirror/theme-one-dark";
import { EditorView, keymap, lineNumbers } from "@codemirror/view";
import {
  injectThemePreviewStyles,
  shieldThemeTemplate,
  stripThemePreviewStyles,
  unshieldThemeTemplate,
} from "./theme-template.js";
import "./style.css";

const sourceWrapStorageKey = "fileloom.editor.source-wrap-lines";
const youtubeVideoIDPattern = /^[A-Za-z0-9_-]{11}$/;

function readSourceWrapPreference() {
  try {
    return window.localStorage.getItem(sourceWrapStorageKey) === "true";
  } catch {
    return false;
  }
}

function writeSourceWrapPreference(value) {
  try {
    window.localStorage.setItem(sourceWrapStorageKey, value ? "true" : "false");
  } catch {
    // Local preferences are best effort; source editing must still work.
  }
}

const app = document.querySelector("#app");
const params = new URLSearchParams(window.location.search);
const path = params.get("path") || "";
const themeParam = params.get("theme") || "";
const state = {
  path,
  record: null,
  editor: null,
  currentHTML: "",
  baseHTML: "",
  baseDocument: null,
  metadata: null,
  resource: "content",
  themeTokens: [],
  dirty: false,
  saving: false,
  changeVersion: 0,
  hostUndo: [],
  hostRedo: [],
  historyTask: Promise.resolve(),
  editorKeyboardCleanup: null,
  conflict: null,
  selected: null,
  codeEdit: null,
  sourceEditorView: null,
  sourceWrapCompartment: null,
  sourceBodyHTML: "",
  baseBodyHTML: "",
  sourceBodyOverride: undefined,
  sheet: null,
  sourceWrapLines: readSourceWrapPreference(),
};

const metadataFields = ["title", "date", "tags", "category", "excerpt", "status", "publish_at", "comments"];

function metadataFromDocument(document) {
  const hasComments = document && Object.prototype.hasOwnProperty.call(document, "comments");
  return {
    title: String(document?.title || ""),
    date: String(document?.date || ""),
    tags: [...(document?.tags || [])],
    category: String(document?.category || ""),
    excerpt: String(document?.excerpt || ""),
    status: String(document?.status || "published"),
    publish_at: String(document?.publish_at || ""),
    comments: hasComments ? document.comments === true : null,
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

function rawBodyHTMLFromEditorDocument(html) {
  const input = String(html || "");
  const opening = /<body\b[^>]*>/i.exec(input);
  if (!opening) return input;
  const bodyStart = opening.index + opening[0].length;
  const bodyEnd = input.toLowerCase().lastIndexOf("</body>");
  if (bodyEnd < bodyStart) return input.slice(bodyStart);
  return input.slice(bodyStart, bodyEnd);
}

function validYouTubeMarkerFromSource(source) {
  try {
    const parsed = new DOMParser().parseFromString(String(source || ""), "text/html");
    return [...parsed.querySelectorAll("[data-fileloom-youtube]")].some((element) => youtubeVideoIDPattern.test(String(element.getAttribute("data-youtube-id") || "").trim()));
  } catch {
    return false;
  }
}

function validFileloomMediaMarkerFromSource(source) {
  try {
    const parsed = new DOMParser().parseFromString(String(source || ""), "text/html");
    return validYouTubeMarkerFromSource(source) || Boolean(parsed.querySelector("[data-fileloom-video], figure.fileloom-video"));
  } catch {
    return /data-fileloom-video|fileloom-video/i.test(String(source || ""));
  }
}

function ensureEditorMediaStyles(html) {
  const source = String(html || "");
  if (!validFileloomMediaMarkerFromSource(source) || source.includes("/_cms/assets/fileloom-media.css")) return source;
  const link = '<link rel="stylesheet" href="/_cms/assets/fileloom-media.css">';
  const index = source.toLowerCase().indexOf("</head>");
  return index >= 0 ? `${source.slice(0, index)}${link}${source.slice(index)}` : `${link}${source}`;
}

function rawBodyHTMLForRecord(record = state.record) {
  if (!isThemeResource(record) && typeof record?.body_html === "string") return record.body_html;
  return String(record?.html || "");
}

function localBodyHTML() {
  return isThemeResource() ? sourceHTMLForSave(state.editor?.getHtml() || state.currentHTML) : state.sourceBodyHTML;
}

function baseBodyHTML() {
  return typeof state.baseBodyHTML === "string" ? state.baseBodyHTML : rawBodyHTMLForRecord();
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

function isThemeResource(record = state.record) {
  return record?.resource === "theme-layout" || Boolean(record?.theme && String(record?.path || "").startsWith("themes/"));
}

function editorRequestURL() {
  if (themeParam) return `/_cms/api/editor?theme=${encodeURIComponent(themeParam)}&engine=deckflow`;
  return `/_cms/api/editor?path=${encodeURIComponent(path)}&engine=deckflow`;
}

function sourceHTMLForSave(html) {
  if (!isThemeResource()) return html;
  return unshieldThemeTemplate(stripThemePreviewStyles(html), state.themeTokens);
}

function editorDocument(record) {
  if (isThemeResource(record)) {
    const shielded = shieldThemeTemplate(record.html || "");
    state.themeTokens = shielded.tokens;
    return injectThemePreviewStyles(shielded.html, record.stylesheet_css || "");
  }
  const css = String(record.stylesheet_css || "").replace(/<\/style/gi, "<\\/style");
  const mediaCSS = validFileloomMediaMarkerFromSource(rawBodyHTMLForRecord(record)) ? '<link rel="stylesheet" href="/_cms/assets/fileloom-media.css">' : "";
  return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/_cms/assets/fileloom-code.css">${mediaCSS}<style>${css}</style><style>html{background:#fff}body{min-height:100vh;margin:0}</style></head><body>${rawBodyHTMLForRecord(record)}</body></html>`;
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
function structuralSelectorMatch(element, selector) {
  if (!element || !selector) return false;
  if (selector === "pre.fileloom-code-block, pre[data-fileloom-code]") return element.tagName === "PRE" && (element.classList.contains("fileloom-code-block") || element.hasAttribute("data-fileloom-code"));
  if (selector === "figure.fileloom-image") return element.tagName === "FIGURE" && element.classList.contains("fileloom-image");
  if (selector === "figure.fileloom-video") return element.tagName === "FIGURE" && element.classList.contains("fileloom-video");
  if (selector === "img") return element.tagName === "IMG";
  if (selector === "video") return element.tagName === "VIDEO";
  if (selector === "[data-fileloom-youtube]") return element.hasAttribute("data-fileloom-youtube");
  return false;
}

function rawElementRangesForSelector(source, selector) {
  const tagNames = selector === "img" ? ["img"] : selector === "video" ? ["video"] : selector === "pre.fileloom-code-block, pre[data-fileloom-code]" ? ["pre"] : selector === "[data-fileloom-youtube]" ? ["figure", "div"] : ["figure"];
  const ranges = [];
  for (const tagName of tagNames) {
    for (const opening of rawOpeningTagRanges(source, tagName)) {
      const commentStart = source.lastIndexOf("<!--", opening.start);
      const commentEnd = source.lastIndexOf("-->", opening.start);
      if (commentStart > commentEnd) continue;
      const openingTag = source.slice(opening.start, opening.end);
      let element = null;
      try {
        const parsed = new DOMParser().parseFromString(`${openingTag}</${tagName}>`, "text/html");
        element = parsed.body?.firstElementChild || null;
      } catch {
        element = null;
      }
      if (!structuralSelectorMatch(element, selector)) continue;
      const end = ["img"].includes(tagName) || /\/\s*>$/.test(openingTag) ? opening.end : matchingElementEnd(source, opening.start, opening.end, tagName);
      if (end >= opening.end) ranges.push({ start: opening.start, end });
    }
  }
  return ranges.sort((left, right) => left.start - right.start);
}

function sourceFallbackRange(source, info, selector) {
  if (!info || !selector) return null;
  const ranges = rawElementRangesForSelector(source, selector);
  const index = Number.isInteger(info.selectorIndex) && info.selectorIndex >= 0
    ? info.selectorIndex
    : Number.isInteger(info.target?.selectorIndex) && info.target.selectorIndex >= 0 ? info.target.selectorIndex : 0;
  return ranges[index] || null;
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
  if (occurrence >= 0) {
    const exact = sourceRangeFromHTML(source, selectedHTML, occurrence);
    if (exact) return exact;
  }
  const selector = element.matches?.("pre.fileloom-code-block, pre[data-fileloom-code]") ? "pre.fileloom-code-block, pre[data-fileloom-code]"
    : element.matches?.("figure.fileloom-image") ? "figure.fileloom-image"
    : element.matches?.("figure.fileloom-video") ? "figure.fileloom-video"
      : element.matches?.("[data-fileloom-youtube]") ? "[data-fileloom-youtube]"
        : element.matches?.("img") ? "img"
          : element.matches?.("video") ? "video" : "";
  if (!selector) return null;
  const selectorCandidates = [...parsed.querySelectorAll(selector)];
  const selectorIndex = selectorCandidates.indexOf(element);
  const fallback = sourceFallbackRange(source, { element, selectorIndex }, selector);
  return fallback ? { ...fallback, html: source.slice(fallback.start, fallback.end) } : null;
}

function sourceSelectionRange(source, selection, closestSelector = "") {
  const info = sourceSelectionInfo(source, selection, closestSelector);
  if (!info) return null;
  const range = sourceRangeFromHTML(source, info.selectedHTML, info.occurrence);
  if (range) return { ...info, ...range };
  const fallbackSelector = closestSelector || (selection === state.selected ? selectedStructuralSelector() : "");
  const fallback = sourceFallbackRange(source, info, fallbackSelector);
  return fallback ? { ...info, ...fallback, html: source.slice(fallback.start, fallback.end) } : null;
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

function sourceOrEditorHTML() {
  return isThemeResource() ? (state.editor?.getHtml() || state.currentHTML) : state.sourceBodyHTML;
}

async function applySourceHTML(nextSource, options = {}) {
  if (isThemeResource()) return applyEditorHTML(nextSource, options);
  return applyEditorHTML(replaceEditorBodyHTML(state.editor?.getHtml() || state.currentHTML, nextSource), {
    ...options,
    sourceBodyHTML: nextSource,
  });
}

function sourceBodyAfterDeckflowChange(previousBody, html, patches) {
  if (Array.isArray(patches) && patches.length) {
    const result = patchElementsInHtml(`<body>${previousBody}</body>`, patches);
    if (result.matched) return rawBodyHTMLFromEditorDocument(result.html);
  }
  return rawBodyHTMLFromEditorDocument(html);
}

async function replaceEditorHTML(next, { clearSelection = true, sourceBodyHTML } = {}) {
  if (!state.editor) return;
  if (clearSelection) {
    state.selected = null;
    updateSelection(null);
  }
  state.sourceBodyOverride = sourceBodyHTML;
  try {
    await state.editor.setHtml(isThemeResource() ? next : ensureEditorMediaStyles(next));
    state.currentHTML = state.editor.getHtml();
  } finally {
    if (!isThemeResource() && sourceBodyHTML !== undefined) state.sourceBodyHTML = sourceBodyHTML;
    state.sourceBodyOverride = undefined;
  }
  installEditorKeyboardShortcuts();
}

function pushHostUndo(entry, bodyHTML = state.sourceBodyHTML) {
  const value = typeof entry === "string" ? { html: entry, bodyHTML } : entry;
  if (!value?.html) return;
  state.hostUndo.push({ html: value.html, bodyHTML: value.bodyHTML });
  if (state.hostUndo.length > 50) state.hostUndo.shift();
}

function recordHostChange(previous, next, previousBodyHTML = state.sourceBodyHTML) {
  if (!previous || previous === next) return;
  pushHostUndo({ html: previous, bodyHTML: previousBodyHTML });
  state.hostRedo = [];
}

function queueHistoryOperation(operation) {
  const task = state.historyTask.then(operation, operation);
  state.historyTask = task.catch(() => {});
  return task;
}

function installEditorKeyboardShortcuts() {
  state.editorKeyboardCleanup?.();
  state.editorKeyboardCleanup = null;
  const previewDocument = state.editor?.iframe?.contentDocument;
  if (!previewDocument) return;
  const handleKeyDown = (event) => {
    const target = event.target;
    const key = event.key.toLowerCase();
    if (!(event.metaKey || event.ctrlKey)) return;
    if (key === "s") {
      event.preventDefault();
      event.stopImmediatePropagation();
      void saveDocument();
      return;
    }
    if (target?.isContentEditable || target?.closest?.("[contenteditable]")) return;
    if (target?.closest?.("input, textarea, select")) return;
    if (key !== "z" && key !== "y") return;
    event.preventDefault();
    event.stopImmediatePropagation();
    if (key === "y" || event.shiftKey) void redoEditorChange();
    else void undoEditorChange();
  };
  previewDocument.addEventListener("keydown", handleKeyDown, true);
  state.editorKeyboardCleanup = () => previewDocument.removeEventListener("keydown", handleKeyDown, true);
}

async function applyEditorHTML(next, { recordHistory = true, sourceBodyHTML } = {}) {
  if (!state.editor) return;
  const previous = state.editor.getHtml();
  if (next === previous && sourceBodyHTML === undefined) return;
  const previousBodyHTML = state.sourceBodyHTML;
  if (recordHistory) recordHostChange(previous, next, previousBodyHTML);
  state.changeVersion += 1;
  await replaceEditorHTML(next, { sourceBodyHTML });
  if (!isThemeResource() && sourceBodyHTML === undefined) state.sourceBodyHTML = rawBodyHTMLFromEditorDocument(state.currentHTML);
  setDirty(true);
}

async function undoEditorChangeNow() {
  if (!state.editor) return;
  await state.editor.flush();
  if (!state.hostUndo.length) {
    setStatus("Nothing to undo", "neutral");
    return;
  }
  const current = state.editor.getHtml();
  const previous = state.hostUndo.pop();
  state.hostRedo.push({ html: current, bodyHTML: state.sourceBodyHTML });
  if (state.hostRedo.length > 50) state.hostRedo.shift();
  state.changeVersion += 1;
  await replaceEditorHTML(previous.html, { sourceBodyHTML: isThemeResource() ? undefined : previous.bodyHTML });
  setDirty(true);
  setStatus("Undid change", "dirty");
}

function undoEditorChange() {
  return queueHistoryOperation(undoEditorChangeNow);
}

async function redoEditorChangeNow() {
  if (!state.editor) return;
  await state.editor.flush();
  if (!state.hostRedo.length) {
    setStatus("Nothing to redo", "neutral");
    return;
  }
  const current = state.editor.getHtml();
  const next = state.hostRedo.pop();
  pushHostUndo({ html: current, bodyHTML: state.sourceBodyHTML });
  state.changeVersion += 1;
  await replaceEditorHTML(next.html, { sourceBodyHTML: isThemeResource() ? undefined : next.bodyHTML });
  setDirty(true);
  setStatus("Redid change", "dirty");
}

function redoEditorChange() {
  return queueHistoryOperation(redoEditorChangeNow);
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

function codeBlockOccurrence(block) {
  if (!block?.ownerDocument) return -1;
  return [...block.ownerDocument.querySelectorAll("pre.fileloom-code-block, pre[data-fileloom-code]")].indexOf(block);
}

function isCodeBlockOpeningTag(tag) {
  if (/\bdata-fileloom-code(?:\s|=|\/?>)/i.test(tag)) return true;
  const classValue = tag.match(/\bclass\s*=\s*(["'])(.*?)\1/i)?.[2] || "";
  return classValue.split(/\s+/).includes("fileloom-code-block");
}

function matchingElementEnd(source, start, openingEnd, tagName) {
  let depth = 1;
  let cursor = openingEnd;
  const normalizedTag = String(tagName || "").toLowerCase();
  while (cursor < source.length) {
    const tagStart = source.indexOf("<", cursor);
    if (tagStart < 0) return -1;
    if (source.startsWith("<!--", tagStart)) {
      const commentEnd = source.indexOf("-->", tagStart + 4);
      cursor = commentEnd < 0 ? source.length : commentEnd + 3;
      continue;
    }
    const closing = source[tagStart + 1] === "/";
    const nameMatch = source.slice(tagStart).match(/^<\/?\s*([A-Za-z][A-Za-z0-9:-]*)\b/);
    if (!nameMatch || nameMatch[1].toLowerCase() !== normalizedTag) {
      cursor = tagStart + 1;
      continue;
    }
    const tagEnd = scanOpeningTagEnd(source, tagStart);
    if (tagEnd < 0) return -1;
    if (closing) depth -= 1;
    else if (!/\/\s*>$/.test(source.slice(tagStart, tagEnd))) depth += 1;
    if (depth === 0) return tagEnd;
    cursor = tagEnd;
  }
  return -1;
}

function codeBlockSourceRange(source, occurrence) {
  let cursor = 0;
  let found = 0;
  while (cursor < source.length) {
    const start = source.indexOf("<", cursor);
    if (start < 0) return null;
    if (source.startsWith("<!--", start)) {
      const commentEnd = source.indexOf("-->", start + 4);
      cursor = commentEnd < 0 ? source.length : commentEnd + 3;
      continue;
    }
    const nameMatch = source.slice(start).match(/^<\s*([A-Za-z][A-Za-z0-9:-]*)\b/);
    if (!nameMatch || nameMatch[1].toLowerCase() !== "pre" || source[start + 1] === "/") {
      cursor = start + 1;
      continue;
    }
    const openingEnd = scanOpeningTagEnd(source, start);
    if (openingEnd < 0) return null;
    const openingTag = source.slice(start, openingEnd);
    if (isCodeBlockOpeningTag(openingTag)) {
      if (found === occurrence) {
        const end = matchingElementEnd(source, start, openingEnd, "pre");
        return end < 0 ? null : { start, end };
      }
      found += 1;
    }
    cursor = openingEnd;
  }
  return null;
}

function replaceCodeBlockSource(source, block, replacement) {
  const occurrence = codeBlockOccurrence(block);
  const range = occurrence < 0 ? null : codeBlockSourceRange(source, occurrence);
  if (!range) return source;
  return `${source.slice(0, range.start)}${replacement}${source.slice(range.end)}`;
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

function selectedVideoBlock() {
  const element = state.selected?.element;
  const figure = element?.closest?.("figure.fileloom-video");
  if (figure?.querySelector("video")) return figure;
  return element?.closest?.("video") || null;
}

function selectedYouTubeBlock() {
  return state.selected?.element?.closest?.("[data-fileloom-youtube]") || null;
}

function selectedStructuralSelector() {
  const element = selectedStructuralElement();
  if (!element) return "";
  if (selectedCodeBlock()) return "pre.fileloom-code-block, pre[data-fileloom-code]";
  if (selectedImageBlock()) return element.tagName === "FIGURE" ? "figure.fileloom-image" : "img";
  if (selectedVideoBlock()) return element.tagName === "FIGURE" ? "figure.fileloom-video" : "video";
  if (selectedYouTubeBlock()) return "[data-fileloom-youtube]";
  return "";
}

function selectedStructuralElement() {
  return selectedCodeBlock() || selectedImageBlock() || selectedVideoBlock() || selectedYouTubeBlock() || state.selected?.element || null;
}

function structuralSelectionInfo(source) {
  return sourceSelectionRange(source, state.selected, selectedStructuralSelector());
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
  const source = sourceOrEditorHTML();
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
  const next = `${source.slice(0, range.start)}${nextOpeningTag}${source.slice(range.end)}`;
  await applySourceHTML(next);
  closeSheet();
  setStatus("Element properties updated", "dirty");
}

function selectionOperationsHTML() {
  const element = selectedStructuralElement();
  if (!element || ["BODY", "HTML"].includes(element.tagName)) return "";
  const previous = element.previousElementSibling;
  const next = element.nextElementSibling;
  let mediaAction = "";
  if (selectedImageBlock()) mediaAction = '<button class="secondary-button" data-action="media">Replace image</button>';
  else if (selectedVideoBlock()) mediaAction = '<button class="secondary-button" data-action="media">Replace video</button>';
  else if (selectedYouTubeBlock()) mediaAction = '<button class="secondary-button" data-action="media">Replace YouTube video</button>';
  return `<div class="selection-actions"><span class="eyebrow">Element actions</span><div class="selection-action-grid"><button class="secondary-button" data-action="properties">Edit properties</button>${mediaAction}<button class="secondary-button" data-action="duplicate">Duplicate</button><button class="secondary-button" data-action="move-up" ${previous ? "" : "disabled"}>Move up</button><button class="secondary-button" data-action="move-down" ${next ? "" : "disabled"}>Move down</button><button class="secondary-button danger-button" data-action="delete-selection">Delete</button></div></div>`;
}

function duplicateWithoutIDs(element) {
  const clone = element.cloneNode(true);
  clone.removeAttribute?.("id");
  clone.querySelectorAll?.("[id]").forEach((node) => node.removeAttribute("id"));
  return cleanSelectedHTML(clone);
}

async function duplicateSelection() {
  const source = sourceOrEditorHTML();
  const info = structuralSelectionInfo(source);
  if (!info) return;
  const duplicate = duplicateWithoutIDs(info.element);
  if (!duplicate) return;
  const next = `${source.slice(0, info.end)}\n${duplicate}\n${source.slice(info.end)}`;
  await applySourceHTML(next);
  closeSheet();
  setStatus("Element duplicated", "dirty");
}

function patchSelectedStructuralElement(source, operation) {
  const element = selectedStructuralElement();
  if (!element) return null;
  const patchSource = isThemeResource() ? source : `<body>${source}</body>`;
  const result = patchElementInHtml(patchSource, createElementTarget(element), [operation]);
  if (!result.matched) return null;
  return isThemeResource() ? result.html : rawBodyHTMLFromEditorDocument(result.html);
}


async function deleteSelection() {
  const source = sourceOrEditorHTML();
  const next = patchSelectedStructuralElement(source, { type: "delete-element" });
  if (next == null || next === source) {
    setStatus("Could not resolve the selected element", "error");
    return;
  }
  await applySourceHTML(next);
  closeSheet();
  setStatus("Element deleted", "dirty");
}

async function moveSelection(direction) {
  const source = sourceOrEditorHTML();
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
  await applySourceHTML(next);
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
  const lines = values.code ? values.code.split("\n").length : 0;
  const summary = `${lines} ${lines === 1 ? "line" : "lines"} · ${values.code.length} characters`;
  return `<div class="sheet-heading"><div><span class="eyebrow">${mode === "update" ? "Edit element" : "Insert"}</span><h2>Code block</h2><p class="sheet-subtitle">Write code with its language and formatting intact.</p></div><button class="icon-button" data-action="close-sheet" aria-label="Close">×</button></div>
    <form id="code-form" class="code-form" data-code-mode="${mode}">
      <div class="code-form-toolbar"><label class="form-field"><span>Language</span><select name="language" aria-label="Code language">${options}</select></label><span class="code-editor-summary" data-code-summary>${summary}</span></div>
      <label class="form-field code-editor-field"><span class="code-editor-label"><strong>Code</strong><small>Indentation and line breaks are preserved</small></span><textarea name="code" rows="18" spellcheck="false" autocapitalize="off" autocomplete="off" autocorrect="off" aria-label="Code" placeholder="Paste or type code here…">${escapeHTML(values.code)}</textarea></label>
      <p class="code-help"><strong>Preview styling follows the active theme.</strong> Code is saved as plain <code>&lt;pre&gt;&lt;code&gt;</code> HTML and highlighted when the page is published.</p>
      <div class="details-actions"><button type="button" class="secondary-button" data-action="close-sheet">Cancel</button><button type="submit" class="primary-button">${mode === "update" ? "Update code" : "Insert code"}</button></div>
    </form>`;
}

async function openCodeSheet(mode = "insert") {
  state.codeEdit = mode === "update" ? { block: selectedCodeBlock(), occurrence: codeBlockOccurrence(selectedCodeBlock()) } : null;
  openSheet("code");
  document.querySelector("#sheet-content").innerHTML = codeSheetHTML(mode);
  updateCodeSummary(document.querySelector("#code-form"));
}

function updateCodeSummary(form) {
  const summary = form?.querySelector("[data-code-summary]");
  const code = form?.elements?.code?.value || "";
  if (!summary) return;
  const lines = code ? code.split("\n").length : 0;
  summary.textContent = `${lines} ${lines === 1 ? "line" : "lines"} · ${code.length} characters`;
}

async function submitCodeForm(form) {
  const language = form.elements.language.value || "plaintext";
  const code = form.elements.code.value;
  const mode = form.dataset.codeMode;
  if (mode === "update") {
    await state.editor.flush();
    const source = sourceOrEditorHTML();
    const block = state.codeEdit?.block || selectedCodeBlock();
    const next = replaceCodeBlockSource(source, block, codeBlockFragment(language, code));
    if (next === source) {
      setStatus("Could not resolve the selected code block", "error");
      return;
    }
    await applySourceHTML(next);
    closeSheet();
    setStatus("Code block updated", "dirty");
    return;
  }
  const hadSelection = Boolean(state.selected);
  const next = insertAfterSelection(sourceOrEditorHTML(), codeBlockFragment(language, code), state.selected);
  await applySourceHTML(next);
  closeSheet();
  setStatus(hadSelection ? "Code block added after selection" : "Code block added", "dirty");
}

function replaceEditorBodyHTML(source, bodyHTML) {
  const input = String(source || "");
  const opening = /<body\b[^>]*>/i.exec(input);
  if (!opening) return String(bodyHTML || "");
  const bodyStart = opening.index + opening[0].length;
  const bodyEnd = input.toLowerCase().lastIndexOf("</body>");
  if (bodyEnd < bodyStart) return String(bodyHTML || "");
  return `${input.slice(0, bodyStart)}${bodyHTML || ""}${input.slice(bodyEnd)}`;
}

function sourceEditorValue() {
  const view = state.sourceEditorView;
  if (view) return view.state.sliceDoc();
  return String(document.querySelector("#source-form [data-source-mirror]")?.value || "");
}

function syncSourceEditorMirror() {
  const form = document.querySelector("#source-form");
  const mirror = form?.querySelector("[data-source-mirror]");
  if (mirror) mirror.value = sourceEditorValue();
}

function destroySourceEditor() {
  state.sourceEditorView?.destroy();
  state.sourceEditorView = null;
  state.sourceWrapCompartment = null;
}

function sourceEditorLineSeparator(source) {
  if (String(source).includes("\r\n")) return "\r\n";
  if (String(source).includes("\r")) return "\r";
  return "\n";
}

function mountSourceEditor(source) {
  destroySourceEditor();
  const parent = document.querySelector("[data-source-editor]");
  if (!parent) return;
  const wrapCompartment = new Compartment();
  state.sourceWrapCompartment = wrapCompartment;
  const editorState = EditorState.create({
    doc: String(source || ""),
    extensions: [
      EditorState.lineSeparator.of(sourceEditorLineSeparator(source)),
      htmlLanguage(),
      oneDark,
      lineNumbers(),
      history(),
      keymap.of([...defaultKeymap, ...historyKeymap, indentWithTab]),
      EditorView.contentAttributes.of({
        spellcheck: "false",
        autocapitalize: "off",
        autocomplete: "off",
        autocorrect: "off",
        "aria-label": "HTML source",
      }),
      wrapCompartment.of(state.sourceWrapLines ? EditorView.lineWrapping : []),
      EditorView.updateListener.of((update) => {
        if (!update.docChanged) return;
        syncSourceEditorMirror();
      }),
    ],
  });
  state.sourceEditorView = new EditorView({ state: editorState, parent });
  parent.dataset.wrapLines = state.sourceWrapLines ? "on" : "off";
  const cmEditor = parent.querySelector(".cm-editor");
  if (cmEditor) cmEditor.dataset.wrapLines = parent.dataset.wrapLines;
  syncSourceEditorMirror();
}

function updateSourceEditorWrap() {
  const parent = document.querySelector("[data-source-editor]");
  const wrapLines = state.sourceWrapLines ? "on" : "off";
  if (parent) {
    parent.dataset.wrapLines = wrapLines;
    const cmEditor = parent.querySelector(".cm-editor");
    if (cmEditor) cmEditor.dataset.wrapLines = wrapLines;
  }
  const mirror = document.querySelector("#source-form [data-source-mirror]");
  if (mirror) mirror.dataset.wrapLines = wrapLines;
  if (state.sourceEditorView && state.sourceWrapCompartment) {
    state.sourceEditorView.dispatch({
      effects: state.sourceWrapCompartment.reconfigure(state.sourceWrapLines ? EditorView.lineWrapping : []),
    });
  }
  syncSourceEditorMirror();
}

function sourceSheetHTML(source) {
  const wrapLines = state.sourceWrapLines ? "on" : "off";
  return `<div class="sheet-heading"><div><span class="eyebrow">Advanced editing</span><h2>HTML source</h2><p class="sheet-subtitle">Edit the body HTML for <strong>${escapeHTML(state.record?.path || "this page")}</strong>.</p></div><button class="icon-button" data-action="close-sheet" aria-label="Close">×</button></div>
    <form id="source-form" class="source-form">
      <div class="source-editor-toolbar"><span class="code-editor-label"><strong>Body HTML</strong><small>Front matter and page metadata stay protected</small></span><label class="source-wrap-toggle"><input type="checkbox" data-source-wrap-lines aria-label="Wrap lines" ${state.sourceWrapLines ? "checked" : ""}><span class="source-wrap-copy"><strong>Wrap lines</strong><small>Presentation only</small></span></label></div>
      <div class="source-editor-field"><div class="source-editor" data-source-editor data-wrap-lines="${wrapLines}" aria-label="HTML source"></div><input type="hidden" name="html" data-source-mirror data-wrap-lines="${wrapLines}" value=""></div>
      <p class="source-warning"><strong>Use this for precise HTML changes.</strong> Visual edits, code blocks, comments, and custom attributes remain source-backed; the next visual edit will use this HTML.</p>
      <div class="details-actions"><button type="button" class="secondary-button" data-action="close-sheet">Cancel</button><button type="submit" class="primary-button">Apply HTML</button></div>
    </form>`;
}

function syncSourceWrapPreference() {
  const parent = document.querySelector("[data-source-editor]");
  const wrapLines = state.sourceWrapLines ? "on" : "off";
  if (parent) {
    parent.dataset.wrapLines = wrapLines;
    const cmEditor = parent.querySelector(".cm-editor");
    if (cmEditor) cmEditor.dataset.wrapLines = wrapLines;
  }
  const toggle = document.querySelector("#source-form [data-source-wrap-lines]");
  if (toggle) toggle.checked = state.sourceWrapLines;
  updateSourceEditorWrap();
}

function setSourceWrapPreference(value) {
  state.sourceWrapLines = Boolean(value);
  writeSourceWrapPreference(state.sourceWrapLines);
  syncSourceWrapPreference();
}

async function openSourceSheet() {
  if (isThemeResource()) {
    setStatus("Theme layouts are edited from the CMS theme workflow", "neutral");
    return;
  }
  await state.editor.flush();
  const source = state.sourceBodyHTML;
  openSheet("source");
  document.querySelector("#sheet-content").innerHTML = sourceSheetHTML(source);
  mountSourceEditor(source);
}

async function submitSourceForm(form) {
  await state.editor.flush();
  const bodyHTML = sourceEditorValue();
  if (bodyHTML === state.sourceBodyHTML) {
    setStatus("No HTML changes", "neutral");
    return;
  }
  const source = state.editor.getHtml();
  const next = replaceEditorBodyHTML(source, bodyHTML);
  await applyEditorHTML(next, { sourceBodyHTML: bodyHTML });
  closeSheet();
  setStatus("HTML source applied", "dirty");
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
  const next = insertAfterSelection(sourceOrEditorHTML(), blockFragment(type), state.selected);
  await applySourceHTML(next);
  closeSheet();
  setStatus(hadSelection ? "Block added after selection" : "Block added", "dirty");
}

function youtubeVideoIDFromURL(value) {
  const input = String(value || "").trim();
  if (!input || /[\s\u0000-\u001f\u007f]/.test(input)) throw new Error("Enter one HTTPS YouTube URL");
  let parsed;
  try { parsed = new URL(input); } catch { throw new Error("YouTube URL is invalid"); }
  if (parsed.protocol !== "https:") throw new Error("YouTube URL must use HTTPS");
  if (!parsed.hostname || parsed.username || parsed.password || parsed.port) throw new Error("YouTube URL host is not allowed");
  if (parsed.hash) throw new Error("YouTube URL fragments are not supported");
  const host = parsed.hostname.toLowerCase();
  let id = "";
  if (host === "youtube.com" || host === "www.youtube.com") {
    if (parsed.pathname === "/watch") {
      const values = parsed.searchParams.getAll("v");
      if (values.length !== 1) throw new Error("YouTube watch URL must contain one video ID");
      id = values[0];
    } else {
      const parts = parsed.pathname.split("/").filter((part) => part !== "");
      if (parts.length !== 2 || !["shorts", "embed"].includes(parts[0]) || parsed.pathname !== `/${parts[0]}/${parts[1]}`) {
        throw new Error("YouTube URL form is not allowed");
      }
      id = parts[1];
    }
  } else if (host === "youtu.be") {
    const parts = parsed.pathname.split("/").filter((part) => part !== "");
    if (parts.length !== 1 || parsed.pathname !== `/${parts[0]}`) throw new Error("youtu.be URL form is not allowed");
    id = parts[0];
  } else {
    throw new Error("YouTube URL host is not allowed");
  }
  if (!youtubeVideoIDPattern.test(id)) throw new Error("YouTube video ID must be exactly 11 characters");
  return id;
}

function youtubeMarkerFragment(id, title = "YouTube video", fallback = "Watch on YouTube") {
  if (!youtubeVideoIDPattern.test(String(id || ""))) return "";
  return `<figure class="fileloom-youtube" data-fileloom-youtube data-youtube-id="${escapeHTML(id)}" data-youtube-title="${escapeHTML(title)}" data-youtube-fallback="${escapeHTML(fallback)}"><div class="fileloom-youtube-placeholder" role="img" aria-label="${escapeHTML(title)}"><span class="fileloom-youtube-play" aria-hidden="true">▶</span><span class="fileloom-youtube-copy"><strong>${escapeHTML(title)}</strong><small>${escapeHTML(fallback)}</small></span></div></figure>`;
}

function mediaFileKind(file) {
  if (["image", "video"].includes(String(file?.media_type || ""))) return String(file.media_type);
  const name = String(file?.name || file?.path || "");
  if (/\.(mp4|webm)$/i.test(name)) return "video";
  if (/\.(avif|gif|jpe?g|png|svg|webp)$/i.test(name)) return "image";
  return "other";
}

function mediaFileLabel(file) {
  const name = String(file?.name || file?.path || "");
  const extension = name.includes(".") ? name.split(".").pop().toUpperCase() : "MEDIA";
  return `${extension} · ${formatMediaBytes(file?.size)}`;
}

function formatMediaBytes(value) {
  const size = Number(value);
  if (!Number.isFinite(size) || size < 0) return "Size unavailable";
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(size < 10240 ? 1 : 0)} KB`;
  return `${(size / (1024 * 1024)).toFixed(size < 10 * 1024 * 1024 ? 1 : 0)} MB`;
}

function formatMediaDuration(value) {
  const duration = Number(value);
  if (!Number.isFinite(duration) || duration < 0) return "Metadata pending";
  const total = Math.round(duration);
  const minutes = Math.floor(total / 60);
  const seconds = String(total % 60).padStart(2, "0");
  return `${minutes}:${seconds}`;
}

function mediaCardHTML(file) {
  const kind = mediaFileKind(file);
  const url = mediaURL(file);
  const name = String(file?.name || file?.path || "media");
  const metadata = mediaFileLabel(file);
  if (kind === "video") {
    return `<article class="media-card media-card-video"><video class="media-preview" src="${escapeHTML(url)}" controls preload="metadata" muted playsinline aria-label="Preview ${escapeHTML(name)}"></video><button type="button" class="media-select" data-media-url="${escapeHTML(url)}" data-media-name="${escapeHTML(name)}" data-media-kind="video" data-media-mime="${escapeHTML(file?.mime || (/\.webm$/i.test(name) ? "video/webm" : "video/mp4"))}"><strong>${escapeHTML(name)}</strong><span data-media-duration>Metadata pending</span><small data-media-size>${escapeHTML(metadata)}</small></button></article>`;
  }
  return `<button type="button" class="media-card media-select" data-media-url="${escapeHTML(url)}" data-media-name="${escapeHTML(name)}" data-media-kind="image"><img src="${escapeHTML(url)}" alt=""><span>${escapeHTML(name)}</span><small>${escapeHTML(metadata)}</small></button>`;
}

async function insertYouTube(url) {
  const id = youtubeVideoIDFromURL(url);
  const marker = youtubeMarkerFragment(id);
  if (!marker) throw new Error("YouTube video ID is invalid");
  await state.editor.flush();
  const source = sourceOrEditorHTML();
  const selected = selectedYouTubeBlock();
  const next = selected
    ? replaceSelection(source, marker, state.selected, "[data-fileloom-youtube]")
    : insertAfterSelection(source, marker, state.selected);
  if (selected && next === source) throw new Error("Could not resolve the selected YouTube video");
  await applySourceHTML(next);
  closeSheet();
  setStatus(selected ? "YouTube video replaced" : state.selected ? "YouTube video added after selection" : "YouTube video added", "dirty");
}

async function submitYouTubeForm(form) {
  const input = form.elements.url;
  try {
    await insertYouTube(input.value);
  } catch (error) {
    const message = error instanceof Error ? error.message : "YouTube URL is invalid";
    input.setCustomValidity(message);
    input.reportValidity();
    const help = form.querySelector("[data-youtube-error]");
    if (help) help.textContent = message;
    setStatus(message, "error");
  }
}

function flattenMedia(node, result = []) {
  if (Array.isArray(node)) {
    node.forEach((child) => flattenMedia(child, result));
    return result;
  }
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
  const selectedReplacement = selectedImageBlock() ? "image" : selectedVideoBlock() ? "video" : selectedYouTubeBlock() ? "youtube" : "";
  const heading = selectedReplacement === "image" ? "Replace selected image" : selectedReplacement === "video" ? "Replace selected video" : selectedReplacement === "youtube" ? "Replace selected YouTube video" : "Media library";
  const youtubeAction = selectedReplacement === "youtube" ? "Replace YouTube video" : "Insert YouTube video";
  openSheet("media");
  const content = document.querySelector("#sheet-content");
  content.innerHTML = '<div class="sheet-loading">Loading media…</div>';
  try {
    const tree = await fetchJSON("/_cms/api/media");
    const files = flattenMedia(tree).filter((file) => ["image", "video"].includes(mediaFileKind(file)));
    content.innerHTML = `
      <div class="sheet-heading"><div><span class="eyebrow">${escapeHTML(heading)}</span><h2>Media</h2><p class="sheet-subtitle">Choose a local image or video, or add a privacy-friendly YouTube link.</p></div><label class="upload-button">Upload<input id="media-upload" type="file" accept="image/*,video/mp4,video/webm" multiple></label></div>
      <div id="media-dropzone" class="media-dropzone" role="button" aria-controls="media-upload" aria-label="Upload images or MP4/WebM videos" tabindex="0"><strong>Drop images or MP4/WebM videos here</strong><span>or tap to choose files</span></div>
      <section class="media-library-section"><div class="media-section-heading"><div><span class="eyebrow">Local files</span><h3>Images &amp; videos</h3></div><small>${files.length} file${files.length === 1 ? "" : "s"}</small></div><div class="media-grid">${files.length ? files.map((file) => mediaCardHTML(file)).join("") : '<p class="empty-state">No images or videos yet.</p>'}</div></section>
      <section class="youtube-insert-section"><div class="media-section-heading"><div><span class="eyebrow">External video</span><h3>YouTube</h3></div><span class="youtube-privacy-note">Loads only after activation</span></div><form id="youtube-form" class="youtube-form" novalidate><label class="form-field"><span>YouTube URL</span><input name="url" type="url" inputmode="url" autocomplete="url" required placeholder="https://youtu.be/…" aria-describedby="youtube-help youtube-error"></label><p id="youtube-help" class="youtube-help">HTTPS watch, shorts, embed, and youtu.be URLs are accepted. Paste a URL only—not iframe code.</p><p id="youtube-error" class="youtube-error" data-youtube-error role="alert"></p><div class="details-actions"><button type="submit" class="primary-button">${escapeHTML(youtubeAction)}</button></div></form></section>`;
    const input = content.querySelector("#media-upload");
    const zone = content.querySelector("#media-dropzone");
    const youtubeInput = content.querySelector('#youtube-form [name="url"]');
    const youtubeError = content.querySelector("[data-youtube-error]");
    const chooseFiles = async (filesToUpload) => {
      try { await uploadMediaFiles(filesToUpload); } catch (error) { setStatus(error.message, "error"); }
    };
    input?.addEventListener("change", async (event) => chooseFiles(event.target.files));
    zone?.addEventListener("click", () => input?.click());
    zone?.addEventListener("keydown", (event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); input?.click(); } });
    ["dragenter", "dragover"].forEach((type) => zone?.addEventListener(type, (event) => { event.preventDefault(); zone.classList.add("is-dragging"); }));
    ["dragleave", "drop"].forEach((type) => zone?.addEventListener(type, (event) => { event.preventDefault(); zone.classList.remove("is-dragging"); }));
    zone?.addEventListener("drop", async (event) => chooseFiles(event.dataTransfer?.files));
    youtubeInput?.addEventListener("input", () => {
      youtubeInput.setCustomValidity("");
      if (youtubeError) youtubeError.textContent = "";
    });
    content.querySelectorAll(".media-card-video video").forEach((video) => {
        video.addEventListener("loadedmetadata", () => {
          const card = video.closest(".media-card");
          const duration = card?.querySelector("[data-media-duration]");
          const size = card?.querySelector("[data-media-size]")?.textContent || "";
          if (duration) duration.textContent = `${formatMediaDuration(video.duration)}${size ? ` · ${size}` : ""}`;
          if (video.duration === Infinity && duration) duration.textContent = `Live video${size ? ` · ${size}` : ""}`;
        });
      });
  } catch (error) {
    content.innerHTML = `<p class="error-card">${escapeHTML(error.message)}</p>`;
  }
}

async function insertImage(url, name) {
  const replacingImage = Boolean(selectedImageBlock());
  const hadSelection = Boolean(state.selected);
  const fragment = `<figure class="fileloom-image"><img src="${escapeHTML(url)}" alt="${escapeHTML(name)}"><figcaption>${escapeHTML(name)}</figcaption></figure>`;
  await state.editor.flush();
  const source = sourceOrEditorHTML();
  const selected = selectedImageBlock();
  const selector = selected?.tagName === "FIGURE" ? "figure.fileloom-image" : "img";
  const next = replacingImage ? replaceSelection(source, fragment, state.selected, selector) : insertAfterSelection(source, fragment, state.selected);
  if (next === source && replacingImage) {
    setStatus("Could not resolve the selected image", "error");
    return;
  }
  await applySourceHTML(next);
  closeSheet();
  setStatus(replacingImage ? "Image replaced" : hadSelection ? "Image added after selection" : "Image added", "dirty");
}

async function insertVideo(url, name, mime = "") {
  const replacingVideo = Boolean(selectedVideoBlock());
  const hadSelection = Boolean(state.selected);
  const normalizedMime = mime || (/\.webm$/i.test(name) ? "video/webm" : "video/mp4");
  const fragment = `<figure class="fileloom-video" data-fileloom-video data-file-name="${escapeHTML(name)}" data-media-mime="${escapeHTML(normalizedMime)}"><video src="${escapeHTML(url)}" controls preload="metadata" playsinline aria-label="${escapeHTML(name)}">${escapeHTML(name)} could not be played by this browser.</video><figcaption>${escapeHTML(name)}</figcaption><a class="fileloom-video-fallback" href="${escapeHTML(url)}" download>Download ${escapeHTML(name)}</a></figure>`;
  await state.editor.flush();
  const source = sourceOrEditorHTML();
  const selected = selectedVideoBlock();
  const selector = selected?.tagName === "FIGURE" ? "figure.fileloom-video" : "video";
  const next = replacingVideo ? replaceSelection(source, fragment, state.selected, selector) : insertAfterSelection(source, fragment, state.selected);
  if (next === source && replacingVideo) {
    setStatus("Could not resolve the selected video", "error");
    return;
  }
  await applySourceHTML(next);
  closeSheet();
  setStatus(replacingVideo ? "Video replaced" : hadSelection ? "Video added after selection" : "Video added", "dirty");
}


function openSheet(kind) {
  if (state.sheet === "source" && kind !== "source") destroySourceEditor();
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
  if (state.sheet === "source") destroySourceEditor();
  state.sheet = null;
  state.codeEdit = null;
  const backdrop = document.querySelector("#sheet-backdrop");
  const sheet = document.querySelector("#editor-sheet");
  if (backdrop) backdrop.hidden = true;
  if (sheet) sheet.hidden = true;
}

function blockCatalogHTML() {
  return `<div class="sheet-heading"><div><span class="eyebrow">Insert</span><h2>Choose a block</h2></div><button class="icon-button" data-action="close-sheet" aria-label="Close">×</button></div><div class="block-grid">${[
    ["heading", "Heading", "Aa"], ["paragraph", "Paragraph", "¶"], ["quote", "Quote", "❞"], ["list", "List", "☷"], ["code", "Code", "<>"], ["divider", "Divider", "—"], ["link", "Link", "↗"], ["media", "Media", "▧"],
  ].map(([type, label, icon]) => `<button class="block-card" data-block="${type}"><span>${icon}</span><strong>${label}</strong><small>Tap to add</small></button>`).join("")}</div>`;
}

function themeDetailsSheetHTML() {
  return `<div class="sheet-heading"><div><span class="eyebrow">Theme layout</span><h2>${escapeHTML(state.record?.theme || "Theme")}</h2></div><button class="icon-button" data-action="close-sheet" aria-label="Close">×</button></div>${selectionOperationsHTML()}<div class="details-actions"><button type="button" class="secondary-button" data-action="history">History</button></div><div class="info-card details-source"><span class="eyebrow">Source</span><strong>${escapeHTML(state.record?.path || "")}</strong><small>Template placeholders are protected while editing. Use Style tokens for CSS variables.</small></div>`;
}

function detailsSheetHTML() {
  if (isThemeResource()) return themeDetailsSheetHTML();
  const metadata = metadataPayload();
  const scheduled = metadata.status === "scheduled";
  const commentsSiteEnabled = Boolean(state.record?.comments?.enabled);
  const codeAction = selectedCodeBlock() ? '<button type="button" class="secondary-button" data-action="edit-code">Edit code</button>' : "";
  const sourceAction = '<button type="button" class="secondary-button" data-action="source">HTML source</button>';
  const pageCommentsEnabled = metadata.comments !== false;
  const commentsField = `<label class="comments-toggle"><input data-metadata="comments" type="checkbox" ${commentsSiteEnabled && pageCommentsEnabled ? "checked" : ""} ${commentsSiteEnabled ? "" : "disabled"}><span><strong>Allow comments on this page</strong><small>${commentsSiteEnabled ? "Visitors can discuss this page through Artalk." : "Enable Artalk comments in the CMS site settings first."}</small></span></label>`;
  return `<div class="sheet-heading"><div><span class="eyebrow">Page details</span><h2>Metadata & status</h2></div><button class="icon-button" data-action="close-sheet" aria-label="Close">×</button></div>${selectionOperationsHTML()}
    <form id="details-form" class="details-form">
      <label class="form-field"><span>Title</span><input data-metadata="title" value="${escapeHTML(metadata.title)}" required></label>
      <div class="details-grid"><label class="form-field"><span>Date</span><input data-metadata="date" type="date" value="${escapeHTML(metadata.date)}"></label><label class="form-field"><span>Status</span><select data-metadata="status"><option value="draft" ${metadata.status === "draft" ? "selected" : ""}>Draft</option><option value="private" ${metadata.status === "private" ? "selected" : ""}>Private</option><option value="published" ${metadata.status === "published" ? "selected" : ""}>Published</option><option value="scheduled" ${metadata.status === "scheduled" ? "selected" : ""}>Scheduled</option></select></label></div>
      <label class="form-field" data-publish-at-field ${scheduled ? "" : "hidden"}><span>Publish at</span><input data-metadata="publish_at" type="datetime-local" value="${escapeHTML(localDateTimeValue(metadata.publish_at))}"><small>Stored in UTC after saving.</small></label>
      ${commentsField}
      <div class="details-grid"><label class="form-field"><span>Tags</span><input data-metadata="tags" value="${escapeHTML(metadata.tags.join(", "))}" placeholder="design, notes"></label><label class="form-field"><span>Category</span><input data-metadata="category" value="${escapeHTML(metadata.category)}"></label></div>
      <label class="form-field"><span>Excerpt</span><textarea data-metadata="excerpt" rows="3">${escapeHTML(metadata.excerpt)}</textarea></label>
      <div class="details-actions">${codeAction}${sourceAction}<button type="button" class="secondary-button" data-action="history">History</button><button type="button" class="primary-button" data-action="save">Save details</button></div>
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
  state.metadata[name] = name === "tags" ? parseTagsInput(field.value) : field.type === "checkbox" ? field.checked : field.value;
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
  const baseBody = baseBodyHTML();
  const localBody = localBodyHTML();
  const remoteBody = rawBodyHTMLForRecord(remote);
  const localChanged = normalizedBodyHTML(localBody) !== normalizedBodyHTML(baseBody);
  const remoteChanged = normalizedBodyHTML(remoteBody) !== normalizedBodyHTML(baseBody);
  const bodyConflict = localChanged && remoteChanged && normalizedBodyHTML(localBody) !== normalizedBodyHTML(remoteBody);
  const fields = merge.conflicts.length ? `<p class="conflict-fields"><strong>Metadata conflicts:</strong> ${escapeHTML(merge.conflicts.join(", "))}</p>` : `<p class="conflict-fields">Metadata changes can be merged safely.</p>`;
  const mergeDisabled = bodyConflict || merge.conflicts.length > 0;
  return `<div class="sheet-heading"><div><span class="eyebrow">Save conflict</span><h2>Review newer source</h2></div><button class="icon-button" data-action="close-sheet" aria-label="Close">×</button></div>
    <div class="info-card conflict-card"><p>The source changed after this editor opened. Choose deliberately; nothing has been overwritten yet.</p><small>Remote source: ${escapeHTML(String(remote.source_sha256 || conflict.current_sha256 || "").slice(0, 12))}…</small>${fields}</div>
    <div class="conflict-diff"><article><span class="eyebrow">Your local body</span><p>${escapeHTML(textSummary(localBody))}</p><small>${localChanged ? "Changed locally" : "Unchanged locally"}</small></article><article><span class="eyebrow">Remote body</span><p>${escapeHTML(textSummary(remoteBody))}</p><small>${remoteChanged ? "Changed remotely" : "Unchanged remotely"}</small></article></div>
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
    const url = state.conflict.reload_url || editorRequestURL();
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
  state.baseBodyHTML = rawBodyHTMLForRecord(remote);
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
  const baseBody = baseBodyHTML();
  const localBody = localBodyHTML();
  const remoteBody = rawBodyHTMLForRecord(remote);
  const localChanged = normalizedBodyHTML(localBody) !== normalizedBodyHTML(baseBody);
  const remoteChanged = normalizedBodyHTML(remoteBody) !== normalizedBodyHTML(baseBody);
  if (localChanged && remoteChanged && normalizedBodyHTML(localBody) !== normalizedBodyHTML(remoteBody)) return;
  if (!localChanged && remoteChanged) {
    await replaceEditorHTML(editorDocument(remote), { sourceBodyHTML: isThemeResource() ? undefined : remoteBody });
    state.currentHTML = state.editor.getHtml();
  }
  state.record = remote;
  state.baseDocument = metadataFromDocument(remote.document);
  state.baseHTML = remote.html || "";
  state.baseBodyHTML = remoteBody;
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
  const sourceAction = isThemeResource(record) ? "" : '<button class="secondary-button desktop-only" data-action="source" title="Edit the HTML source">HTML source</button>';
  const inspectorSourceAction = isThemeResource(record) ? "" : '<button class="secondary-button" data-action="source">HTML source</button>';
  app.innerHTML = `
    <div class="editor-shell">
      <header class="editor-topbar">
        <div class="topbar-leading"><button class="back-button" data-action="back" aria-label="Back to CMS">←<span class="desktop-only"> CMS</span></button><div class="brand-lockup"><span class="brand-mark">F</span><span class="desktop-only">FILELOOM</span></div></div>
        <div class="document-heading"><span class="eyebrow">Visual editor</span><strong title="${title}">${title}</strong><span class="dirty-dot" data-dirty-indicator hidden></span></div>
        <div class="topbar-actions"><span id="editor-status" data-kind="neutral">Ready</span><button class="icon-button desktop-only" data-action="undo" aria-label="Undo">↶</button><button class="icon-button desktop-only" data-action="redo" aria-label="Redo">↷</button><button class="secondary-button desktop-only" data-action="preview">Preview</button>${sourceAction}<button class="primary-button" data-action="save" disabled>Save</button><button class="icon-button mobile-only" data-action="more" aria-label="More">•••</button></div>
      </header>
      <main class="editor-workspace">
        <aside class="desktop-panel desktop-blocks"><div class="panel-heading"><div><span class="eyebrow">Add</span><h2>Blocks</h2></div></div>${blockCatalogHTML()}</aside>
        <section class="canvas-region"><div id="deckflow-canvas" aria-label="Editable page canvas"></div><div class="canvas-hint">Tap text to edit · Select an element for controls</div></section>
        <aside class="desktop-panel desktop-inspector"><div class="panel-heading"><div><span class="eyebrow">Inspect</span><h2>Selection</h2></div></div><div id="selection-info" class="selection-info"><strong>Nothing selected</strong><p>Choose an element in the canvas to edit it.</p></div><div class="inspector-divider"></div><div class="quick-actions inspector-actions"><button class="secondary-button" data-action="details">Details</button>${inspectorSourceAction}<button class="secondary-button" data-action="history">History</button></div><div class="info-card"><span class="eyebrow">Source</span><strong>${escapeHTML(record.path)}</strong><small data-document-summary>${escapeHTML(record.theme || "default")} theme · ${escapeHTML(record.document?.status || "published")} · HTML-first</small></div></aside>
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
    const html = sourceHTMLForSave(await state.editor.flush());
    const versionAtStart = state.changeVersion;
    const metadata = isThemeResource() ? undefined : metadataPayload();
    const bodyHTML = isThemeResource() ? undefined : state.sourceBodyHTML;
    const payload = await fetchJSON("/_cms/api/editor-save", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        resource: state.resource,
        theme: isThemeResource() ? state.record.theme : undefined,
        path: state.record.path,
        html: isThemeResource() ? html : undefined,
        body_html: isThemeResource() ? undefined : bodyHTML,
        metadata,
        base_sha256: state.record.source_sha256,
        engine: "deckflow",
      }),
    });
    const hasNewerEdits = state.changeVersion !== versionAtStart;
    state.record.source_sha256 = payload.source_sha256;
    state.record.document = payload.document || state.record.document;
    if (payload.document && Object.prototype.hasOwnProperty.call(payload.document, "html")) state.record.html = payload.document.html;
    else if (Object.prototype.hasOwnProperty.call(payload, "html")) state.record.html = payload.html;
    if (!isThemeResource() && typeof payload.body_html === "string") state.record.body_html = payload.body_html;
    state.baseDocument = metadataFromDocument(state.record.document);
    state.baseHTML = state.record.html || "";
    state.baseBodyHTML = isThemeResource() ? state.baseHTML : rawBodyHTMLForRecord(state.record);
    state.conflict = null;
    updateDocumentHeading();
    if (!hasNewerEdits) {
      if (!isThemeResource() && typeof payload.body_html === "string") state.sourceBodyHTML = payload.body_html;
      state.currentHTML = html;
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
  const fresh = await fetchJSON(editorRequestURL());
  state.record = fresh;
  state.resource = fresh.resource || state.resource;
  state.path = fresh.path || state.path;
  state.currentHTML = editorDocument(fresh);
  state.sourceBodyHTML = rawBodyHTMLForRecord(fresh);
  state.baseHTML = fresh.html || "";
  state.baseBodyHTML = state.sourceBodyHTML;
  state.baseDocument = metadataFromDocument(fresh.document);
  state.metadata = metadataFromDocument(fresh.document);
  state.hostUndo = [];
  state.hostRedo = [];
  state.conflict = null;
  state.changeVersion += 1;
  await replaceEditorHTML(state.currentHTML, { sourceBodyHTML: isThemeResource(fresh) ? undefined : state.sourceBodyHTML });
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
    const html = sourceHTMLForSave(await state.editor.flush());
    const response = await fetch("/_cms/api/editor-preview", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        resource: state.resource,
        theme: isThemeResource() ? state.record.theme : undefined,
        path: state.record.path,
        html: isThemeResource() ? html : undefined,
        body_html: isThemeResource() ? undefined : state.sourceBodyHTML,
        metadata: isThemeResource() ? undefined : metadataPayload(),
      }),
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
    case "source":
      await openSourceSheet();
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
    if (media.dataset.mediaKind === "video") await insertVideo(media.dataset.mediaUrl, media.dataset.mediaName || "video", media.dataset.mediaMime || "");
    else await insertImage(media.dataset.mediaUrl, media.dataset.mediaName || "image");
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
  if (event.target.closest("#code-form [name=code]")) updateCodeSummary(event.target.form);
});

app.addEventListener("change", (event) => {
  const field = event.target.closest("#details-form [data-metadata]");
  if (field) updateMetadataDraft(field);
  const wrapToggle = event.target.closest("#source-form [data-source-wrap-lines]");
  if (wrapToggle) setSourceWrapPreference(wrapToggle.checked);
});

app.addEventListener("keydown", (event) => {
  if ((event.metaKey || event.ctrlKey) && event.key === "Enter" && event.target.closest("#source-form .cm-content")) {
    event.preventDefault();
    event.target.closest("#source-form")?.requestSubmit();
    return;
  }
  const textarea = event.target.closest("#code-form textarea[name=code]");
  if (!textarea) return;
  if (event.key === "Tab" && !event.shiftKey) {
    event.preventDefault();
    const start = textarea.selectionStart;
    const end = textarea.selectionEnd;
    textarea.setRangeText("  ", start, end, "end");
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
    return;
  }
  if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
    event.preventDefault();
    textarea.form?.requestSubmit();
  }
});

app.addEventListener("submit", async (event) => {
  const codeForm = event.target.closest("#code-form");
  const sourceForm = event.target.closest("#source-form");
  const inspectorForm = event.target.closest("#element-inspector-form");
  const youtubeForm = event.target.closest("#youtube-form");
  if (!codeForm && !sourceForm && !inspectorForm && !youtubeForm) return;
  event.preventDefault();
  if (codeForm) await submitCodeForm(codeForm);
  else if (sourceForm) await submitSourceForm(sourceForm);
  else if (inspectorForm) await submitElementInspector(inspectorForm);
  else await submitYouTubeForm(youtubeForm);
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
  if (!path && !themeParam) throw new Error("No content or theme was provided");
  state.record = await fetchJSON(editorRequestURL());
  state.resource = state.record.resource || (themeParam ? "theme-layout" : "content");
  state.path = state.record.path || path;
  state.sourceBodyHTML = rawBodyHTMLForRecord(state.record);
  state.baseHTML = state.record.html || "";
  state.baseBodyHTML = state.sourceBodyHTML;
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
    onChange: ({ html, patches, reason }) => {
      const previous = state.currentHTML;
      const previousBodyHTML = state.sourceBodyHTML;
      state.currentHTML = html;
      if (!isThemeResource() && state.sourceBodyOverride === undefined) {
        state.sourceBodyHTML = sourceBodyAfterDeckflowChange(previousBodyHTML, html, patches);
      }
      if (reason !== "undo" && reason !== "redo") recordHostChange(previous, html, previousBodyHTML);
      state.changeVersion += 1;
      setDirty(true);
    },
    onSelectionChange: updateSelection,
    onError: (error) => setStatus(error.message, "error"),
  });
  await state.editor.ready;
  installEditorKeyboardShortcuts();
  setStatus("Ready", "success");
}

start().catch((error) => {
  app.innerHTML = `<main class="fatal-error"><span class="eyebrow">Fileloom editor</span><h1>Editor could not load</h1><p>${escapeHTML(error.message)}</p><button class="primary-button" data-action="back">Back to CMS</button></main>`;
});
