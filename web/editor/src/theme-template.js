const tokenPattern = /\{\{[^{}]*\}\}/g;
const rawTextTags = new Set(["script", "style", "title", "textarea"]);

function escapeHTML(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

function markerID(index) {
  return `fileloom-template-${index}`;
}

function inComment(source, index) {
  const commentStart = source.lastIndexOf("<!--", index);
  const commentEnd = source.lastIndexOf("-->", index);
  return commentStart > commentEnd;
}

function inTag(source, index) {
  const tagStart = source.lastIndexOf("<", index);
  const tagEnd = source.lastIndexOf(">", index);
  return tagStart > tagEnd;
}

function inRawTextElement(source, index) {
  const prefix = source.slice(0, index);
  let active = "";
  const pattern = /<\/?([A-Za-z][A-Za-z0-9-]*)\b[^>]*>/g;
  let match;
  while ((match = pattern.exec(prefix))) {
    const tag = match[1].toLowerCase();
    if (!rawTextTags.has(tag)) continue;
    if (match[0][1] === "/") {
      if (active === tag) active = "";
    } else {
      active = tag;
    }
  }
  return Boolean(active);
}

function tokenKind(source, index) {
  if (inComment(source, index) || inTag(source, index) || inRawTextElement(source, index)) return "sentinel";
  return "text";
}

function projectedSentinel(source, index, sentinel) {
  const tagStart = source.lastIndexOf("<", index);
  const tag = source.slice(Math.max(0, tagStart), index);
  if (/^<link\b/i.test(tag) && /\bhref\s*=\s*["'][^"']*$/i.test(tag)) {
    return `data:text/css,/*${sentinel}*/`;
  }
  return sentinel;
}

export function shieldThemeTemplate(source) {
  const input = String(source ?? "");
  const tokens = [];
  const replacements = [];
  for (const match of input.matchAll(tokenPattern)) {
    const raw = match[0];
    const index = match.index ?? 0;
    const id = markerID(tokens.length);
    const kind = tokenKind(input, index);
    const sentinel = `__FILELOOM_TEMPLATE_${tokens.length}__`;
    const projected = kind === "sentinel" ? projectedSentinel(input, index, sentinel) : sentinel;
    tokens.push({ id, kind, raw, sentinel, projected });
    replacements.push({
      start: index,
      end: index + raw.length,
      value: kind === "text"
        ? `<span class="fileloom-template-slot" data-fileloom-template-token="${id}" contenteditable="false">${escapeHTML(raw)}</span>`
        : projected,
    });
  }
  let html = input;
  for (const replacement of replacements.reverse()) {
    html = `${html.slice(0, replacement.start)}${replacement.value}${html.slice(replacement.end)}`;
  }
  return { html, tokens };
}

function countOccurrences(source, value) {
  let count = 0;
  let cursor = 0;
  while (true) {
    const index = source.indexOf(value, cursor);
    if (index < 0) return count;
    count += 1;
    cursor = index + value.length;
  }
}

function markerPattern(id) {
  const escaped = id.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return new RegExp(`<span\\b(?=[^>]*\\bdata-fileloom-template-token=["']${escaped}["'])[^>]*>[\\s\\S]*?<\\/span>`, "gi");
}

export function unshieldThemeTemplate(projected, tokens) {
  let html = String(projected ?? "");
  for (const token of tokens || []) {
    if (token.kind === "text") {
      const pattern = markerPattern(token.id);
      const matches = html.match(pattern) || [];
      if (matches.length !== 1) throw new Error(`Template slot ${token.raw} was changed or removed`);
      html = html.replace(pattern, token.raw);
    } else {
      const projectedValue = token.projected || token.sentinel;
      const count = countOccurrences(html, projectedValue);
      if (count !== 1) throw new Error(`Template slot ${token.raw} was changed or removed`);
      html = html.replace(projectedValue, token.raw);
    }
  }
  if (html.includes("data-fileloom-template-token=") || html.includes("__FILELOOM_TEMPLATE_")) {
    throw new Error("The theme template contains unresolved protected slots");
  }
  return html;
}

export function injectThemePreviewStyles(source, css) {
  const themeStyle = `<style data-fileloom-theme-preview>${String(css || "").replace(/<\/style/gi, "<\\/style")}</style>`;
  const slotStyle = `<style data-fileloom-theme-slots>.fileloom-template-slot{display:inline-block;padding:.08em .35em;border:1px dashed #6347ee;border-radius:.35em;color:#5037c9;background:#f0edff;font:600 .78em/1.35 ui-monospace,SFMono-Regular,Menlo,monospace;text-transform:none;white-space:nowrap}.fileloom-template-slot::before{content:"Template slot ";opacity:.62}</style>`;
  const codeStyle = `<link rel="stylesheet" href="/_cms/assets/fileloom-code.css" data-fileloom-editor-code>`;
  const style = codeStyle + themeStyle + slotStyle;
  const lower = String(source || "").toLowerCase();
  const headEnd = lower.indexOf("</head>");
  if (headEnd >= 0) return `${source.slice(0, headEnd)}${style}${source.slice(headEnd)}`;
  return `${style}${source}`;
}

export function stripThemePreviewStyles(source) {
  return String(source || "")
    .replace(/<link\b[^>]*data-fileloom-editor-code[^>]*>\s*/gi, "")
    .replace(/<style\b[^>]*data-fileloom-theme-(?:preview|slots)[^>]*>[\s\S]*?<\/style>\s*/gi, "");
}
