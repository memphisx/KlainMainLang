// A live system monitor — a `htop`-lite that redraws on a timer, showing the
// self-refreshing `klain:tui` loop pattern (as opposed to the input-driven
// task manager). This is the shape a CLI/Docker resource dashboard takes.
//
//   ./klaintop              # interactive: refreshes ~once a second, q quits
//   ./klaintop </dev/null   # non-TTY: paints one sample and exits
//
// The trick is `readKey(1000)` from `klain:tty`: it waits up to a second for a
// keystroke and returns "" if none came — so the loop wakes to repaint on a
// tick *and* responds to keys, without a background thread. CPU utilisation is
// the classic delta of busy-vs-idle jiffies between two samples of `os.cpus()`.

import { Box, Text, Progress, Spinner, render, enter, leave } from "klain:tui";
import { readKey } from "klain:tty";
import { cpus, totalmem, freemem, hostname, platform } from "os";

// Aggregate { total, idle } jiffies across every core.
function sample(): { total: number; idle: number } {
  const cs = cpus();
  let total = 0;
  let idle = 0;
  for (let i = 0; i < cs.length; i++) {
    const t = cs[i].times;
    total = total + t.user + t.nice + t.sys + t.idle + t.irq;
    idle = idle + t.idle;
  }
  return { total, idle };
}

// Style-enum props (colours, border, …) are compile-time literals in Stage 1,
// so the two bars are written out rather than factored behind a `color` param.
function cpuBar(frac: number) {
  return Box({ flexDirection: "row", gap: 1 }, [
    Text("CPU", { color: "gray", width: 4 }),
    Progress(frac, { color: "yellow", width: 24 }),
    Text(Math.round(frac * 100) + "%", { color: "yellow", width: 5 }),
  ]);
}
function memBar(frac: number) {
  return Box({ flexDirection: "row", gap: 1 }, [
    Text("MEM", { color: "gray", width: 4 }),
    Progress(frac, { color: "magenta", width: 24 }),
    Text(Math.round(frac * 100) + "%", { color: "magenta", width: 5 }),
  ]);
}

function view(cpuFrac: number, memFrac: number, tick: number, cores: number) {
  const totalMB = Math.round(totalmem() / 1048576);
  const usedMB = Math.round((totalmem() - freemem()) / 1048576);
  return Box(
    { flexDirection: "column", width: 42, border: "round", borderColor: "cyan", padding: 1 },
    [
      Box({ flexDirection: "row", justifyContent: "space-between" }, [
        Text("klaintop", { color: "green", bold: true }),
        Text(hostname() + " · " + platform(), { color: "gray", dim: true }),
      ]),
      Box({ height: 1 }, []),
      cpuBar(cpuFrac),
      memBar(memFrac),
      Box({ height: 1 }, []),
      Box({ flexDirection: "row", justifyContent: "space-between" }, [
        Text(usedMB + " / " + totalMB + " MB · " + cores + " cores", { color: "gray" }),
        Spinner(tick, { label: "live", color: "blue" }),
      ]),
      Box({ height: 1 }, []),
      Text("q quit", { color: "gray", dim: true }),
    ],
  );
}

// totalmem()/freemem() are byte counts as integers, so force a floating-point
// division (multiply by 1.0) rather than truncating to 0.
function memFrac(): number {
  const total = totalmem();
  return total > 0 ? ((total - freemem()) * 1.0) / total : 0;
}

let prev = sample();
const cores = cpus().length;
let tick = 0;

enter();
render(view(0, memFrac(), tick, cores));

if (!process.stdin.isTTY) {
  leave();
} else {
  process.stdin.setRawMode(true);
  let running = true;
  while (running) {
    const key: string = readKey(1000); // wake on a key OR after ~1s
    if (key === "q" || key.charCodeAt(0) === 3) {
      running = false;
    } else {
      const cur = sample();
      const dTotal = cur.total - prev.total;
      const dIdle = cur.idle - prev.idle;
      const cpuFrac = dTotal > 0 ? 1 - (dIdle * 1.0) / dTotal : 0;
      prev = cur;
      tick = tick + 1;
      render(view(cpuFrac, memFrac(), tick, cores));
    }
  }
  leave();
}
