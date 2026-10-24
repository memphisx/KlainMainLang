// A typed Map passed where a Map<any, any> is expected is the same object:
// reads see its entries, and writes through the parameter reach the caller.
function addDefaults(settings: Map<any, any>): void {
  if (!settings.has("retries")) settings.set("retries", 3);
  console.log("inside:", settings.get("timeout"), settings.size);
}

const settings = new Map<string, number>([["timeout", 30]]);
addDefaults(settings);
console.log("outside:", settings, settings.get("retries"));
