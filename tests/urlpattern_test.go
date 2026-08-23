package tests

import (
	"testing"
)

// --- URLPattern (TDD-00100 / ADR-00311) ---

func TestE2EURLPatternTestAndComponents(t *testing.T) {
	assertOutput(t, `
const pattern = new URLPattern({ pathname: "/books/:id" })
console.log(pattern.pathname)
console.log(pattern.protocol)
console.log(pattern.test("https://example.com/books/123"))
console.log(pattern.test("https://example.com/authors/9"))
console.log(pattern.test("not a url"))
`, "/books/:id\n*\ntrue\nfalse\nfalse")
}

func TestE2EURLPatternExecGroups(t *testing.T) {
	assertOutput(t, `
const pattern = new URLPattern({ pathname: "/books/:id" })
const m = pattern.exec("https://example.com/books/123")
if (m !== null) {
  console.log(m.get("id"))
}
console.log(pattern.exec("https://example.com/nope") === null)
`, "123\ntrue")
}

func TestE2EURLPatternMultiComponentAndOptionalGroup(t *testing.T) {
	assertOutput(t, `
const api = new URLPattern({ protocol: "https", hostname: "api.example.com", pathname: "/v1/:resource/:id?" })
console.log(api.test("https://api.example.com/v1/users/42"))
console.log(api.test("https://api.example.com/v1/users"))
console.log(api.test("http://api.example.com/v1/users/42"))
console.log(api.test("https://other.example.com/v1/users/42"))
const g = api.exec("https://api.example.com/v1/users/42")
if (g !== null) {
  console.log(g.get("resource"))
  console.log(g.get("id"))
}
const only = api.exec("https://api.example.com/v1/users")
if (only !== null) {
  console.log(only.get("resource"))
  console.log(only.get("id") === null)
}
`, "true\ntrue\nfalse\nfalse\nusers\n42\nusers\ntrue")
}

func TestE2EURLPatternDefaultsAndEmptyComponent(t *testing.T) {
	assertOutput(t, `
console.log(new URLPattern().test("https://anything.example/x?q=1#f"))
console.log(new URLPattern({ search: "" }).test("https://example.com/a?q=1"))
console.log(new URLPattern({ search: "" }).test("https://example.com/a"))
`, "true\nfalse\ntrue")
}

func TestE2EURLPatternInvalidPatternThrowsTypeError(t *testing.T) {
	assertOutput(t, `
try {
  const bad = new URLPattern({ pathname: "/x/{group}" })
  console.log(bad.test("https://e.com/x/y"))
} catch (e) {
  console.log(e instanceof TypeError)
}
`, "true")
}
