// esbuild config for eidetic-vscode. Bundles src/extension.ts → dist/extension.js
// in CommonJS form so VS Code's extension host can require() it. Externalises
// the `vscode` module (provided at runtime by the host).

const esbuild = require('esbuild');

const production = process.argv.includes('--production');
const watch = process.argv.includes('--watch');

/** @type {import('esbuild').BuildOptions} */
const opts = {
  entryPoints: ['src/extension.ts'],
  bundle: true,
  format: 'cjs',
  platform: 'node',
  target: 'node18',
  outfile: 'dist/extension.js',
  external: ['vscode'],
  sourcemap: !production,
  minify: production,
  logLevel: 'info'
};

async function main() {
  if (watch) {
    const ctx = await esbuild.context(opts);
    await ctx.watch();
    console.log('[esbuild] watching...');
  } else {
    await esbuild.build(opts);
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
