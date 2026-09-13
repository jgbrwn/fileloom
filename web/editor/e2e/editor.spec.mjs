import { test, expect } from "@playwright/test";

const editorURL = "/_cms/editor?path=pages%2Fabout.html";
const e2eBaseURL = process.env.FILELOOM_E2E_URL || "http://127.0.0.1:8010";
const e2eOwnerEmail = process.env.FILELOOM_E2E_EMAIL || "owner@example.com";

async function openEditor(page) {
  await page.goto(editorURL);
  await expect(page.locator(".editor-shell")).toBeVisible();
  await expect(page.locator("iframe.deckflow-html-editor__preview")).toBeVisible();
}

async function cmsJSON(page, url, init = {}) {
  const { body: requestBody, ...requestInit } = init;
  const response = await page.request.fetch(new URL(url, e2eBaseURL).toString(), {
    ...requestInit,
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

function editorMetadata(document) {
  return {
    title: document.title,
    date: document.date,
    tags: document.tags || [],
    category: document.category || "",
    excerpt: document.excerpt || "",
    status: document.status || "published",
    publish_at: document.publish_at || "",
  };
}

async function restoreEditorDocument(page, original) {
  const current = await getEditorDocument(page);
  const revisionsResponse = await cmsJSON(page, `/_cms/api/revisions?path=${encodeURIComponent(original.path)}`);
  expect(revisionsResponse.status, JSON.stringify(revisionsResponse.body)).toBe(200);
  const revision = (revisionsResponse.body.revisions || []).find((candidate) => candidate.sha256 === original.source_sha256);
  if (!revision) throw new Error(`Could not find original revision ${original.source_sha256}`);
  const body = new URLSearchParams({ path: original.path, id: revision.id, base_sha256: current.source_sha256 }).toString();
  const response = await cmsJSON(page, "/_cms/api/revisions/restore", {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body,
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

test.describe("Deckflow Fileloom editor", () => {
  test("loads the editor shell and keeps theme controls separate", async ({ page }) => {
    await openEditor(page);
    await expect(page.locator(".document-heading strong")).toHaveText("About");
    await expect(page.locator(".canvas-hint")).toContainText("Tap text to edit");
    await openDetails(page);
    await expect(page.locator("#details-form [data-metadata=title]")).toHaveValue("About");
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

  test("loads Deckflow without requesting Vvveb fallback assets", async ({ page }) => {
    const legacyRequests = [];
    page.on("request", (request) => {
      if (/fileloom-vvveb|\/vvveb(?:js|\/)/i.test(request.url())) legacyRequests.push(request.url());
    });
    const response = await page.goto(editorURL);
    expect(response?.status()).toBe(200);
    await expect(page.locator(".editor-shell")).toBeVisible();
    expect(legacyRequests).toEqual([]);
    const document = await getEditorDocument(page);
    expect(document.editor.selected).toBe("deckflow");
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
  });

  test("edits safe link properties without reserializing the body", async ({ page }) => {
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
  });

  test("saves, reloads, and preserves the generated public page", async ({ page }) => {
    const original = await getEditorDocument(page);
    try {
      await openEditor(page);
      await insertParagraphBlock(page);
      const frame = page.frameLocator("iframe.deckflow-html-editor__preview");
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
    await page.locator("#media-upload").setInputFiles({ name: firstName, mimeType: "image/png", buffer: Buffer.from([0x89, 0x50, 0x4e, 0x47]) });
    await expect(page.locator(`[data-media-name="${firstName}"]`)).toBeVisible();
    await page.locator("#media-upload").setInputFiles({ name: secondName, mimeType: "image/png", buffer: Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d]) });
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
