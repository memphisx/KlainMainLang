// files — the view: the two-pane layout (a list of entries beside a preview
// pane) as a pure function of the current directory, its entries, and the
// cursor. The preview text comes from the fs layer (fs.ts).

import { Box, Text, List } from "klain:tui";
import { preview } from "./fs";

export function view(dir: string, entries: string[], cursor: number) {
  return Box(
    { flexDirection: "column", width: 72, height: 22, border: "round", borderColor: "cyan" },
    [
      Text(" " + dir, { color: "green", bold: true }),
      Box({ flexDirection: "row", flexGrow: 1 }, [
        Box({ width: 28, padding: 1, border: "single", borderColor: "blue" }, [
          List(entries, { selected: cursor }),
        ]),
        Box({ flexGrow: 1, padding: 1 }, [
          Text(preview(dir, entries[cursor]), { color: "gray" }),
        ]),
      ]),
      Text(" ↑/↓ move · Enter open · q quit", { color: "gray", dim: true }),
    ],
  );
}
