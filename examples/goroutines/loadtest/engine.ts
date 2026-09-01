// engine — the load-generation goroutines.
//
// `startRun` spawns `concurrency` worker goroutines. Each is a closed loop:
// check the shared `stop` channel (non-blocking), fire one synchronous request,
// measure it, and stream a Result out — until `stop` is closed (the entry
// closes it at the duration deadline or when the user stops manually), at which
// point the worker signals `done`. There is no fixed request count and no
// request queue: a load tester runs for a duration or until stopped, hammering
// as fast as the target answers.
//
// Headers arrive as one newline-joined blob (a closure can't capture an array
// variable); each worker splits it locally and applies the pairs per request.

import { go, Channel, select, defaultCase } from "klain:sync";
import { Result } from "./stats";

export function startRun(
  concurrency: number,
  method: string,
  url: string,
  headerBlob: string,
  body: string,
  results: Channel<Result>,
  done: Channel<number>,
  stop: Channel<number>,
): void {
  for (let w = 0; w < concurrency; w++) {
    spawnWorker(method, url, headerBlob, body, results, done, stop);
  }
}

function spawnWorker(
  method: string,
  url: string,
  headerBlob: string,
  body: string,
  results: Channel<Result>,
  done: Channel<number>,
  stop: Channel<number>,
): void {
  go(() => {
    for (;;) {
      // Non-blocking stop check: once `stop` is closed, its recvCase fires for
      // every worker, so all agents halt together.
      let stopped = false;
      select(
        stop.recvCase((_: number) => { stopped = true; }),
        defaultCase(() => {}),
      );
      if (stopped) break;

      const t0 = performance.now();
      const xhr = new XMLHttpRequest();
      xhr.open(method, url, false); // async: false — a real blocking request
      if (headerBlob !== "") {
        const lines = headerBlob.split("\n"); // local array — fine inside a closure
        for (let h = 0; h < lines.length; h++) {
          const line = lines[h];
          const idx = line.indexOf(":");
          if (idx > 0) {
            const name = line.substring(0, idx).trim();
            const value = line.substring(idx + 1).trim();
            if (name !== "") xhr.setRequestHeader(name, value);
          }
        }
      }
      if (body !== "") xhr.send(body);
      else xhr.send();

      const latencyUs = Math.round((performance.now() - t0) * 1000);
      // status is 0 on a connection/transport failure; send() never throws.
      results.send({ latencyUs: latencyUs, status: xhr.status, bytes: xhr.responseText.length });
    }
    done.send(1);
  });
}
