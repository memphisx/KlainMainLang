package tests

import "testing"

// A tagged template's object is frozen, strings and raw alike, and a
// module var a nested function assigns its parameter holds the value.
func TestE2ETemplateObjectFrozenThroughAny(t *testing.T) {
	assertSameAsNodeCompatJS(t, `"use strict";
var t = null;
(function(p) { t = p; })`+"`a${1}b`"+`;
console.log(t, Object.isFrozen(t), Object.isFrozen(t.raw));
try { t.x = true; } catch (e) { console.log(e.constructor.name); }
try { t.raw.y = true; } catch (e) { console.log(e.constructor.name); }
console.log(t.x, t.raw.y);
var u = 0;
function tag(p) { u = p; }
tag(["x"]);
console.log(u);
`)
	assertSameAsNodeCompatJS(t, `"use strict";
const a = Object.freeze([1]);
const b = a;
try { b.y = 2; } catch (e) { console.log(e.constructor.name); }
`)
}
