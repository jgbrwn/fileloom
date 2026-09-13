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
