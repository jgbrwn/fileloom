import { test, expect } from "@playwright/test";

const editorURL = "/_cms/editor?path=pages%2Fabout.html";

async function openEditor(page) {
  await page.goto(editorURL);
  await expect(page.locator(".editor-shell")).toBeVisible();
  await expect(page.locator("iframe.deckflow-html-editor__preview")).toBeVisible();
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
