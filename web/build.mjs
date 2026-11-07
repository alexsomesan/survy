// Builds the frontend into web/dist: index.html, app.js, app.css.
import { build } from "esbuild";
import { copyFile, mkdir, readdir, rm } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

const root = path.dirname(fileURLToPath(import.meta.url));
const dist = path.join(root, "dist");

await mkdir(dist, { recursive: true });
for (const f of await readdir(dist)) if (f !== ".gitkeep") await rm(path.join(dist, f), { recursive: true });

const common = { bundle: true, minify: true, logLevel: "info", target: ["es2020", "chrome100", "firefox100", "safari15"] };

await build({ ...common, entryPoints: [path.join(root, "src/main.ts")], outfile: path.join(dist, "app.js"), format: "iife" });
await build({ ...common, entryPoints: [path.join(root, "src/app.css")], outfile: path.join(dist, "app.css") });
await copyFile(path.join(root, "src/index.html"), path.join(dist, "index.html"));
