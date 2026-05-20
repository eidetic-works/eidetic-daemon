// esbuild config for eidetic-obsidian.
//
// Obsidian plugins ship as a single CommonJS `main.js` at the plugin root
// (alongside manifest.json + styles.css). We bundle `src/main.ts` into that,
// externalising `obsidian`, `electron`, and Node built-ins that Obsidian's
// runtime provides on desktop. Mobile builds also load this same bundle —
// any desktop-only `node:*` imports must be guarded behind runtime checks
// (see src/daemonClient.ts for the requestUrl fallback).

import esbuild from 'esbuild';
import builtins from 'builtin-modules';
import process from 'node:process';

const production = process.argv.includes('production');
const watch = process.argv.includes('watch');

/** @type {import('esbuild').BuildOptions} */
const opts = {
  entryPoints: ['src/main.ts'],
  bundle: true,
  format: 'cjs',
  platform: 'node',
  target: 'es2022',
  outfile: 'main.js',
  external: [
    'obsidian',
    'electron',
    '@codemirror/autocomplete',
    '@codemirror/collab',
    '@codemirror/commands',
    '@codemirror/language',
    '@codemirror/lint',
    '@codemirror/search',
    '@codemirror/state',
    '@codemirror/view',
    '@lezer/common',
    '@lezer/highlight',
    '@lezer/lr',
    ...builtins
  ],
  sourcemap: production ? false : 'inline',
  minify: production,
  treeShaking: true,
  logLevel: 'info'
};

if (watch) {
  const ctx = await esbuild.context(opts);
  await ctx.watch();
  console.log('[esbuild] watching for changes...');
} else {
  await esbuild.build(opts);
}
