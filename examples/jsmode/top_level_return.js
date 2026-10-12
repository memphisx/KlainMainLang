// A CommonJS script may `return` at its top level: the rest of the file is
// skipped, but the process carries on as Node's does — the pending timer
// still fires. Run with:  klainmain -compat=js top_level_return.js

const config = { dryRun: true, city: "Thessaloniki" };

console.log(`planning a trip to ${config.city}`);
setTimeout(() => console.log("reminder: pack a jacket"), 5);

if (config.dryRun) {
  console.log("dry run: nothing booked");
  return;
}

console.log("booking the trip");
