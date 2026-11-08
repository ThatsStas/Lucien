// Vitest setup: a Map-backed localStorage.
// Node 26 ships a global `localStorage` that answers undefined without
// --localstorage-file and shadows jsdom's, so every unit test gets this
// deterministic stub instead (card web-1: "unit-test the store against a
// localStorage stub").

class MemoryStorage {
  private map = new Map<string, string>()

  get length() {
    return this.map.size
  }

  getItem(key: string): string | null {
    return this.map.has(key) ? this.map.get(key)! : null
  }

  setItem(key: string, value: string): void {
    this.map.set(key, String(value))
  }

  removeItem(key: string): void {
    this.map.delete(key)
  }

  clear(): void {
    this.map.clear()
  }

  key(index: number): string | null {
    return Array.from(this.map.keys())[index] ?? null
  }
}

for (const name of ['localStorage', 'sessionStorage'] as const) {
  Object.defineProperty(globalThis, name, {
    value: new MemoryStorage(),
    writable: true,
    configurable: true,
  })
}
