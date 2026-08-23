// Browser-shaped worker module for browser_echo.ts — ambient onmessage/
// postMessage, no imports (compiled into the spawning example's binary).
onmessage = (e: { data: string }) => {
  postMessage(e.data + "!!!");
};
