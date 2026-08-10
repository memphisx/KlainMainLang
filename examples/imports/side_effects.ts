// Top-level side-effecting code in imported files (TDD-00052) —
// side_effects_setup.ts's own top-level `console.log` runs once, in
// dependency order, strictly before this file's own top-level statements.
// (A file that genuinely participates in an import *cycle* keeps a
// stricter, declarations-only-plus-literal-only-initializers rule instead
// — see TDD-00052's Design section for why a cycle can't get the same
// freedom safely.)

import { ready } from './side_effects_setup'

console.log("main: ready = " + ready())
