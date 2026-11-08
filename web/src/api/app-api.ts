// App-level API wiring (FRONTEND-DESIGN.md 6, 9): every client call made
// through useAppApi() passes ApiErrors to the unauthorized handler, which
// toasts once and routes to /settings. The handler is injected so unit
// tests feed it stubbed rejections without Vue or Element Plus.

import { createClient, ApiError, type Api } from './client'

export interface UnauthorizedHandler {
  handle(err: unknown): boolean
  reset(): void
}

export interface UnauthorizedDeps {
  toast: (message: string) => void
  goSettings: () => void
}

export const AUTH_TOAST_TEXT = 'authentication failed, check the token'

export function createUnauthorizedHandler(deps: UnauthorizedDeps): UnauthorizedHandler {
  let armed = true
  return {
    handle(err: unknown): boolean {
      if (!(err instanceof ApiError) || err.status !== 401) return false
      if (armed) {
        armed = false
        deps.toast(AUTH_TOAST_TEXT)
        deps.goSettings()
      }
      return true
    },
    reset() {
      armed = true
    },
  }
}

export function createAppApi(base: Api, handler: UnauthorizedHandler): Api {
  const wrapped: Record<string, unknown> = {}
  for (const key of Object.keys(base) as (keyof Api)[]) {
    const fn = base[key] as unknown as (...args: unknown[]) => Promise<unknown>
    wrapped[key] = async (...args: unknown[]) => {
      try {
        return await fn(...args)
      } catch (err) {
        handler.handle(err)
        throw err
      }
    }
  }
  return wrapped as unknown as Api
}

// ---- app singleton -----------------------------------------------------
// main.ts installs it after Pinia is live; views call useAppApi().

let installed: { api: Api; handler: UnauthorizedHandler } | null = null

export function installAppApi(base: Api, handler: UnauthorizedHandler): void {
  installed = { api: createAppApi(base, handler), handler }
}

export function useAppApi(): Api {
  if (!installed) throw new Error('app api not installed; call installAppApi in main.ts')
  return installed.api
}

// Called when the token is edited (or its toast closes) so the next 401 can
// alert again; a queued burst while one toast is up stays suppressed.
export function resetAuthHandler(): void {
  installed?.handler.reset()
}

export { createClient }
