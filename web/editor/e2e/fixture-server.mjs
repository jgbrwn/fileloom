import { cp, mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { spawn } from "node:child_process";

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, "../../..");
const sourceSite = join(root, "site");
const tempRoot = await mkdtemp(join(tmpdir(), "fileloom-editor-e2e-"));
const targetSite = join(tempRoot, "site");
const ownerEmail = process.env.FILELOOM_E2E_EMAIL || "owner@example.com";
const port = process.env.FILELOOM_E2E_PORT || "8010";

const childEnv = { ...process.env };
if (process.env.FILELOOM_E2E_CSP) {
  childEnv.FILELOOM_CMS_CSP = process.env.FILELOOM_E2E_CSP;
  childEnv.FILELOOM_PUBLIC_CSP = process.env.FILELOOM_E2E_CSP;
}

await cp(sourceSite, targetSite, {
  recursive: true,
  filter: (source) => !source.includes(`${join("site", ".fileloom")}`) && !source.includes(`${join("site", "public")}`),
});

const child = spawn("go", ["run", "./cmd/fileloom", "-listen", `127.0.0.1:${port}`, "-site", targetSite, "-web", join(root, "web"), "-owner-email", ownerEmail], {
  cwd: root,
  env: childEnv,
  stdio: "inherit",
});

const shutdown = () => {
  if (!child.killed) child.kill("SIGTERM");
};
process.on("SIGINT", shutdown);
process.on("SIGTERM", shutdown);
process.on("exit", shutdown);
child.on("exit", (code, signal) => {
  if (signal !== "SIGTERM" && signal !== "SIGINT") process.exitCode = code || 1;
});
