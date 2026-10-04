// JSON.stringify with a replacer and a space chosen at run time, toJSON,
// and JSON.parse with a reviver — each as Node runs them.

interface Order {
  id: number
  total: number
  placed: Date
  secret: string
}

const order: Order = { id: 7, total: 19.5, placed: new Date(Date.UTC(2026, 0, 2)), secret: 'hidden' }

// A replacer function sees every key; returning undefined drops it.
console.log(JSON.stringify(order, (key, value) => (key === 'secret' ? undefined : value)))

// A property list keeps only the keys it names, in its order.
console.log(JSON.stringify(order, ['total', 'id']))

// The indent can come from configuration.
const indent = process.argv.length > 99 ? 4 : 2
console.log(JSON.stringify({ id: order.id, tags: ['a', 'b'] }, null, indent))

// A reviver rebuilds what stringify flattened.
const back = JSON.parse(JSON.stringify(order), (key, value) => (key === 'placed' ? new Date(value) : value))
console.log(back.placed instanceof Date, back.placed.toISOString())

// A cycle names its path, as V8 does.
const node: any = { name: 'root', child: { name: 'leaf' } }
node.child.parent = node
try {
  JSON.stringify(node)
} catch (e: any) {
  console.log(e.message)
}
