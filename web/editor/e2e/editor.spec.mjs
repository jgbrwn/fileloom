import { test, expect } from "@playwright/test";

const editorURL = "/_cms/editor?path=pages%2Fabout.html";
const themeEditorURL = "/_cms/editor?theme=brutalist";
const e2eBaseURL = process.env.FILELOOM_E2E_URL || "http://127.0.0.1:8010";
const e2eOwnerEmail = process.env.FILELOOM_E2E_EMAIL || "owner@example.com";

async function openEditor(page) {
  await page.goto(editorURL);
  await expect(page.locator(".editor-shell")).toBeVisible();
  const mobile = await page.locator(".mobile-nav").isVisible();
  if (mobile) await expect(page.locator(".mobile-nav")).toBeVisible();
  else await expect(page.locator(".desktop-blocks .block-grid")).toBeVisible();
}

async function openThemeEditor(page) {
  await page.goto(themeEditorURL);
  await expect(page.locator(".editor-shell")).toBeVisible();
  const mobile = await page.locator(".mobile-nav").isVisible();
  if (mobile) await expect(page.locator(".mobile-nav")).toBeVisible();
  else await expect(page.locator(".desktop-blocks .block-grid")).toBeVisible();
}

async function cmsJSON(page, url, init = {}) {
  const { body: requestBody, ...requestInit } = init;
  const response = await page.request.fetch(new URL(url, e2eBaseURL).toString(), {
    ...requestInit,
    timeout: requestInit.timeout ?? 15000,
    ...(requestBody === undefined ? {} : { data: requestBody }),
    headers: { "X-ExeDev-Email": e2eOwnerEmail, ...(init.headers || {}) },
  });
  const text = await response.text();
  let payload = text;
  try { payload = JSON.parse(text); } catch {}
  return { status: response.status(), body: payload, etag: response.headers()["etag"] || null };
}

async function getEditorDocument(page) {
  const response = await cmsJSON(page, "/_cms/api/editor?path=pages%2Fabout.html&engine=deckflow");
  expect(response.status).toBe(200);
  return response.body;
}

async function getThemeDocument(page) {
  const response = await cmsJSON(page, "/_cms/api/editor?theme=brutalist&engine=deckflow");
  expect(response.status).toBe(200);
  return response.body;
}

async function restoreThemeDocument(page, original) {
  const current = await getThemeDocument(page);
  if (current.source_sha256 === original.source_sha256) return;
  const response = await cmsJSON(page, "/_cms/api/editor-save", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      resource: "theme-layout",
      theme: "brutalist",
      path: original.path,
      html: original.html,
      base_sha256: current.source_sha256,
      engine: "deckflow",
    }),
  });
  expect(response.status, JSON.stringify(response.body)).toBe(200);
}

function editorMetadata(document) {
  return {
    title: document.title,
    tags: document.tags || [],
    category: document.category || "",
    excerpt: document.excerpt || "",
    status: document.status || "published",
    publish_at: document.publish_at || "",
  };
}

async function restoreEditorDocument(page, original) {
  const current = await getEditorDocument(page);
  if (current.source_sha256 === original.source_sha256) return;
  const response = await cmsJSON(page, "/_cms/api/editor-save", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      path: original.path,
      engine: "deckflow",
      html: original.html,
      metadata: editorMetadata(original.document),
      base_sha256: current.source_sha256,
    }),
  });
  expect(response.status, JSON.stringify(response.body)).toBe(200);
}

async function insertParagraphBlock(page) {
  const mobile = await page.locator(".mobile-nav").isVisible();
  if (mobile) await page.locator('[data-action="blocks"]:visible').click();
  const catalog = page.locator(mobile ? '#sheet-content .block-grid [data-block="paragraph"]' : '.desktop-blocks .block-grid [data-block="paragraph"]');
  await catalog.click();
}
async function openDetails(page) {
  await page.locator('[data-action="details"]:visible').first().click();
  await expect(page.locator("#details-form")).toBeVisible();
}

async function focusTextEnd(page, locator) {
  await locator.click();
  const alreadyEditing = (await locator.getAttribute("data-local-editor-editing")) === "true";
  await page.locator("iframe.deckflow-html-editor__preview").focus();
  if (!alreadyEditing) await page.keyboard.press("Enter");
  await locator.evaluate((element) => {
    const selection = element.ownerDocument.defaultView.getSelection();
    const range = element.ownerDocument.createRange();
    range.selectNodeContents(element);
    range.collapse(false);
    selection?.removeAllRanges();
    selection?.addRange(range);
    element.focus();
  });
  await page.locator("iframe.deckflow-html-editor__preview").focus();
}

async function selectTextRange(locator, start, end) {
  await locator.click();
  await locator.evaluate((element, offsets) => {
    const document = element.ownerDocument;
    const walker = document.createTreeWalker(element, document.defaultView.NodeFilter.SHOW_TEXT);
    const nodes = [];
    while (walker.nextNode()) nodes.push(walker.currentNode);
    let cursor = 0;
    let startPoint = null;
    let endPoint = null;
    for (const node of nodes) {
      const next = cursor + node.data.length;
      if (!startPoint && offsets.start <= next) startPoint = { node, offset: Math.max(0, offsets.start - cursor) };
      if (!endPoint && offsets.end <= next) {
        endPoint = { node, offset: Math.max(0, offsets.end - cursor) };
        break;
      }
      cursor = next;
    }
    if (!startPoint || !endPoint) return;
    const range = document.createRange();
    range.setStart(startPoint.node, startPoint.offset);
    range.setEnd(endPoint.node, endPoint.offset);
    const selection = document.defaultView.getSelection();
    selection?.removeAllRanges();
    selection?.addRange(range);
    document.dispatchEvent(new Event("selectionchange"));
  }, { start, end });
}

test.describe("Deckflow Fileloom editor", () => {
  test("keeps comments opt-in and disables the page control until site setup", async ({ page }) => {
    await page.goto("/_cms/");
    await expect(page.locator("#comments-status-pill")).toHaveText("Off");
    await page.locator("#comments-configure").click();
    await expect(page.locator("#comments-dialog")).toBeVisible();
    await expect(page.locator("#comments-enabled")).not.toBeChecked();
    await page.locator("#comments-cancel").click();

    await openEditor(page);
    await openDetails(page);
    await expect(page.locator('[data-metadata="comments"]')).toBeDisabled();
    await expect(page.locator("#sheet-content")).toContainText("Enable Artalk comments in the CMS site settings first");
  });

  test("explains local Artalk setup without putting loopback in the public server field", async ({ page }) => {
    await page.goto("/_cms/");
    await page.locator("#comments-local-setup").click();
    await expect(page.locator("#comments-install-dialog")).toBeVisible();
    await expect(page.locator("#comments-install-command")).toContainText("scripts/install-artalk.sh");
    await page.locator("#comments-install-done").click();
    await page.locator("#comments-configure").click();
    await page.locator("#comments-mode").selectOption("local");
    await expect(page.locator("#comments-server")).toHaveValue("/_fileloom/artalk");
    await expect(page.locator("#comments-server")).toBeDisabled();
    await expect(page.locator("#comments-local-port")).toHaveValue("23366");
    await page.locator("#comments-cancel").click();
  });

  test("renders Artalk only after site opt-in and removes it when disabled", async ({ page }) => {
    await page.route("https://comments.example.test/**", (route) => route.abort());
    const enabled = await cmsJSON(page, "/_cms/api/comments/config", {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: "enabled=true&provider=artalk&server=https%3A%2F%2Fcomments.example.test&site=fileloom-e2e",
    });
    expect(enabled.status, JSON.stringify(enabled.body)).toBe(200);
    await page.goto("/2026/welcome-to-fileloom/");
    await expect(page.locator("[data-fileloom-comments]")).toBeVisible();
    await expect(page.locator(".fileloom-comments .artalk")).toBeVisible();

    const disabled = await cmsJSON(page, "/_cms/api/comments/config", {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: "enabled=false&provider=artalk&server=https%3A%2F%2Fcomments.example.test&site=fileloom-e2e",
    });
    expect(disabled.status, JSON.stringify(disabled.body)).toBe(200);
    await page.goto("/2026/welcome-to-fileloom/");
    await expect(page.locator("[data-fileloom-comments]")).toHaveCount(0);
  });

  test("loads the editor shell and keeps theme controls separate", async ({ page }) => {
    await openEditor(page);
    await expect(page.locator(".document-heading strong")).toHaveText("About");
    await expect(page.locator(".canvas-hint")).toContainText("Tap text to edit");
    await openDetails(page);
    await expect(page.locator("#details-form [data-metadata=title]")).toHaveValue("About");
    await expect(page.locator("[data-publish-at-field]")).toBeHidden();
    await page.locator('[data-metadata="status"]').selectOption("scheduled");
    await expect(page.locator('[data-publish-at-field]')).toBeVisible();
    await page.locator('#sheet-content [data-action="close-sheet"]').click();
    if (await page.locator(".mobile-nav").isVisible()) {
      await page.locator('[data-action="style"]').click();
      await expect(page.locator("#sheet-content")).toContainText("Theme controls stay in Fileloom");
    } else {
      await expect(page.locator(".desktop-inspector")).toContainText("theme");
    }
  });

  test("keeps the canvas on one scroll surface", async ({ page }) => {
    await openEditor(page);
    const scrollState = await page.locator(".deckflow-html-editor__viewport").evaluate((viewport) => {
      const iframe = viewport.querySelector("iframe");
      const previewDocument = iframe?.contentDocument;
      return {
        viewportOverflowY: getComputedStyle(viewport).overflowY,
        iframeHeight: iframe?.clientHeight || 0,
        previewHeight: Math.max(previewDocument?.documentElement?.scrollHeight || 0, previewDocument?.body?.scrollHeight || 0),
      };
    });
    expect(scrollState.viewportOverflowY).toBe("hidden");
    expect(scrollState.previewHeight).toBeGreaterThanOrEqual(scrollState.iframeHeight);
  });
  test("loads Deckflow with only the production editor engine", async ({ page }) => {
    const response = await page.goto(editorURL);
    expect(response?.status()).toBe(200);
    await expect(page.locator(".editor-shell")).toBeVisible();
    const document = await getEditorDocument(page);
    expect(document.editor.selected).toBe("deckflow");
    expect(document.editor.engines).toEqual([{ name: "deckflow", available: true }]);
  });

  test("supports contextual insertion without saving", async ({ page }) => {
    await openEditor(page);
    const mobile = await page.locator(".mobile-nav").isVisible();
    if (mobile) {
      await page.locator('[data-action="blocks"]').click();
      await page.locator('#sheet-content [data-block="paragraph"]').click();
    } else {
      await page.locator('.desktop-blocks [data-block="paragraph"]').click();
    }
    await expect(page.locator("#editor-status")).toHaveAttribute("data-kind", "dirty");
    await expect(page.locator('[data-action="save"]').first()).toBeEnabled();
  });

  test("inserts a language-aware code block without saving", async ({ page }) => {
    await openEditor(page);
    const mobile = await page.locator(".mobile-nav").isVisible();
    if (mobile) await page.locator('[data-action="blocks"]').click();
    const catalog = page.locator(mobile ? '#sheet-content .block-grid [data-block="code"]' : '.desktop-blocks .block-grid [data-block="code"]');
    await expect(catalog).toBeVisible();
    await catalog.click();
    await page.locator('#code-form [name="language"]').selectOption("go");
    await page.locator('#code-form [name="code"]').fill("package main\n\nfunc main() {}");
    await page.locator('#code-form button[type="submit"]').click();
    await expect(page.locator("#editor-status")).toHaveAttribute("data-kind", "dirty");
    await expect(page.frameLocator("iframe.deckflow-html-editor__preview").locator('pre[data-fileloom-code]')).toHaveAttribute("data-language", "go");
    await expect(page.frameLocator("iframe.deckflow-html-editor__preview").locator('pre[data-fileloom-code] code')).toContainText("package main");
  });

  test("edits code block content and language across the responsive editor", async ({ page }) => {
    await openEditor(page);
    const mobile = await page.locator(".mobile-nav").isVisible();
    if (mobile) await page.locator('[data-action="blocks"]:visible').click();
    const catalog = page.locator(mobile ? '#sheet-content .block-grid [data-block="code"]' : '.desktop-blocks .block-grid [data-block="code"]');
    await catalog.click();
    await page.locator('#code-form [name="language"]').selectOption("python");
    await page.locator('#code-form [name="code"]').fill("def old():\n    return 1");
    const editorHeight = await page.locator('#code-form textarea[name="code"]').evaluate((textarea) => textarea.getBoundingClientRect().height);
    expect(editorHeight).toBeGreaterThan(mobile ? 260 : 340);
    await page.locator('#code-form button[type="submit"]').click();

    const frame = page.frameLocator("iframe.deckflow-html-editor__preview");
    const block = frame.locator("pre[data-fileloom-code]");
    await block.click();
    if (mobile) {
      await page.locator('[data-action="details"]:visible').click();
      await expect(page.locator('#sheet-content [data-action="edit-code"]')).toBeVisible();
    } else {
      await expect(page.locator('.selection-code-action')).toBeVisible();
    }
    await page.locator('[data-action="edit-code"]:visible').click();
    await expect(page.locator("#code-form")).toBeVisible();
    await page.locator('#code-form [name="language"]').selectOption("javascript");
    await page.locator('#code-form [name="code"]').fill('const value = "<tag>&";\n  return value;');
    await page.locator('#code-form button[type="submit"]').click();

    await expect(page.locator("#editor-status")).toHaveText("Code block updated");
    await expect(block).toHaveAttribute("data-language", "javascript");
    await expect(block.locator("code")).toHaveClass(/language-javascript/);
    await expect(block.locator("code")).toContainText('<tag>&');
  });

  test("opens and applies the body HTML source editor", async ({ page }) => {
    await openEditor(page);
    const mobile = await page.locator(".mobile-nav").isVisible();
    if (mobile) {
      await page.locator('[data-action="details"]:visible').click();
      await page.locator('#sheet-content [data-action="source"]').click();
    } else {
      await page.locator('.topbar-actions [data-action="source"]:visible').click();
    }
    await expect(page.locator("#source-form")).toBeVisible();
    await expect(page.locator('#source-form [name="html"]')).toHaveValue(/ordinary HTML file/);
    await page.locator('#source-form [name="html"]').fill('<p data-source-edit="yes">Edited directly in HTML.</p>\n<pre class="fileloom-code-block" data-fileloom-code data-language="javascript"><code class="language-javascript" data-language="javascript">const source = true;</code></pre>');
    await page.locator('#source-form button[type="submit"]').click();
    const frame = page.frameLocator("iframe.deckflow-html-editor__preview");
    await expect(page.locator("#editor-status")).toHaveText("HTML source applied");
    await expect(frame.locator('p[data-source-edit="yes"]')).toHaveText("Edited directly in HTML.");
    await expect(frame.locator('pre[data-fileloom-code]')).toHaveAttribute("data-language", "javascript");
  });

  test("opens the actual theme-applied preview", async ({ page }) => {
    await openEditor(page);
    const mobile = await page.locator(".mobile-nav").isVisible();
    if (mobile) await page.locator('[data-action="blocks"]').click();
    const catalog = page.locator(mobile ? '#sheet-content .block-grid [data-block="code"]' : '.desktop-blocks .block-grid [data-block="code"]');
    await catalog.click();
    await page.locator('#code-form [name="language"]').selectOption("javascript");
    await page.locator('#code-form [name="code"]').fill("const answer = 42;");
    await page.locator('#code-form button[type="submit"]').click();
    const popupPromise = page.waitForEvent("popup");
    await page.locator('[data-action="preview"]:visible').first().click();
    const popup = await popupPromise;
    await popup.waitForLoadState("domcontentloaded");
    await expect(popup.locator("header.site-header")).toBeVisible();
    await expect(popup.locator("link[href^='/theme/style.css']")).toHaveCount(1);
    await expect(popup.locator(".article-body")).toContainText("ordinary HTML file");
    await expect(popup.locator("pre.fileloom-code-block")).toHaveAttribute("data-language", "javascript");
    await expect(popup.locator("pre.fileloom-code-block code")).toHaveClass(/fileloom-code-highlighted/);
    const codeColors = await popup.locator("pre.fileloom-code-block").evaluate((element) => ({
      background: getComputedStyle(element).backgroundColor,
      keyword: getComputedStyle(element.querySelector(".fileloom-token-keyword")).color,
    }));
    expect(codeColors.background).toBe("rgb(17, 17, 17)");
    expect(codeColors.keyword).toBe("rgb(234, 255, 0)");
    await popup.close();
  });

  test("supports selection-aware duplicate and movement actions", async ({ page }) => {
    await openEditor(page);
    const frame = page.frameLocator("iframe.deckflow-html-editor__preview");
    const paragraphs = frame.locator("p");
    await paragraphs.nth(1).click();
    const mobile = await page.locator(".mobile-nav").isVisible();
    if (mobile) {
      await page.locator('[data-action="details"]:visible').click();
      await expect(page.locator("#sheet-content .selection-actions")).toBeVisible();
    } else {
      await expect(page.locator(".selection-actions")).toBeVisible();
    }
    await page.locator('[data-action="move-up"]:visible').click();
    await expect(frame.locator("p").first()).toContainText("The public site is regenerated");
    await frame.locator("p").first().click();
    if (mobile) {
      await page.locator('[data-action="details"]:visible').click();
    }
    const count = await frame.locator("p").count();
    await page.locator('[data-action="duplicate"]:visible').click();
    await expect(frame.locator("p")).toHaveCount(count + 1);
    await frame.locator("p").last().click();
    if (mobile) await page.locator('[data-action="details"]:visible').click();
    await page.locator('[data-action="delete-selection"]:visible').click();
    await expect(frame.locator("p")).toHaveCount(count);
  });

  test("edits safe link properties without reserializing the body", async ({ page }) => {
    const original = await getEditorDocument(page);
    try {
      await openEditor(page);
      const mobile = await page.locator(".mobile-nav").isVisible();
    if (mobile) await page.locator('[data-action="blocks"]:visible').click();
    const catalog = page.locator(mobile ? '#sheet-content .block-grid [data-block="link"]' : '.desktop-blocks .block-grid [data-block="link"]');
    await catalog.click();
    const frame = page.frameLocator("iframe.deckflow-html-editor__preview");
    await frame.locator('a[href="/"]').last().click();
    if (mobile) await page.locator('[data-action="details"]:visible').click();
    await page.locator('[data-action="properties"]:visible').click();
    await expect(page.locator("#element-inspector-form")).toBeVisible();
    await expect(page.locator('#element-inspector-form [name="href"]')).toHaveValue("/");
    await page.locator('#element-inspector-form [name="href"]').fill("javascript:alert(1)");
    await page.locator('#element-inspector-form button[type="submit"]').click();
    await expect(page.locator("#editor-status")).toHaveText("Link URL is unsafe or invalid");
    await expect(page.locator("#element-inspector-form")).toBeVisible();
    await page.locator('#element-inspector-form [name="href"]').fill("https://example.com/docs");
    await page.locator('#element-inspector-form [name="id"]').fill("docs-link");
    await page.locator('#element-inspector-form [name="class"]').fill("primary-link");
    await page.locator('#element-inspector-form [name="target"]').selectOption("_blank");
    await page.locator('#element-inspector-form [name="rel"]').fill("noopener");
    await page.locator('#element-inspector-form button[type="submit"]').click();
    const link = frame.locator('a[href="https://example.com/docs"]').last();
    await expect(link).toHaveAttribute("id", "docs-link");
    await expect(link).toHaveClass(/primary-link/);
    await expect(link).toHaveAttribute("target", "_blank");
    await expect(link).toHaveAttribute("rel", "noopener");
    await expect(page.locator("#editor-status")).toHaveAttribute("data-kind", "dirty");
    await page.locator('[data-action="save"]:visible').first().click();
    await expect(page.locator("#editor-status")).toHaveText("Saved");
    await page.reload();
    const reloadedFrame = page.frameLocator("iframe.deckflow-html-editor__preview");
    const reloadedLink = reloadedFrame.locator('a[href="https://example.com/docs"]').last();
    await expect(reloadedLink).toHaveAttribute("id", "docs-link");
    await expect(reloadedLink).toHaveClass(/primary-link/);
    await expect(reloadedLink).toHaveAttribute("target", "_blank");
    await expect(reloadedLink).toHaveAttribute("rel", "noopener");
    } finally {
      await restoreEditorDocument(page, original);
    }
  });

  test("supports keyboard inline editing and preserves mixed markup", async ({ page }) => {
    const original = await getEditorDocument(page);
    try {
      await openEditor(page);
      const frame = page.frameLocator("iframe.deckflow-html-editor__preview");
      const mixed = frame.locator("p").first();
      const originalCode = await mixed.locator("code").textContent();
      await focusTextEnd(page, mixed);
      await expect(mixed).toHaveAttribute("contenteditable", "true");
      await page.keyboard.insertText(" [mixed-inline]");
      await page.keyboard.press("Control+Enter");
      await expect(mixed).toContainText("[mixed-inline]");
      await expect(mixed.locator("code")).toHaveText(originalCode);
      await expect(mixed).not.toHaveAttribute("data-local-editor-editing", "true");
      await expect(mixed).not.toHaveAttribute("contenteditable", /.*/);

      const plain = frame.locator("p").nth(1);
      const originalPlain = await plain.textContent();
      await focusTextEnd(page, plain);
      await expect(plain).toHaveAttribute("contenteditable", "plaintext-only");
      await page.keyboard.insertText(" [cancelled]");
      await page.keyboard.press("Escape");
      await expect(plain).toHaveText(originalPlain);
      await expect(plain).not.toHaveAttribute("data-local-editor-editing", "true");
      await focusTextEnd(page, plain);
      await page.keyboard.insertText(" [committed]");
      await page.keyboard.press("Control+Enter");
      await expect(plain).toContainText("[committed]");
      await expect(page.locator("#editor-status")).toHaveAttribute("data-kind", "dirty");
      await focusTextEnd(page, plain);
      await page.keyboard.insertText(" [keyboard-save]");
      await page.keyboard.press("Control+S");
      await expect(page.locator("#editor-status")).toHaveText("Saved");
      await page.reload();
      const reloadedFrame = page.frameLocator("iframe.deckflow-html-editor__preview");
      await expect(reloadedFrame.locator("p").first()).toContainText("[mixed-inline]");
      await expect(reloadedFrame.locator("p").first().locator("code")).toHaveText(originalCode);
      await expect(reloadedFrame.locator("p").nth(1)).toContainText("[committed]");
      await expect(reloadedFrame.locator("p").nth(1)).toContainText("[keyboard-save]");
    } finally {
      await restoreEditorDocument(page, original);
    }
  });

  test("rejects mixed-text edits that would change source structure", async ({ page }) => {
    const original = await getEditorDocument(page);
    try {
      await openEditor(page);
      const frame = page.frameLocator("iframe.deckflow-html-editor__preview");
      const paragraph = frame.locator("p").first();
      const originalCode = await paragraph.locator("code").textContent();
      await focusTextEnd(page, paragraph);
      await page.keyboard.press("Control+A");
      await page.keyboard.insertText("flattened mixed markup");
      await page.keyboard.press("Control+Enter");
      await expect(page.locator("#editor-status")).toHaveText("The text edit changed the HTML structure and was reverted");
      await expect(paragraph.locator("code")).toHaveText(originalCode);
      await expect(paragraph).not.toContainText("flattened mixed markup");
    } finally {
      await restoreEditorDocument(page, original);
    }
  });
  test("keeps keyboard undo and redo ordered across inline and structural edits", async ({ page }) => {
    const original = await getEditorDocument(page);
    try {
      await openEditor(page);
      const frame = page.frameLocator("iframe.deckflow-html-editor__preview");
      const paragraphs = frame.locator("p");
      const initialCount = await paragraphs.count();
      await insertParagraphBlock(page);
      const token = " [keyboard-history]";
      await focusTextEnd(page, paragraphs.first());
      await page.keyboard.insertText(token);
      await page.keyboard.press("Control+Enter");
      await expect(paragraphs.first()).toContainText(token);

      await page.keyboard.press("Control+Z");
      await expect(paragraphs.first()).not.toContainText(token);
      await expect(paragraphs).toHaveCount(initialCount + 1);
      await page.keyboard.press("Control+Z");
      await expect(paragraphs).toHaveCount(initialCount);
      await page.keyboard.press("Control+Shift+Z");
      await expect(paragraphs).toHaveCount(initialCount + 1);
      await page.keyboard.press("Control+Z");
      await expect(paragraphs).toHaveCount(initialCount);
      await focusTextEnd(page, paragraphs.first());
      await page.keyboard.press("Control+Enter");
      await page.locator("iframe.deckflow-html-editor__preview").focus();
      await page.keyboard.press("Control+D");
      await expect(paragraphs).toHaveCount(initialCount + 1);
      await page.locator("iframe.deckflow-html-editor__preview").focus();
      await page.keyboard.press("Delete");
      await expect(paragraphs).toHaveCount(initialCount);
      await page.keyboard.press("Escape");
      await expect(page.locator("#selection-info")).toContainText("Nothing selected");
    } finally {
      await restoreEditorDocument(page, original);
    }
  });

  test("keeps undo and redo controls usable on desktop and mobile", async ({ page }) => {
    const original = await getEditorDocument(page);
    try {
      await openEditor(page);
      const frame = page.frameLocator("iframe.deckflow-html-editor__preview");
      const initialCount = await frame.locator("p").count();
      await insertParagraphBlock(page);
      await expect(frame.locator("p")).toHaveCount(initialCount + 1);
      const mobile = await page.locator(".mobile-nav").isVisible();
      if (mobile) await page.locator('[data-action="style"]:visible').click();
      const undo = page.locator('[data-action="undo"]:visible').first();
      const redo = page.locator('[data-action="redo"]:visible').first();
      await undo.click();
      await expect(frame.locator("p")).toHaveCount(initialCount);
      await redo.click();
      await expect(frame.locator("p")).toHaveCount(initialCount + 1);
    } finally {
      await restoreEditorDocument(page, original);
    }
  });

  test("formats a selected text range without flattening inline markup", async ({ page }) => {
    const original = await getEditorDocument(page);
    try {
      await openEditor(page);
      const frame = page.frameLocator("iframe.deckflow-html-editor__preview");
      const paragraph = frame.locator("p").first();
      const originalCode = await paragraph.locator("code").textContent();
      await selectTextRange(paragraph, 0, 4);
      const toolbar = frame.locator(".local-editor-toolbar");
      await expect(toolbar).toBeVisible();
      await toolbar.locator('[data-tool="bold"]').click();
      await expect(paragraph.locator('[data-local-text-key][style*="font-weight"]')).toHaveCount(1);
      await expect(paragraph.locator("code")).toHaveText(originalCode);
      await expect(page.locator("#editor-status")).toHaveAttribute("data-kind", "dirty");
      await page.locator('[data-action="save"]:visible').first().click();
      await expect(page.locator("#editor-status")).toHaveText("Saved");
      await page.reload();
      const reloadedParagraph = page.frameLocator("iframe.deckflow-html-editor__preview").locator("p").first();
      await expect(reloadedParagraph.locator('[data-local-text-key][style*="font-weight"]')).toHaveCount(1);
      await expect(reloadedParagraph.locator("code")).toHaveText(originalCode);
    } finally {
      await restoreEditorDocument(page, original);
    }
  });
  test("loads the Deckflow theme-layout editor", async ({ page }) => {
    await openThemeEditor(page);
    const document = await getThemeDocument(page);
    expect(document.resource).toBe("theme-layout");
    expect(document.editor.selected).toBe("deckflow");
    expect(document.editor.engines).toEqual([{ name: "deckflow", available: true }]);
    expect(document.html).toContain("{{content}}");
    expect(await page.frameLocator("iframe.deckflow-html-editor__preview").locator("[data-fileloom-template-token]").count()).toBeGreaterThan(0);
  });

  test("saves and reloads a theme layout while preserving template slots", async ({ page }) => {
    const original = await getThemeDocument(page);
    try {
      await openThemeEditor(page);
      await insertParagraphBlock(page);
      await page.locator('[data-action="save"]:visible').first().click();
      await expect(page.locator("#editor-status")).toHaveText("Saved");
      await page.reload();
      const frame = page.frameLocator("iframe.deckflow-html-editor__preview");
      await expect(frame.locator("p").filter({ hasText: "Write something here." })).toBeVisible();
      const saved = await getThemeDocument(page);
      expect(saved.html).toContain("{{content}}");
      expect(saved.html).toContain("Write something here.");
      expect(saved.html).not.toContain("FILELOOM_TEMPLATE");
      expect(saved.html).not.toContain("data-fileloom-template-token");
    } finally {
      await restoreThemeDocument(page, original);
    }
  });

  test("renders an unsaved theme layout preview without literal placeholders", async ({ page }) => {
    const original = await getThemeDocument(page);
    try {
      await openThemeEditor(page);
      await insertParagraphBlock(page);
      const popupPromise = page.waitForEvent("popup");
      await page.locator('[data-action="preview"]:visible').first().click();
      const popup = await popupPromise;
      await popup.waitForLoadState("domcontentloaded");
      await expect(popup.locator("header.site-header")).toBeVisible();
      await expect(popup.locator(".fileloom-theme-preview")).toBeVisible();
      await expect(popup.locator("body")).not.toContainText("{{content}}");
      await popup.close();
    } finally {
      await restoreThemeDocument(page, original);
    }
  });
  test("restores a theme-layout revision from Deckflow history", async ({ page }) => {
    const original = await getThemeDocument(page);
    try {
      await openThemeEditor(page);
      await insertParagraphBlock(page);
      await page.locator('[data-action="save"]:visible').first().click();
      await expect(page.locator("#editor-status")).toHaveText("Saved");
      const mobile = await page.locator(".mobile-nav").isVisible();
      if (mobile) {
        await page.locator('[data-action="details"]:visible').click();
        await page.locator('#sheet-content [data-action="history"]').click();
      } else {
        await page.locator('[data-action="history"]:visible').first().click();
      }
      await expect(page.locator('[data-sheet="history"] .revision-list')).toBeVisible();
      await page.locator('[data-sheet="history"] [data-revision-id]').last().click();
      await expect(page.locator("#editor-status")).toHaveText("Revision restored");
      await expect(page.frameLocator("iframe.deckflow-html-editor__preview").locator("p").filter({ hasText: "Write something here." })).toHaveCount(0);
    } finally {
      await restoreThemeDocument(page, original);
    }
  });
  test("saves, reloads, and preserves the generated public page", async ({ page }) => {
    const original = await getEditorDocument(page);
    try {
      await openEditor(page);
      await insertParagraphBlock(page);
      await page.keyboard.press("Control+S");
      await expect(page.locator("#editor-status")).toHaveText("Saved");
      await page.reload();
      await expect(page.locator(".editor-shell")).toBeVisible();
      await expect(page.frameLocator("iframe.deckflow-html-editor__preview").locator("p").filter({ hasText: "Write something here." })).toBeVisible();
      const publicResponse = await page.evaluate(async () => {
        const response = await fetch("/about/", { cache: "no-store" });
        return { status: response.status, text: await response.text() };
      });
      expect(publicResponse.status).toBe(200);
      expect(publicResponse.text).toContain("Write something here.");
    } finally {
      await restoreEditorDocument(page, original);
    }
  });

  test("surfaces a stale save conflict and can keep local edits", async ({ page }) => {
    const original = await getEditorDocument(page);
    try {
      await openEditor(page);
      await insertParagraphBlock(page);
      const remote = await getEditorDocument(page);
      const remoteSave = await cmsJSON(page, "/_cms/api/editor-save", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ path: remote.path, engine: "deckflow", html: "<p data-conflict-probe=\"remote\">Remote edit</p>", metadata: editorMetadata(remote.document), base_sha256: remote.source_sha256 }),
      });
      expect(remoteSave.status).toBe(200);
      await page.locator('[data-action="save"]:visible').first().click();
      await expect(page.locator('[data-sheet="conflict"]')).toBeVisible();
      await expect(page.locator("#sheet-content")).toContainText("Remote edit");
      await page.locator('[data-action="overwrite-conflict"]:visible').click();
      await expect(page.locator("#editor-status")).toHaveText("Saved");
      const persisted = await getEditorDocument(page);
      expect(persisted.html).toContain("Write something here.");
    } finally {
      await restoreEditorDocument(page, original);
    }
  });

  test("inserts and replaces media through the selection-aware media sheet", async ({ page }) => {
    const suffix = test.info().project.name;
    const firstName = `first-${suffix}.png`;
    const secondName = `second-${suffix}.png`;
    await openEditor(page);
    const mobile = await page.locator(".mobile-nav").isVisible();
    if (mobile) await page.locator('[data-action="media"]:visible').click();
    else await page.locator('.desktop-blocks [data-block="media"]').click();
    await page.locator("#media-upload").setInputFiles([
      { name: firstName, mimeType: "image/png", buffer: Buffer.from([0x89, 0x50, 0x4e, 0x47]) },
      { name: secondName, mimeType: "image/png", buffer: Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d]) },
    ]);
    await expect(page.locator(`[data-media-name="${firstName}"]`)).toBeVisible();
    await expect(page.locator(`[data-media-name="${secondName}"]`)).toBeVisible();
    await page.locator(`[data-media-name="${firstName}"]`).click();
    const frame = page.frameLocator("iframe.deckflow-html-editor__preview");
    await expect(frame.locator(`img[src="/media/${firstName}"]`)).toBeVisible();
    await frame.locator(`img[src="/media/${firstName}"]`).click();
    if (mobile) await page.locator('[data-action="details"]:visible').click();
    await page.locator('[data-action="properties"]:visible').click();
    await page.locator('#element-inspector-form [name="alt"]').fill("First image");
    await page.locator('#element-inspector-form [name="loading"]').selectOption("lazy");
    await page.locator('#element-inspector-form button[type="submit"]').click();
    await expect(frame.locator(`img[src="/media/${firstName}"]`)).toHaveAttribute("alt", "First image");
    await expect(frame.locator(`img[src="/media/${firstName}"]`)).toHaveAttribute("loading", "lazy");
    await frame.locator(`img[src="/media/${firstName}"]`).click();
    if (mobile) await page.locator('[data-action="media"]:visible').click();
    else await page.locator('[data-action="media"]:visible').first().click();
    await expect(page.locator("#sheet-content")).toContainText("Replace selected image");
    await page.locator(`[data-media-name="${secondName}"]`).click();
    await expect(frame.locator(`img[src="/media/${secondName}"]`)).toBeVisible();
    await expect(frame.locator(`img[src="/media/${firstName}"]`)).toHaveCount(0);
  });

  test("restores a source revision from the editor history sheet", async ({ page }) => {
    const original = await getEditorDocument(page);
    try {
      await openEditor(page);
      await insertParagraphBlock(page);
      await page.locator('[data-action="save"]:visible').first().click();
      await expect(page.locator("#editor-status")).toHaveText("Saved");
      const mobile = await page.locator(".mobile-nav").isVisible();
      if (mobile) {
        await page.locator('[data-action="details"]:visible').click();
        await page.locator('#details-form [data-action="history"]').click();
      } else {
        await page.locator('[data-action="history"]:visible').first().click();
      }
      await expect(page.locator('[data-sheet="history"] .revision-list')).toBeVisible();
      const revision = page.locator('[data-sheet="history"] [data-revision-id]').first();
      await expect(revision).toBeVisible();
      await revision.click();
      await expect(page.locator("#editor-status")).toHaveText("Revision restored");
      await expect(page.frameLocator("iframe.deckflow-html-editor__preview").locator("p").filter({ hasText: "Write something here." })).toHaveCount(0);
    } finally {
      await restoreEditorDocument(page, original);
    }
  });

  test("opens source history from the editor", async ({ page }) => {
    await openEditor(page);
    const history = page.locator('[data-action="history"]:visible').first();
    if (await history.count()) await history.click();
    else {
      await openDetails(page);
      await page.locator('#details-form [data-action="history"]').click();
    }
    await expect(page.locator('[data-sheet="history"]')).toBeVisible();
    await expect(page.locator("#sheet-content")).toContainText(/Revision history|No revisions yet/);
  });
});
