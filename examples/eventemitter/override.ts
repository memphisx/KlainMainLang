// Overriding an EventEmitter method (TDD-00157): a class in an
// EventEmitter-rooted tree may declare `emit`/`on`/`off`/... to wrap the
// built-in behavior — the single most common real-world subclass idiom.
// The override wins at every call site (statically and through the vtable),
// and `super.<method>(...)` reaches the underlying dispatch.
class MetricsBus extends EventEmitter<[string, number]> {
  emitted: number = 0;
  attached: number = 0;

  // Wrap emit to count every dispatch, then delegate to the real one.
  emit(event: string, name: string, value: number): boolean {
    this.emitted++;
    return super.emit(event, name, value);
  }

  // Wrap on to count subscriptions; stays chainable via super.on's `this`.
  on(event: string, listener: (name: string, value: number) => void): MetricsBus {
    this.attached++;
    return super.on(event, listener);
  }
}

const bus = new MetricsBus();
bus
  .on("metric", (name, value) => console.log("metric " + name + " = " + value))
  .on("metric", (name, value) => console.log("  (mirror) " + name));

bus.emit("metric", "cpu", 42);
bus.emit("metric", "mem", 71);

console.log("emitted: " + bus.emitted);
console.log("attached: " + bus.attached);
console.log("listeners: " + bus.listenerCount("metric"));
