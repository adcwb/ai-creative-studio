// Static UI-message coverage. User content and dynamic server messages are reviewed separately.
const ts = require('../frontend/node_modules/typescript')
const fs = require('node:fs')
const path = require('node:path')
const vm = require('node:vm')
const root = path.resolve(__dirname, '../frontend/src')
const messages = {}
for (const file of fs.readdirSync(path.join(root, 'locales'))) {
  const context = {exports: {}}
  const source = fs.readFileSync(path.join(root, 'locales', file), 'utf8')
  vm.runInNewContext(ts.transpileModule(source, {compilerOptions: {module: ts.ModuleKind.CommonJS}}).outputText, context)
  Object.assign(messages, context.exports.default)
}
const failures = []
function inspect(directory) {
  for (const file of fs.readdirSync(directory)) {
    const filename = path.join(directory, file)
    if (fs.statSync(filename).isDirectory()) {if (file !== 'locales') inspect(filename); continue}
    if (!/\.tsx?$/.test(file) || file.includes('.test.')) continue
    const source = ts.createSourceFile(filename, fs.readFileSync(filename, 'utf8'), ts.ScriptTarget.Latest, true, file.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS)
    function visit(node) {
      if (ts.isCallExpression(node) && ['t', 'translate'].includes(node.expression.getText(source)) && node.arguments[0] && ts.isStringLiteral(node.arguments[0])) {
        const key = node.arguments[0].text
        if (/[\u3400-\u9fff]/.test(key) && !(key in messages)) failures.push(`${file}: missing English message ${JSON.stringify(key)}`)
      }
      if (ts.isJsxText(node) && /[\u3400-\u9fff]/.test(node.text) && !(file === 'LanguagePicker.tsx' && node.text.trim() === '简体中文')) failures.push(`${file}: untranslated JSX ${JSON.stringify(node.text.trim())}`)
      ts.forEachChild(node, visit)
    }
    visit(source)
  }
}
inspect(root)
if (failures.length) {console.error(failures.join('\n')); process.exitCode = 1}
else console.log(`i18n static coverage passed: ${Object.keys(messages).length} messages; no untranslated Chinese JSX except language endonym.`)
