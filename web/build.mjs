import { build } from "esbuild";
import { cp, mkdir, rm } from "node:fs/promises";
await rm("dist", { recursive: true, force: true });
await mkdir("dist", { recursive: true });
await Promise.all(
  ["index.html", "img", "lang", "favicon.png", "favicon.webp"].map((path) =>
    cp(path, `dist/${path}`, { recursive: true }),
  ),
);
await build({
  entryPoints: ["src/app.js", "src/style.css"],
  outdir: "dist/assets",
  bundle: true,
  minify: true,
  sourcemap: false,
  target: ["es2022"],
});
