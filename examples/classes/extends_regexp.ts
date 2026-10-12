// class X extends RegExp: a pattern with methods of its own. The instance is
// the RegExp itself: test/exec, source/flags/lastIndex, the string methods
// that take a RegExp, instanceof and console.log behave as in Node.

class Slug extends RegExp {
  label: string;
  constructor(label: string) {
    super("[^a-z0-9]+", "g");
    this.label = label;
  }
  slugify(s: string): string {
    return s.toLowerCase().replace(this, "-").replace(/^-|-$/g, "");
  }
}

class Word extends RegExp {
  count(s: string): number {
    return (s.match(this) ?? []).length;
  }
}

const slug = new Slug("slug");
console.log(slug.slugify("Thessaloniki White Tower!"));
console.log(slug, slug.source, slug.flags, slug instanceof RegExp);

const word = new Word("\\w+", "g");
console.log(word.count("one two  three"), String(word), "a b".split(new Word("\\s")));

function describe(re: RegExp): string {
  return re instanceof Slug ? "a slug pattern" : "another pattern";
}
console.log(describe(slug), describe(word));
