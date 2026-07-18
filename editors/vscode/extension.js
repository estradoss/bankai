// bankai IDE bridge — VS Code extension.
//
// It discovers a running `bankai --ide` process via its lockfile under
// ~/.claude/ide/<port>.lock, then talks to the bridge's plain-HTTP endpoints
// (see internal/bridge): it pushes the current editor selection and polls for
// agent→IDE commands (open a file, show a diff). No external npm deps — uses
// Node's built-in http/fs, which VS Code bundles.

const vscode = require('vscode')
const http = require('http')
const fs = require('fs')
const os = require('os')
const path = require('path')

let timer = null
let base = null // { host, port, token }

function ideDir() {
  return path.join(os.homedir(), '.claude', 'ide')
}

// Find the newest lockfile and parse its connection info.
function discover() {
  let dir
  try {
    dir = fs.readdirSync(ideDir())
  } catch {
    return null
  }
  const locks = dir.filter((f) => f.endsWith('.lock'))
  if (locks.length === 0) return null
  // Pick the most recently modified lockfile.
  locks.sort(
    (a, b) =>
      fs.statSync(path.join(ideDir(), b)).mtimeMs -
      fs.statSync(path.join(ideDir(), a)).mtimeMs,
  )
  try {
    const info = JSON.parse(fs.readFileSync(path.join(ideDir(), locks[0]), 'utf8'))
    if (info.transport !== 'http' || !info.port) return null
    return { host: '127.0.0.1', port: info.port, token: info.authToken || '' }
  } catch {
    return null
  }
}

// Minimal JSON HTTP helper against the bridge.
function request(method, urlPath, body) {
  return new Promise((resolve, reject) => {
    if (!base) return reject(new Error('not connected'))
    const data = body ? JSON.stringify(body) : null
    const headers = { 'Content-Type': 'application/json' }
    if (base.token) headers['Authorization'] = 'Bearer ' + base.token
    const req = http.request(
      { host: base.host, port: base.port, path: urlPath, method, headers },
      (res) => {
        let buf = ''
        res.on('data', (c) => (buf += c))
        res.on('end', () => resolve(buf ? safeParse(buf) : null))
      },
    )
    req.on('error', reject)
    if (data) req.write(data)
    req.end()
  })
}

function safeParse(s) {
  try {
    return JSON.parse(s)
  } catch {
    return null
  }
}

function pushSelection(editor) {
  if (!editor || !base) return
  const sel = editor.selection
  const doc = editor.document
  request('POST', '/v1/selection', {
    file: doc.uri.fsPath,
    text: doc.getText(sel),
    startLine: sel.start.line + 1,
    endLine: sel.end.line + 1,
  }).catch(() => {})
}

// Poll agent→IDE commands and apply them.
async function pollCommands() {
  if (!base) return
  const res = await request('GET', '/v1/commands').catch(() => null)
  if (!res || !res.commands) return
  for (const cmd of res.commands) {
    if (cmd.kind === 'openFile' && cmd.file) {
      const doc = await vscode.workspace.openTextDocument(cmd.file).then(
        (d) => d,
        () => null,
      )
      if (doc) vscode.window.showTextDocument(doc)
    } else if (cmd.kind === 'showDiff' && cmd.file) {
      const left = vscode.Uri.parse('untitled:' + cmd.file + ' (old)')
      const right = vscode.Uri.file(cmd.file)
      vscode.commands.executeCommand('vscode.diff', left, right, 'bankai diff: ' + path.basename(cmd.file))
    }
  }
}

function connect() {
  base = discover()
  if (base) {
    vscode.window.setStatusBarMessage('bankai: connected to agent on :' + base.port, 4000)
  }
}

function activate(context) {
  connect()

  context.subscriptions.push(
    vscode.commands.registerCommand('bankai.reconnect', connect),
    vscode.window.onDidChangeTextEditorSelection((e) => pushSelection(e.textEditor)),
  )

  // Poll for agent→IDE commands, and periodically re-discover the bridge.
  timer = setInterval(() => {
    if (!base) connect()
    pollCommands().catch(() => {})
  }, 1000)
  context.subscriptions.push({ dispose: () => clearInterval(timer) })
}

function deactivate() {
  if (timer) clearInterval(timer)
}

module.exports = { activate, deactivate }
