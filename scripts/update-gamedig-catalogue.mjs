// Extract literal public catalogue metadata; never execute upstream JavaScript.
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdir, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import ts from 'typescript';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const commit = 'aff6afd252dc1c4853aae7522423623b7b5adec7', version = '5.3.3';
const base = `https://raw.githubusercontent.com/gamedig/node-gamedig/${commit}/`;
async function read(name) {
  const response = await fetch(base + name, { signal: AbortSignal.timeout(15000), redirect: 'error' });
  assert.equal(response.status, 200);
  const raw = Buffer.from(await response.arrayBuffer());
  assert.ok(raw.length > 0 && raw.length <= 2 * 1024 * 1024);
  return raw;
}
const raw = await read('lib/games.js');
const source = ts.createSourceFile('games.js', raw.toString('utf8'), ts.ScriptTarget.ES2023, true, ts.ScriptKind.JS);
assert.equal(source.parseDiagnostics.length, 0);
const declaration = source.statements.flatMap(statement => ts.isVariableStatement(statement) ? [...statement.declarationList.declarations] : []).find(item => item.name.getText(source) === 'games');
assert.ok(declaration?.initializer && ts.isObjectLiteralExpression(declaration.initializer));
function property(object, name) {
  return object.properties.find(item => ts.isPropertyAssignment(item) && (ts.isIdentifier(item.name) || ts.isStringLiteral(item.name)) && item.name.text === name);
}
const games = declaration.initializer.properties.map(item => {
  assert.ok(ts.isPropertyAssignment(item) && (ts.isIdentifier(item.name) || ts.isStringLiteral(item.name)) && ts.isObjectLiteralExpression(item.initializer));
  const id = item.name.text, name = property(item.initializer, 'name');
  assert.match(id, /^[a-z0-9][a-z0-9_-]{0,63}$/u);
  assert.ok(name && ts.isStringLiteral(name.initializer));
  const options = property(item.initializer, 'options');
  const port = options && ts.isObjectLiteralExpression(options.initializer) ? property(options.initializer, 'port') : undefined;
  const defaultPort = port && ts.isNumericLiteral(port.initializer) ? Number(port.initializer.text) : undefined;
  if (defaultPort !== undefined) assert.ok(Number.isInteger(defaultPort) && defaultPort >= 1 && defaultPort <= 65535);
  return { id, name: name.initializer.text, ...(defaultPort === undefined ? {} : { defaultPort }) };
}).sort((a, b) => a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
assert.ok(games.length >= 100 && games.length <= 1024);
assert.equal(new Set(games.map(game => game.id)).size, games.length);
const output = resolve(root, 'apps/node-go/internal/gamedigmonitor');
await mkdir(output, { recursive: true });
const license = await read('LICENSE');
const digest = value => createHash('sha256').update(value).digest('hex');
const provenance = { module: 'gamedig', version, commit, source: 'lib/games.js', sourceSha256: digest(raw), licenseSha256: digest(license), description: 'Literal public game IDs, labels and default ports extracted without executing the source.' };
await writeFile(resolve(output, 'catalogue.json'), JSON.stringify({ version, games }, null, 2) + '\n');
await writeFile(resolve(output, 'upstream.json'), JSON.stringify(provenance, null, 2) + '\n');
await writeFile(resolve(output, 'LICENSE.gamedig'), license);
console.log(JSON.stringify({ version, commit, games: games.length, sourceSha256: provenance.sourceSha256 }));
