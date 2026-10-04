// A Date's time value is clipped to +-8.64e15 ms: setTime past that range,
// or with NaN or an infinity, makes an Invalid Date, whose getters are all
// NaN. A `var` declared in a loop body is the same Date after the loop.
// Run with:  klainmain -compat=js invalid_dates.js

const departures = [0, 1.5e12, 8.64e15, 8.64e15 + 1, Infinity];
for (var i = 0; i < departures.length; i++) {
  var d = new Date(0);
  console.log(d.setTime(departures[i]), d.getUTCFullYear(), d.getUTCMonth());
}
console.log("last:", d.getTime());

const invalid = new Date(NaN);
console.log(invalid.setUTCMonth(5), invalid.setUTCFullYear(2026), invalid.toISOString());
