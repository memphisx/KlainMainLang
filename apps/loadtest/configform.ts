// configform — the interactive config screen (first TUI screen).
//
// A small component: FormState holds the editable values + which field has
// focus; handleKey mutates it in response to a keystroke and returns whether to
// keep editing, start the run, or quit; renderForm projects it to a klain:tui
// tree. Text fields are edited char-by-char (klain:tui's TextInput is
// display-only), method cycles with ←/→, duration takes digits or [i] for
// infinite, and headers are a small add-list.

import { Box, Text, TextInput } from "klain:tui";
import { Config } from "./config";

const METHODS = ["GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"];
const NFIELDS = 7; // 0 url, 1 method, 2 agents, 3 duration, 4 headers, 5 body, 6 start

export interface FormState {
  url: string;
  methodIdx: number;
  agents: string;
  durationSecs: string;
  infinite: boolean;
  headers: string[];
  headerDraft: string;
  body: string;
  focused: number;
}

export function newForm(cfg: Config): FormState {
  let mi = 0;
  for (let i = 0; i < METHODS.length; i++) if (METHODS[i] === cfg.method) mi = i;
  return {
    url: cfg.url === " help" ? "" : cfg.url,
    methodIdx: mi,
    agents: cfg.concurrency + "",
    durationSecs: cfg.infinite ? "" : Math.round(cfg.durationMs / 1000) + "",
    infinite: cfg.infinite,
    headers: cfg.headers,
    headerDraft: "",
    body: cfg.body,
    focused: 0,
  };
}

function chop(s: string): string {
  if (s.length === 0) return s;
  return s.substring(0, s.length - 1);
}

export function handleKey(f: FormState, key: string): string {
  if (key === "") return "editing";
  const code = key.charCodeAt(0);
  if (code === 3) return "quit";          // Ctrl-C
  if (key === "\x1b") return "quit";      // Esc (bare)
  if (key === "\x1b[A") { f.focused = (f.focused + NFIELDS - 1) % NFIELDS; return "editing"; }
  if (key === "\x1b[B") { f.focused = (f.focused + 1) % NFIELDS; return "editing"; }
  if (key === "\x1b[C") { if (f.focused === 1) f.methodIdx = (f.methodIdx + 1) % METHODS.length; return "editing"; }
  if (key === "\x1b[D") { if (f.focused === 1) f.methodIdx = (f.methodIdx + METHODS.length - 1) % METHODS.length; return "editing"; }
  if (key === "\r" || key === "\n") {
    if (f.focused === 6) return "start";
    if (f.focused === 4) {
      const d = f.headerDraft.trim();
      if (d !== "") { f.headers.push(d); f.headerDraft = ""; }
      return "editing";
    }
    f.focused = (f.focused + 1) % NFIELDS;
    return "editing";
  }
  if (code === 127 || code === 8) { // backspace
    if (f.focused === 0) f.url = chop(f.url);
    else if (f.focused === 2) f.agents = chop(f.agents);
    else if (f.focused === 3) f.durationSecs = chop(f.durationSecs);
    else if (f.focused === 4) {
      if (f.headerDraft !== "") f.headerDraft = chop(f.headerDraft);
      else if (f.headers.length > 0) f.headers.pop();
    } else if (f.focused === 5) f.body = chop(f.body);
    return "editing";
  }
  if (code >= 32 && code < 127) { // printable
    if (f.focused === 0) f.url = f.url + key;
    else if (f.focused === 2) { if (code >= 48 && code <= 57) f.agents = f.agents + key; }
    else if (f.focused === 3) {
      if (key === "i" || key === "I") f.infinite = !f.infinite;
      else if (code >= 48 && code <= 57) { f.durationSecs = f.durationSecs + key; f.infinite = false; }
    } else if (f.focused === 4) f.headerDraft = f.headerDraft + key;
    else if (f.focused === 5) f.body = f.body + key;
    // method (1) / start (6): printable ignored
    return "editing";
  }
  return "editing";
}

export function formToConfig(f: FormState): Config {
  let agents = parseInt(f.agents, 10);
  if (Number.isNaN(agents) || agents < 1) agents = 1;
  let durMs = 10000;
  const secs = parseFloat(f.durationSecs);
  if (!Number.isNaN(secs) && secs > 0) durMs = Math.round(secs * 1000);
  return {
    url: f.url,
    method: METHODS[f.methodIdx],
    concurrency: agents,
    durationMs: durMs,
    infinite: f.infinite,
    headers: f.headers,
    body: f.body,
    runNow: true,
    durationSet: true,
  };
}

// --- rendering ---

// A row's marker + label. Nodes are returned (never passed as params — a
// klain:tui node has no nameable TS type, only `ptr`, so it can't annotate a
// parameter; it can only be returned and placed in a children array).
function marker(focused: boolean) {
  return focused ? Text("›", { color: "cyan", bold: true, width: 2 }) : Text(" ", { width: 2 });
}
function label(text: string, focused: boolean) {
  return focused ? Text(text, { color: "cyan", width: 12 }) : Text(text, { color: "gray", width: 12 });
}
function textValue(value: string, focused: boolean, placeholder: string) {
  if (focused) return TextInput(value, { color: "white" });
  if (value === "") return Text(placeholder, { color: "gray", dim: true });
  return Text(value, { color: "white" });
}

export function renderForm(f: FormState) {
  const title = Text("klainload — configure the test", { color: "green", bold: true });

  const urlRow = Box({ flexDirection: "row", gap: 1 }, [
    marker(f.focused === 0), label("Target URL", f.focused === 0),
    textValue(f.url, f.focused === 0, "http://…"),
  ]);
  const methodStr = "‹ " + METHODS[f.methodIdx] + " ›";
  const methodText = f.focused === 1
    ? Text(methodStr, { color: "cyan", bold: true })
    : Text(methodStr, { color: "white", bold: true });
  const methodRow = Box({ flexDirection: "row", gap: 1 }, [
    marker(f.focused === 1), label("Method", f.focused === 1), methodText,
  ]);
  const agentsRow = Box({ flexDirection: "row", gap: 1 }, [
    marker(f.focused === 2), label("Agents", f.focused === 2),
    textValue(f.agents, f.focused === 2, "8"),
  ]);
  const durVal = f.infinite
    ? Text("∞ infinite", { color: "yellow", bold: true })
    : textValue(f.durationSecs === "" ? "" : f.durationSecs + "s", f.focused === 3, "10s");
  const durRow = Box({ flexDirection: "row", gap: 1 }, [
    marker(f.focused === 3), label("Duration", f.focused === 3),
    durVal, Text("[i]=infinite", { color: "gray", dim: true }),
  ]);

  // headers: the add-input plus the committed list
  const hdrChildren = [textValue(f.headerDraft, f.focused === 4, "Name: Value  (Enter adds)")];
  for (let i = 0; i < f.headers.length; i++) {
    hdrChildren.push(Text("  • " + f.headers[i], { color: "gray" }));
  }
  const hdrRow = Box({ flexDirection: "row", gap: 1 }, [
    marker(f.focused === 4), label("Headers", f.focused === 4),
    Box({ flexDirection: "column" }, hdrChildren),
  ]);

  const bodyRow = Box({ flexDirection: "row", gap: 1 }, [
    marker(f.focused === 5), label("Body", f.focused === 5),
    textValue(f.body, f.focused === 5, "(none)"),
  ]);

  const startNode = f.focused === 6
    ? Text("[ Start test ]", { color: "green", bold: true, inverse: true })
    : Text("[ Start test ]", { color: "green", bold: true });
  const startRow = Box({ flexDirection: "row", gap: 1 }, [Text("  ", { width: 2 }), startNode]);

  const footer = Text("↑/↓ move · ←/→ change method · Enter next/add/start · Esc quit", { color: "gray", dim: true });

  return Box(
    { flexDirection: "column", width: 64, border: "round", borderColor: "cyan", padding: 1 },
    [
      title,
      Box({ height: 1 }, []),
      urlRow, methodRow, agentsRow, durRow, hdrRow, bodyRow,
      Box({ height: 1 }, []),
      startRow,
      Box({ height: 1 }, []),
      footer,
    ],
  );
}
