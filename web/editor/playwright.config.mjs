import { defineConfig, devices } from "@playwright/test";

const baseURL = process.env.FILELOOM_E2E_URL || "http://127.0.0.1:8000";
const ownerEmail = process.env.FILELOOM_E2E_EMAIL || "owner@example.com";

export default defineConfig({
  testDir: "./e2e",
  timeout: 30_000,
  fullyParallel: false,
  reporter: process.env.CI ? "dot" : "list",
  use: {
    baseURL,
    extraHTTPHeaders: { "X-ExeDev-Email": ownerEmail },
    trace: "retain-on-failure",
  },
  projects: [
    { name: "desktop", use: { viewport: { width: 1440, height: 900 } } },
    { name: "tablet", use: { viewport: { width: 834, height: 1112 }, isMobile: true, hasTouch: true } },
    { name: "android", use: { ...devices["Pixel 7"] } },
  ],
});
