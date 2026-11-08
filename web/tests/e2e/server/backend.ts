// Backend control for specs (card web-1 health.spec): stop the harness
// backend by pid, restart it with the same binary and config, and make sure
// it is alive again when the spec file ends. Only one spec file may use
// this; the suite runs with a single worker (playwright.config).

import { spawn } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'

const bin = process.env.LUCIEN_E2E_BACKEND_BIN
const args = JSON.parse(process.env.LUCIEN_E2E_BACKEND_ARGS ?? '[]') as string[]
const tmp = process.env.LUCIEN_E2E_TMP_DIR

const BACKEND_PORT = 18188
let restarted: ReturnType<typeof spawn> | null = null

function pidFilePath(): string {
  if (!tmp) throw new Error('LUCIEN_E2E_TMP_DIR missing; run via pnpm test:e2e')
  return path.join(tmp, 'backend.pid')
}

async function waitUntilDown(timeoutMs = 10_000): Promise<void> {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    try {
      await fetch(`http://127.0.0.1:${BACKEND_PORT}/v1/health`)
      await new Promise((r) => setTimeout(r, 100))
    } catch {
      return
    }
  }
  throw new Error('backend did not stop within the deadline')
}

async function waitUntilUp(timeoutMs = 15_000): Promise<void> {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    try {
      const res = await fetch(`http://127.0.0.1:${BACKEND_PORT}/v1/health`)
      if (res.ok) return
    } catch {
      // not up yet
    }
    await new Promise((r) => setTimeout(r, 200))
  }
  throw new Error('backend did not come back up within the deadline')
}

export async function stopBackend(): Promise<void> {
  try {
    process.kill(Number(fs.readFileSync(pidFilePath(), 'utf8')), 'SIGTERM')
  } catch {
    // already stopped by a previous call
  }
  await waitUntilDown()
}

export async function startBackend(): Promise<void> {
  if (!bin) throw new Error('LUCIEN_E2E_BACKEND_BIN missing; run via pnpm test:e2e')
  if (restarted && restarted.exitCode === null) return
  const out = fs.openSync(path.join(tmp as string, 'lucien.restarted.log'), 'a')
  restarted = spawn(bin, args, { stdio: ['ignore', out, out] })
  fs.writeFileSync(pidFilePath(), String(restarted.pid ?? 0))
  await waitUntilUp()
}

export async function ensureBackendUp(): Promise<void> {
  try {
    const res = await fetch(`http://127.0.0.1:${BACKEND_PORT}/v1/health`)
    if (res.ok) return
  } catch {
    // fall through to restart
  }
  await startBackend()
}
