// Playwright global setup (FRONTEND-DESIGN.md 10): real stack, no mocks.
// Builds the backend and stub_whisper from source, boots them on fixed
// ports with a generated TOML, and tears everything down after the run.
// The e2e token is hashed here and never written to the repo.

import { execFileSync, spawn, type ChildProcess } from 'node:child_process'
import crypto from 'node:crypto'
import fs from 'node:fs'
import net from 'node:net'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const WEB_DIR = path.resolve(HERE, '../../..')
const REPO_ROOT = path.resolve(WEB_DIR, '..')

export const E2E_TOKEN = 'e2e-token-not-a-secret'
export const BACKEND_PORT = 18188
export const STUB_WHISPER_PORT = 18191
export const PREVIEW_PORT = 4173

async function assertPortFree(port: number): Promise<void> {
  const probe = net.createServer()
  try {
    await new Promise<void>((resolve, reject) => {
      probe.once('error', (err: NodeJS.ErrnoException) => reject(err))
      probe.once('listening', () => probe.close(() => resolve()))
      probe.listen(port, '127.0.0.1')
    })
  } catch (err) {
    const code = (err as NodeJS.ErrnoException).code
    throw new Error(
      `e2e port ${port} is not free (${code ?? 'unknown'}); stop whatever holds it and rerun`,
    )
  }
}

function spawnLogged(command: string, args: string[], logPath: string): ChildProcess {
  const out = fs.openSync(logPath, 'a')
  const child = spawn(command, args, { stdio: ['ignore', out, out] })
  return child
}

async function waitForHealth(timeoutMs = 15_000): Promise<void> {
  const url = `http://127.0.0.1:${BACKEND_PORT}/v1/health`
  const deadline = Date.now() + timeoutMs
  let lastError = ''
  while (Date.now() < deadline) {
    try {
      const res = await fetch(url)
      if (res.ok) return
      lastError = `HTTP ${res.status}`
    } catch (err) {
      lastError = String(err)
    }
    await new Promise((r) => setTimeout(r, 200))
  }
  throw new Error(`backend never became healthy on ${url} (${lastError})`)
}

export default async function globalSetup(): Promise<() => Promise<void>> {
  // 4173 is checked by the playwright webServer itself (--strictPort):
  // Playwright starts webServer before globalSetup, so probing it here would
  // race our own preview server.
  for (const port of [BACKEND_PORT, STUB_WHISPER_PORT]) {
    await assertPortFree(port)
  }

  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'lucien-e2e-'))
  const backendBin = path.join(tmp, 'lucien')
  const stubBin = path.join(tmp, 'stub_whisper')
  execFileSync('go', ['build', '-o', backendBin, './cmd/lucien'], {
    cwd: REPO_ROOT,
    stdio: 'pipe',
  })
  execFileSync('go', ['build', '-o', stubBin, './tests/stubs/stub_whisper'], {
    cwd: REPO_ROOT,
    stdio: 'pipe',
  })

  const notesDir = path.join(tmp, 'notes')
  fs.mkdirSync(notesDir)
  const tokenHash = crypto.createHash('sha256').update(E2E_TOKEN).digest('hex')
  const configPath = path.join(tmp, 'config.toml')
  fs.writeFileSync(
    configPath,
    [
      'bind = "127.0.0.1"',
      `port = ${BACKEND_PORT}`,
      `notes_dir = ${JSON.stringify(notesDir)}`,
      `index_path = ${JSON.stringify(path.join(tmp, 'index.sqlite'))}`,
      `whisper_url = "http://127.0.0.1:${STUB_WHISPER_PORT}"`,
      // Fast backoff so card-5 polling specs do not wait out production delays.
      // (Must precede [[clients]]: TOML would nest them in that table.)
      'backoff_base_seconds = 1',
      'backoff_factor = 2',
      'backoff_cap_seconds = 5',
      'worker_idle_poll_seconds = 1',
      '[[clients]]',
      'name = "e2e"',
      `token_sha256 = "${tokenHash}"`,
    ].join('\n'),
  )

  const stub = spawnLogged(
    stubBin,
    ['-port', String(STUB_WHISPER_PORT), '-mode', 'ok'],
    path.join(tmp, 'stub_whisper.log'),
  )
  const backendArgs = ['--config', configPath]
  const backend = spawnLogged(backendBin, backendArgs, path.join(tmp, 'lucien.log'))
  // Specs that stop/restart the backend (health footer spec) find its pid
  // here: workers cannot reach the setup process's child handles directly.
  fs.writeFileSync(path.join(tmp, 'backend.pid'), String(backend.pid ?? 0))
  try {
    await waitForHealth()
  } catch (err) {
    // Leave nothing half-alive behind a failed startup.
    stub.kill('SIGKILL')
    backend.kill('SIGKILL')
    throw new Error(`${String(err)}; see ${path.join(tmp, 'lucien.log')}`, { cause: err })
  }

  // Specs that need to kill/restart the backend (health footer spec) drive
  // it through these handles via tests/e2e/server/backend.ts.
  process.env.LUCIEN_E2E_BACKEND_BIN = backendBin
  process.env.LUCIEN_E2E_BACKEND_ARGS = JSON.stringify(backendArgs)
  process.env.LUCIEN_E2E_TMP_DIR = tmp

  return async () => {
    // Also trust the pid file: a spec-restarted backend repointed it.
    const pidFile = path.join(tmp, 'backend.pid')
    if (fs.existsSync(pidFile)) {
      try {
        process.kill(Number(fs.readFileSync(pidFile, 'utf8')), 'SIGTERM')
      } catch {
        // already gone
      }
    }
    for (const child of [backend, stub]) {
      if (child.exitCode === null && child.signalCode === null) {
        child.kill('SIGTERM')
        await new Promise<void>((resolve) => {
          const timer = setTimeout(() => {
            child.kill('SIGKILL')
            resolve()
          }, 3000)
          child.once('exit', () => {
            clearTimeout(timer)
            resolve()
          })
        })
      }
    }
    fs.rmSync(tmp, { recursive: true, force: true })
  }
}
