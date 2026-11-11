// The pre-class inheritance pattern over Node's own classes: a constructor
// function runs EventEmitter's (Readable's) constructor on `this`, and
// util.inherits links its prototype to the class's.
const EventEmitter = require('events');
const { Readable } = require('stream');
const util = require('util');

function Thermometer(city) {
    EventEmitter.call(this);
    this.city = city;
}
util.inherits(Thermometer, EventEmitter);
Thermometer.prototype.report = function (celsius) {
    this.emit('reading', this.city, celsius);
};

const t = new Thermometer('Thessaloniki');
t.on('reading', (city, c) => console.log(`${city}: ${c}°C`));
t.report(21);
console.log(t instanceof EventEmitter, t instanceof Thermometer);

function Countdown(from) {
    Readable.call(this);
    this.n = from;
}
util.inherits(Countdown, Readable);
Countdown.prototype._read = function () {
    this.push(this.n > 0 ? String(this.n--) : null);
};
new Countdown(3).on('data', (d) => console.log('tick', String(d)));
