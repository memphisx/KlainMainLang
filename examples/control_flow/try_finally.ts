// try/finally: the finally block runs on EVERY exit from try/catch — including
// an early `return`, `break`, or `continue`, not just falling off the end.

// --- finally runs even when try returns ---
function readWithCleanup(): number {
    try {
        console.log("open resource")
        return 42          // finally still runs before the function returns
    } finally {
        console.log("close resource")
    }
}
console.log(readWithCleanup())
// open resource
// close resource
// 42

// --- the returned value is captured before finally runs ---
function captured(): number {
    let x = 1
    try {
        return x           // returns 1...
    } finally {
        x = 99             // ...this later write doesn't change the returned value
    }
}
console.log(captured())    // 1

// --- a return in finally overrides the try's return (JS semantics) ---
function overridden(): number {
    try {
        return 1
    } finally {
        return 9           // this one wins
    }
}
console.log(overridden())  // 9

// --- nested finallys run innermost-first ---
function nested(): number {
    try {
        try {
            return 5
        } finally {
            console.log("inner cleanup")
        }
    } finally {
        console.log("outer cleanup")
    }
}
console.log(nested())
// inner cleanup
// outer cleanup
// 5

// --- break/continue in a loop also run the loop-nested finally ---
for (let i = 0; i < 3; i++) {
    try {
        if (i === 1) { continue }   // still runs this iteration's finally
        console.log("body " + i)
    } finally {
        console.log("iter cleanup " + i)
    }
}
// body 0
// iter cleanup 0
// iter cleanup 1
// body 2
// iter cleanup 2
