// Allocator path: (almost) none — this is the n-body integrator from the
// Computer Language Benchmarks Game (the suite PerryTS benchmarks against). It's
// float-heavy arithmetic over a fixed set of bodies with no per-iteration
// allocation, so it isolates raw compute/FPU throughput from memory management:
// the modes should be indistinguishable here, and it's the fair speed baseline
// against Node/PerryTS where a GC has nothing to do.

const SOLAR_MASS = 4 * Math.PI * Math.PI;
const DAYS_PER_YEAR = 365.24;

class Body {
  x: number; y: number; z: number;
  vx: number; vy: number; vz: number;
  mass: number;
  constructor(x: number, y: number, z: number, vx: number, vy: number, vz: number, mass: number) {
    this.x = x; this.y = y; this.z = z;
    this.vx = vx; this.vy = vy; this.vz = vz;
    this.mass = mass;
  }
}

function makeBodies(): Body[] {
  const sun = new Body(0, 0, 0, 0, 0, 0, SOLAR_MASS);
  const jupiter = new Body(4.8414314424647209, -1.16032004402742839, -0.103622044471123109,
    0.00166007664274403694 * DAYS_PER_YEAR, 0.00769901118419740425 * DAYS_PER_YEAR,
    -0.0000690460016972063023 * DAYS_PER_YEAR, 0.000954791938424326609 * SOLAR_MASS);
  const saturn = new Body(8.34336671824457987, 4.12479856412430479, -0.403523417114321381,
    -0.00276742510726862411 * DAYS_PER_YEAR, 0.00499852801234917238 * DAYS_PER_YEAR,
    0.0000230417297573763929 * DAYS_PER_YEAR, 0.000285885980666130812 * SOLAR_MASS);
  const bodies = [sun, jupiter, saturn];
  let px = 0, py = 0, pz = 0;
  for (let i = 0; i < bodies.length; i++) {
    px += bodies[i].vx * bodies[i].mass;
    py += bodies[i].vy * bodies[i].mass;
    pz += bodies[i].vz * bodies[i].mass;
  }
  sun.vx = -px / SOLAR_MASS;
  sun.vy = -py / SOLAR_MASS;
  sun.vz = -pz / SOLAR_MASS;
  return bodies;
}

function energy(bodies: Body[]): number {
  let e = 0;
  for (let i = 0; i < bodies.length; i++) {
    const bi = bodies[i];
    e += 0.5 * bi.mass * (bi.vx * bi.vx + bi.vy * bi.vy + bi.vz * bi.vz);
    for (let j = i + 1; j < bodies.length; j++) {
      const bj = bodies[j];
      const dx = bi.x - bj.x, dy = bi.y - bj.y, dz = bi.z - bj.z;
      const d = Math.sqrt(dx * dx + dy * dy + dz * dz);
      e -= (bi.mass * bj.mass) / d;
    }
  }
  return e;
}

function advance(bodies: Body[], dt: number): void {
  for (let i = 0; i < bodies.length; i++) {
    const bi = bodies[i];
    for (let j = i + 1; j < bodies.length; j++) {
      const bj = bodies[j];
      const dx = bi.x - bj.x, dy = bi.y - bj.y, dz = bi.z - bj.z;
      const d2 = dx * dx + dy * dy + dz * dz;
      const mag = dt / (d2 * Math.sqrt(d2));
      bi.vx -= dx * bj.mass * mag; bi.vy -= dy * bj.mass * mag; bi.vz -= dz * bj.mass * mag;
      bj.vx += dx * bi.mass * mag; bj.vy += dy * bi.mass * mag; bj.vz += dz * bi.mass * mag;
    }
  }
  for (let i = 0; i < bodies.length; i++) {
    const b = bodies[i];
    b.x += dt * b.vx; b.y += dt * b.vy; b.z += dt * b.vz;
  }
}

// BENCH_SCALE (default 1) scales the workload identically across every engine and
// is opaque to the optimizer, so the loops can't be constant-folded away.
const scale = parseInt(process.env.BENCH_SCALE ?? "1");
const bodies = makeBodies();
const steps = 1500000 * scale;
for (let i = 0; i < steps; i++) advance(bodies, 0.01);
// Print to 9 decimals so it matches the classic benchmark's checksum.
console.log("nbody checksum: " + energy(bodies).toFixed(9));
