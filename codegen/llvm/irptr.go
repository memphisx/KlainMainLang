package llvm

// irPtr is the IR spelling of a managed reference: a pointer in address
// space 1, apart from code and stack slots (TDD-00230 P3.3). Every Type
// that holds a heap reference, and every IR text that names one, spells
// it through this constant.
const irPtr = "ptr addrspace(1)"

// irGlobalAS places a managed static object (string constants, shapes)
// in the managed address space.
const irGlobalAS = " addrspace(1)"

// irAddrCast converts between a program-address-space pointer (code, a
// stack slot) and a managed reference.
const irAddrCast = "addrspacecast"

// managedSpace reports whether managed references have their own address
// space; false spells them as plain pointers (irPtr "ptr", irGlobalAS "",
// irAddrCast "bitcast").
const managedSpace = true
