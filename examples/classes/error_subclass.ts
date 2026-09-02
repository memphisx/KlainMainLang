// class X extends Error: user-defined error types with real
// throw/catch instanceof discrimination.

class HttpError extends Error {
  status: number;
  constructor(status: number, msg: string) {
    super(msg);
    this.name = "HttpError";
    this.status = status;
  }
}

class TimeoutError extends Error {}

function request(city: string): string {
  if (city === "Thessaloniki") {
    return "200 OK";
  }
  throw new HttpError(404, `no forecast for ${city}`);
}

console.log(request("Thessaloniki"));

try {
  request("Atlantis");
} catch (e) {
  if (e instanceof HttpError) {
    console.log(`${e.name} ${e.status}: ${e.message}`);
  }
  console.log(e instanceof TimeoutError); // false — siblings don't match
  console.log(e instanceof Error); // true
}

const t = new TimeoutError("gave up after 5s");
console.log(`${t}`); // Error: gave up after 5s (name defaults to "Error")
