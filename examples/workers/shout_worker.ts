// Browser-shaped worker module for browser_echo.ts — the worker's `self`
// (onmessage/postMessage), no imports (compiled into the spawning example's
// binary).
self.onmessage = (e: { data: string }) => {
  self.postMessage(e.data + "!!!");
};
