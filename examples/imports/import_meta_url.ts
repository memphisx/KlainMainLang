// import.meta.url (TDD-00055 Stage 1) — resolved entirely at compile time,
// per file, into a plain string literal (this file's own absolute path as
// a file:// URL, matching real Node/browser convention) — codegen never
// sees a real "module metadata object". Only the exact `import.meta.url`
// form is supported: bare `import.meta` or any other member
// (`import.meta.resolve`, etc.) is a clean parse-time error instead.

console.log(import.meta.url.startsWith("file://"))
console.log(import.meta.url.endsWith("import_meta_url.ts"))
