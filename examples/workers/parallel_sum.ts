// Workers (worker_threads): spawn two OS threads, split an array between
// them, and combine the partial sums as the replies arrive.
import { Worker } from 'worker_threads';

const a = new Worker('./sum_worker.ts', { workerData: "left" });
const b = new Worker('./sum_worker.ts', { workerData: "right" });

const left: number[] = [1, 2, 3, 4, 5];
const right: number[] = [6, 7, 8, 9, 10];

let total = 0;
let replies = 0;
const finish = () => {
  replies++;
  if (replies === 2) {
    console.log("total: " + total);
  }
};

a.on('message', (partial: number) => { total += partial; a.terminate(); finish(); });
b.on('message', (partial: number) => { total += partial; b.terminate(); finish(); });

a.postMessage(left);
b.postMessage(right);
