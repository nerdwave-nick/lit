// Keeps refreshes from running twice at once. Pure: register.tsx runs the refreshes.
//
//   const wait = gate.enter()
//   if (wait) return wait            // one is running: it runs once more, then `wait` resolves
//   do await refreshNow() while (gate.again())
export function refreshGate() {
  let running = false
  // Who waits for the run after the current one; non-null while that run is owed.
  let owed: (() => void)[] | null = null
  // Who waits for the current run.
  let current: (() => void)[] = []
  return {
    enter(): Promise<void> | null {
      if (!running) {
        running = true
        return null
      }
      return new Promise<void>(resolve => (owed ??= []).push(resolve))
    },
    // After each run: true when the caller must run once more for requests that came meanwhile.
    again(): boolean {
      for (const resolve of current) resolve()
      current = []
      if (owed) {
        ;[current, owed] = [owed, null]
        return true
      }
      running = false
      return false
    },
  }
}
