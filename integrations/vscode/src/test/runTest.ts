// runTest.ts — Mocha runner for the daemonClient smoke tests. We don't use
// @vscode/test-electron here because the tests only exercise plain Node code
// (the HTTP client + helpers). The extension-host integration tests are a
// follow-up once we have a CI runner.

import Mocha = require('mocha');
import * as path from 'node:path';
import * as fs from 'node:fs';

async function main(): Promise<void> {
  const mocha = new Mocha({ ui: 'bdd', color: true, timeout: 10_000 });
  const testsDir = path.resolve(__dirname);
  for (const file of fs.readdirSync(testsDir)) {
    if (file.endsWith('.test.js')) mocha.addFile(path.join(testsDir, file));
  }
  await new Promise<void>((resolve, reject) => {
    mocha.run((failures) => {
      if (failures > 0) reject(new Error(`${failures} test(s) failed`));
      else resolve();
    });
  });
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
