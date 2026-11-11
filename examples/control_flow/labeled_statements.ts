// Any statement can carry a label: `break label` leaves it. A loop's labels
// (several may be stacked) also name its `continue`.
const seen = new Set(["Thessaloniki", "Kavala", "Volos"]);

search: {
  for (const city of seen) {
    if (city === "Kavala") {
      console.log("found", city);
      break search;
    }
  }
  console.log("not found");
}

rows: cells: for (let r = 0; r < 3; r++) {
  for (const city of seen) {
    if (city === "Kavala") continue rows;
    console.log(r, city);
  }
}
